//go:build smoke

package smoke

import (
	"context"
	"fmt"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"golang.org/x/sync/errgroup"
)

// TestLogStreamsOfASeedGroup opens the streams of a seed group from the
// group list, and the detail of a stream.
func TestLogStreamsOfASeedGroup(t *testing.T) {
	h := start(t, "")
	h.open("logs", "Log Groups")
	h.filter("/ecs/web")
	h.waitFor("/ecs/web")
	h.keys("v")
	h.waitFor("web/app/1", "web/app/2", "2/2 log streams")
	h.keys("d")
	h.waitFor(":log-stream:web/app/") // the ARN shows only in the detail
}

// TestLogStreamsPaging loads more streams than the budget of the list
// (4 pages of 50), and l loads the rest.
func TestLogStreamsPaging(t *testing.T) {
	const count = 210
	group := "/smoke/" + uniqueName("streams")
	createLogStreams(t, group, count)

	h := start(t, "")
	h.open("logs", "Log Groups")
	h.filter(group)
	h.waitFor(group)
	h.keys("v")
	h.waitFor("200/200 log streams · l for more")
	h.keys("l")
	h.waitFor(fmt.Sprintf("%d/%d log streams", count, count))
	h.waitGone("l for more")
}

// TestLogEventsPageAStream opens a stream with 2,500 events and pages back
// with l until the footer says "start of stream".
func TestLogEventsPageAStream(t *testing.T) {
	group := "/smoke/" + uniqueName("events")
	createLogEvents(t, group, "app/1", 2500)

	h := start(t, "")
	h.open("logs", "Log Groups")
	h.filter(group)
	h.waitFor(group)
	h.keys("v")
	h.waitFor("1/1 log streams")
	h.enter()
	h.waitFor("1000 events ·", "l for older")
	h.keys("l")
	h.waitFor("2000 events ·")
	h.keys("l")
	h.waitFor("2500 events ·", "start of stream")
}

// TestLogEventsFilterPattern opens a group with :logs NAME and sets a
// filter pattern with f. Every tenth event has ERROR.
func TestLogEventsFilterPattern(t *testing.T) {
	group := "/smoke/" + uniqueName("pattern")
	createLogEvents(t, group, "app/1", 500)

	h := start(t, "")
	h.keys(":logs " + group)
	h.enter()
	h.waitFor("500 events ·")
	h.keys("f")
	h.keys("ERROR")
	h.enter()
	h.waitFor("filter: ERROR", "50 events ·")
}

// LocalStack has no live tail, so F polls FilterLogEvents with the
// endpoint override (decision D5). An event put after the start shows.
func TestLogEventsLiveTailPolls(t *testing.T) {
	group := "/smoke/" + uniqueName("tail")
	createLogEvents(t, group, "app/1", 20)

	h := start(t, "")
	h.keys(":logs " + group)
	h.enter()
	h.waitFor("20 events ·")
	h.keys("F")
	h.waitFor("POLL ")

	logs := services(t).Client.LogsClient()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := logs.PutLogEvents(ctx, &cloudwatchlogs.PutLogEventsInput{
		LogGroupName: sdkaws.String(group), LogStreamName: sdkaws.String("app/1"),
		LogEvents: []types.InputLogEvent{{Timestamp: sdkaws.Int64(time.Now().UnixMilli()), Message: sdkaws.String("tail event arrived")}},
	}); err != nil {
		t.Fatalf("putting log events: %v", err)
	}
	h.waitFor("tail event arrived")
	h.keys("F")
	h.waitFor("Live tail stopped.")
}

// createLogEvents makes group with one stream of n events in the last 20
// minutes. Every tenth event has ERROR.
func createLogEvents(t *testing.T, group, stream string, n int) {
	t.Helper()
	createLogStreams(t, group, 0)
	logs := services(t).Client.LogsClient()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := logs.CreateLogStream(ctx, &cloudwatchlogs.CreateLogStreamInput{
		LogGroupName: sdkaws.String(group), LogStreamName: sdkaws.String(stream),
	}); err != nil {
		t.Fatalf("creating log stream: %v", err)
	}
	start := time.Now().Add(-20 * time.Minute).UnixMilli()
	events := make([]types.InputLogEvent, n)
	for i := range events {
		text := fmt.Sprintf("event %04d ok", i)
		if i%10 == 0 {
			text = fmt.Sprintf("event %04d ERROR failed", i)
		}
		events[i] = types.InputLogEvent{Timestamp: sdkaws.Int64(start + int64(i)*400), Message: sdkaws.String(text)}
	}
	if _, err := logs.PutLogEvents(ctx, &cloudwatchlogs.PutLogEventsInput{
		LogGroupName: sdkaws.String(group), LogStreamName: sdkaws.String(stream), LogEvents: events,
	}); err != nil {
		t.Fatalf("putting log events: %v", err)
	}
}

// createLogStreams makes group with n streams, and deletes the group at
// the end of the test.
func createLogStreams(t *testing.T, group string, n int) {
	t.Helper()
	logs := services(t).Client.LogsClient()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	_, err := logs.CreateLogGroup(ctx, &cloudwatchlogs.CreateLogGroupInput{LogGroupName: sdkaws.String(group)})
	if err != nil {
		t.Fatalf("creating log group %s: %v", group, err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := logs.DeleteLogGroup(ctx, &cloudwatchlogs.DeleteLogGroupInput{LogGroupName: sdkaws.String(group)}); err != nil {
			t.Logf("deleting log group %s: %v", group, err)
		}
	})
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(16)
	for i := range n {
		g.Go(func() error {
			name := fmt.Sprintf("stream-%03d", i)
			_, err := logs.CreateLogStream(gctx, &cloudwatchlogs.CreateLogStreamInput{
				LogGroupName: sdkaws.String(group), LogStreamName: sdkaws.String(name),
			})
			if err != nil {
				return fmt.Errorf("creating log stream %s: %w", name, err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
}

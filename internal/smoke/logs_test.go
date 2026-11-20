//go:build smoke

package smoke

import (
	"context"
	"fmt"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
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

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
)

// :lambda NAME opens the detail of a seeded function with hidden values,
// $ shows the values, and enter opens the log group of the function
// (docs/design/lambda.md, PR 2).
func TestLambdaFunctionDetailAndLogs(t *testing.T) {
	requestID := putReportLine(t, "/aws/lambda/orders-api")

	h := start(t, "")
	h.keys(":lambda orders-api")
	h.enter()
	h.waitFor("Last update", "Active")
	h.keys("2")
	h.waitFor("Values are hidden", "DB_PASSWORD")
	h.waitGone("not-a-real-secret")

	h.esc() // close the panel
	h.waitGone("Last update")
	h.keys("$")
	h.waitFor("not-a-real-secret")

	h.esc()
	h.waitGone("not-a-real-secret")
	h.enter()
	h.waitFor("/aws/lambda/orders-api events", "events ·")
	// LocalStack returns the events of a group one stream after the other,
	// not merged by time, so the newest page can hold only the busy seeded
	// stream. A filter pattern finds the invocation line.
	h.keys("f")
	h.keys(requestID)
	h.enter()
	h.waitFor("REPORT RequestId: " + requestID)
}

// putReportLine writes a REPORT line of now into a new stream of group,
// and deletes the stream at the end of the test. The seeded lines can be
// older than the last hour that the event viewer shows.
func putReportLine(t *testing.T, group string) string {
	t.Helper()
	logs := services(t).Client.LogsClient()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	stream := uniqueName("report")
	if _, err := logs.CreateLogStream(ctx, &cloudwatchlogs.CreateLogStreamInput{
		LogGroupName: sdkaws.String(group), LogStreamName: sdkaws.String(stream),
	}); err != nil {
		t.Fatalf("creating log stream: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if _, err := logs.DeleteLogStream(ctx, &cloudwatchlogs.DeleteLogStreamInput{
			LogGroupName: sdkaws.String(group), LogStreamName: sdkaws.String(stream),
		}); err != nil {
			t.Logf("deleting log stream %s: %v", stream, err)
		}
	})
	id := fmt.Sprintf("smoke%d", time.Now().UnixNano())
	line := fmt.Sprintf("REPORT RequestId: %s\tDuration: 12.50 ms\tBilled Duration: 13 ms\tMemory Size: 256 MB\tMax Memory Used: 70 MB\t\n", id)
	if _, err := logs.PutLogEvents(ctx, &cloudwatchlogs.PutLogEventsInput{
		LogGroupName: sdkaws.String(group), LogStreamName: sdkaws.String(stream),
		LogEvents: []types.InputLogEvent{{Timestamp: sdkaws.Int64(time.Now().UnixMilli()), Message: sdkaws.String(line)}},
	}); err != nil {
		t.Fatalf("putting the REPORT line: %v", err)
	}
	return id
}

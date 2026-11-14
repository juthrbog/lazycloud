//go:build smoke

package smoke

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"golang.org/x/sync/errgroup"
)

// TestSQSSendPurgeDelete sends a message to a queue, purges the queue, and
// deletes it, all through the Manage menu.
func TestSQSSendPurgeDelete(t *testing.T) {
	name := uniqueName("sqs")
	createQueues(t, name)

	h := start(t, "")
	h.open("sqs", "SQS Queues")
	h.filter(name)
	h.waitFor(name)

	h.keys("m")
	h.pick("Send Message")
	h.waitFor("Message Body")
	h.keys("hello from the smoke test")
	h.submitForm()
	h.waitFor("Message sent")
	// A send does not refresh the row, so the test reloads the list.
	h.keys("r")
	h.waitLoaded()
	h.until("1 message in the queue", queueRow(name, 1).MatchString)

	h.keys("m")
	h.pick("Purge Queue")
	h.confirm(name)
	h.waitFor("Purged 1 queue(s)")
	h.until("an empty queue", queueRow(name, 0).MatchString)

	h.keys("m")
	h.pick("Delete Queue")
	h.confirm(name)
	h.waitFor("Deleted 1 queue(s)")
	h.waitGone("│ " + name)
}

// TestSQSPaging loads more queues than one page of ListQueues holds (100).
func TestSQSPaging(t *testing.T) {
	const count = 120
	prefix := uniqueName("page")
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("%s-%03d", prefix, i)
	}
	createQueues(t, names...)

	h := start(t, "")
	h.open("sqs", "SQS Queues")
	h.filter(prefix)
	h.until(fmt.Sprintf("%d queues", count), regexp.MustCompile(fmt.Sprintf(`\b%d/\d+ queues`, count)).MatchString)
}

// queueRow matches the row of queue name with n visible messages.
func queueRow(name string, n int) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(name) + `\s+Standard\s+` + fmt.Sprint(n) + `\s`)
}

// createQueues creates standard queues and deletes the remaining ones when
// the test ends.
func createQueues(t *testing.T, names ...string) {
	t.Helper()
	sqsc := services(t).Client.SQSClient()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	urls := make([]string, len(names))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(16)
	for i, n := range names {
		g.Go(func() error {
			out, err := sqsc.CreateQueue(gctx, &sqs.CreateQueueInput{QueueName: sdkaws.String(n)})
			if err != nil {
				return fmt.Errorf("creating queue %s: %w", n, err)
			}
			urls[i] = sdkaws.ToString(out.QueueUrl)
			return nil
		})
	}
	err := g.Wait()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(16)
		for _, u := range urls {
			if u == "" {
				continue
			}
			// The test can delete the queue itself. An error for a
			// missing queue is fine.
			g.Go(func() error {
				_, _ = sqsc.DeleteQueue(gctx, &sqs.DeleteQueueInput{QueueUrl: sdkaws.String(u)})
				return nil
			})
		}
		_ = g.Wait()
	})
	if err != nil {
		t.Fatal(err)
	}
}

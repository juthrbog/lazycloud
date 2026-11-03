package views

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Each result message that can outlive its view says what happened, so
// the app can show it after the user left the view (msg.Outcome).
func TestOutcomes(t *testing.T) {
	cases := []struct {
		name  string
		in    msg.Outcome
		level int
		text  string
	}{
		{"copy done", s3CopyCompleteMsg{bucket: "b", dstKey: "k2"}, 1, "Copied → k2"},
		{"copy not replaced", s3CopyCompleteMsg{bucket: "b", dstKey: "k2", err: aws.ErrDestinationExists}, 2, "Not copied: s3://b/k2 exists"},
		{"move failed", s3MoveCompleteMsg{bucket: "b", dstKey: "k2", err: errors.New("AccessDenied")}, 2, "Not moved to s3://b/k2: AccessDenied"},
		{"download failed", s3DownloadCompleteMsg{bucket: "b", key: "k", err: errors.New("AccessDenied")}, 2, "Download of s3://b/k failed: AccessDenied"},
		{"download cancelled", s3DownloadCompleteMsg{bucket: "b", key: "k", err: fmt.Errorf("writing file: %w", context.Canceled)}, 0, "Download of s3://b/k cancelled"},
		{"copy cancelled", s3CopyCompleteMsg{bucket: "b", dstKey: "k2", err: context.Canceled}, 0, "Not copied to s3://b/k2: cancelled"},
		{"delete cancelled while listing", s3DeleteCompleteMsg{bucket: "b", prefix: "p/", err: fmt.Errorf("listing objects in p/: %w", context.Canceled)}, 0, "Delete in s3://b/p/ cancelled. No object was deleted."},
		{"delete cancelled", s3DeleteCompleteMsg{bucket: "b", prefix: "p/", count: 5, err: &aws.DeleteObjectsError{
			Failed: []aws.KeyFailure{{Key: "p/d", Code: "Canceled"}, {Key: "p/e", Code: "Canceled"}},
			Err:    context.Canceled,
		}}, 0, "Delete cancelled: deleted 3 of 5 object(s)"},
		{"redrive stopped", sqsRedriveDoneMsg{queue: "dlq", canceled: true}, 0, "Redrive of dlq stopped. The moved messages stay in the source queue."},
		{"redrive not stopped", sqsRedriveDoneMsg{queue: "dlq", canceled: true, err: errors.New("AccessDenied")}, 2, "Could not stop the redrive of dlq (AccessDenied). SQS goes on with it."},
		{"delete none", s3DeleteCompleteMsg{bucket: "b"}, 2, "No objects found to delete"},
		{"delete partial", s3DeleteCompleteMsg{bucket: "b", prefix: "p/", count: 2, err: &aws.DeleteObjectsError{
			Failed: []aws.KeyFailure{{Key: "p/x", Code: "AccessDenied"}},
		}}, 2, "Deleted 1 of 2 object(s). Failed: x (AccessDenied)"},
		{"buckets", s3BucketDeletedMsg{done: []string{"a"}}, 1, "Deleted 1 bucket(s)"},
		{"buckets cancelled", s3BucketDeletedMsg{done: []string{"a"}, canceled: 2}, 0, "Bucket delete cancelled: deleted 1 of 3 bucket(s)"},
		{"buckets cancelled after a failure", s3BucketDeletedMsg{
			done: []string{"a"}, failed: []itemFailure{{name: "b", err: errors.New("AccessDenied")}}, canceled: 1,
		}, 2, "Bucket delete cancelled: deleted 1 of 3 bucket(s). Failed: b (AccessDenied)"},
		{"delete cancelled after a failure", s3DeleteCompleteMsg{bucket: "b", prefix: "p/", count: 5, err: &aws.DeleteObjectsError{
			Failed: []aws.KeyFailure{{Key: "p/x", Code: "AccessDenied"}, {Key: "p/y", Code: "Canceled"}},
			Err:    context.Canceled,
		}}, 2, "Delete cancelled: deleted 3 of 5 object(s). Failed: x (AccessDenied)"},
		{"bucket create failed", s3BucketCreatedMsg{name: "n", err: errors.New("BucketAlreadyExists")}, 2, "Bucket n not created: BucketAlreadyExists"},
		{"ec2", ec2InstanceMutatedMsg{action: "stopped", instanceID: "i-1"}, 1, "Instance stopped: i-1"},
		{"send", sqsMessageSentMsg{}, 1, "Message sent"},
		{"sqs delete standard", sqsMessageDeletedMsg{ids: []string{"m1"}}, 1, "SQS accepted the delete of 1 message(s)"},
		{"sqs delete fifo", sqsMessageDeletedMsg{ids: []string{"m1"}, isFIFO: true}, 1, "Deleted 1 message(s)"},
		{"redrive done", sqsRedriveDoneMsg{status: "COMPLETED", moved: 3}, 1, "Redrive completed: 3 messages moved"},
		{"redrive failed", sqsRedriveDoneMsg{status: "FAILED", reason: "AccessDenied"}, 2, "Redrive failed: AccessDenied"},
		{"redrive lost", sqsRedriveDoneMsg{queue: "dlq", err: errors.New("Throttling")}, 2, "Lost track of the redrive of dlq (Throttling). SQS goes on with it."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.in.Outcome()
			assert.Equal(t, c.level, got.Level)
			assert.Equal(t, c.text, got.Text)
		})
	}
}

func TestRedriveEnd(t *testing.T) {
	_, done := redriveEnd("dlq", []aws.MessageMoveTask{{Status: "RUNNING"}})
	assert.False(t, done, "a running task goes on")

	_, done = redriveEnd("dlq", []aws.MessageMoveTask{{Status: "CANCELLING"}, {Status: "COMPLETED", ApproximateNumberOfMsgsMoved: 5}})
	assert.False(t, done, "only the newest task decides; CANCELLING still runs")

	end, done := redriveEnd("dlq", []aws.MessageMoveTask{{Status: "FAILED", FailureReason: "AccessDenied"}, {Status: "COMPLETED"}})
	require.True(t, done)
	assert.Equal(t, "FAILED", end.status)
	assert.Equal(t, "AccessDenied", end.reason)

	end, done = redriveEnd("dlq", nil)
	require.True(t, done, "no task: the watch ends")
	assert.Empty(t, end.status)
}

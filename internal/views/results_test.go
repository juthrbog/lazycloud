package views

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// These tests cover the SQS findings M5, M7, M9, M22, and L25 of
// docs/reviews/2026-10-11, and the S3 and EC2 parts of M7.

var errDenied = &smithy.GenericAPIError{Code: "AccessDenied", Message: "not allowed"}

func TestFailureTextNamesThreeAndCountsTheRest(t *testing.T) {
	failures := []itemFailure{
		{name: "a", err: errDenied}, {name: "b", err: errors.New("timeout")},
		{name: "c", err: errDenied}, {name: "d", err: errDenied}, {name: "e", err: errDenied},
	}
	assert.Equal(t, "a (AccessDenied), b (timeout), c (AccessDenied) and 2 more", failureText(failures))

	text, ok := resultText("Deleted", 7, "queue(s)", failures)
	assert.False(t, ok)
	assert.Equal(t, "Deleted 2 of 7 queue(s). Failed: a (AccessDenied), b (timeout), c (AccessDenied) and 2 more", text)

	text, ok = resultText("Deleted", 2, "queue(s)", nil)
	assert.True(t, ok)
	assert.Equal(t, "Deleted 2 queue(s)", text)
}

func threeQueues(v *SQSQueues) {
	v.Update(sqsAllAttrsLoadedMsg{queues: []aws.Queue{
		{Name: "qa", URL: "ua", ApproximateMessageCount: 8},
		{Name: "qb", URL: "ub", ApproximateMessageCount: 10},
		{Name: "qc", URL: "uc", ApproximateMessageCount: 3},
	}})
}

// M7: a delete of several queues went on with no queue after the first error,
// and only the first queue left the list.
func TestSQSQueues_DeleteReportsEachQueue(t *testing.T) {
	v, svc := newQueueList()
	threeQueues(v)
	svc.On("DeleteQueue", mock.Anything, "ua").Return(nil)
	svc.On("DeleteQueue", mock.Anything, "ub").Return(errDenied)
	svc.On("DeleteQueue", mock.Anything, "uc").Return(nil)

	_, cmd := v.Update(collectMsgs(v.deleteQueues([]string{"ua", "ub", "uc"}))[0])
	assert.Equal(t, []string{"ub"}, v.table.VisibleKeys(), "only the failed queue stays")
	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Equal(t, 2, got[0].Level)
	assert.Equal(t, "Deleted 2 of 3 queue(s). Failed: qb (AccessDenied)", got[0].Text)
	svc.AssertNumberOfCalls(t, "DeleteQueue", 3)
}

func TestSQSQueues_PurgeRefreshesOnlyPurgedQueues(t *testing.T) {
	v, svc := newQueueList()
	threeQueues(v)
	svc.On("PurgeQueue", mock.Anything, "ua").Return(nil)
	svc.On("PurgeQueue", mock.Anything, "ub").Return(errDenied)
	svc.On("GetQueueAttributes", mock.Anything, "ua").Return(&aws.Queue{Name: "qa", URL: "ua"}, nil)

	_, cmd := v.Update(collectMsgs(v.purgeQueues([]string{"ua", "ub"}))[0])
	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Equal(t, "Purged 1 of 2 queue(s). Failed: qb (AccessDenied)", got[0].Text)
	svc.AssertCalled(t, "GetQueueAttributes", mock.Anything, "ua")
	svc.AssertNotCalled(t, "GetQueueAttributes", mock.Anything, "ub")
}

// M9: Send Message used the cursor queue while Purge and Delete used the
// selection.
func TestSQSQueues_SendTargetsTheSelectedQueue(t *testing.T) {
	v, svc := newQueueList()
	threeQueues(v)
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	v.Update(keyPress(' ')) // select qb
	v.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, "ua", v.table.CursorKey(), "the cursor is on another queue")

	_, cmd := v.Update(keyPress('m'))
	picker, ok := collectMsgs(cmd)[0].(msg.RequestActionPickerMsg)
	require.True(t, ok)
	assert.Contains(t, picker.Options, "Send Message")

	v.Update(ui.PickerResultMsg{ID: "action", Selected: 0, Value: "Send Message"})
	svc.On("SendMessage", mock.Anything, "ub", "hi", int32(0), "", "").Return(nil)
	_, cmd = v.Update(msg.FormResultMsg{ID: "sqs_send_message", Values: map[string]string{"body": "hi"}})
	collectMsgs(cmd)
	svc.AssertCalled(t, "SendMessage", mock.Anything, "ub", "hi", int32(0), "", "")
}

func TestSQSQueues_ManyQueuesOfferNoSend(t *testing.T) {
	v, _ := newQueueList()
	threeQueues(v)
	v.Update(keyPress(' '))
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	v.Update(keyPress(' '))

	_, cmd := v.Update(keyPress('m'))
	picker, ok := collectMsgs(cmd)[0].(msg.RequestActionPickerMsg)
	require.True(t, ok)
	assert.Equal(t, "Manage 2 Queues", picker.Title)
	assert.Equal(t, []string{"Purge Queue", "Delete Queue"}, picker.Options)
}

// M22: a queue that cannot be read showed 0 messages.
func TestSQSQueues_UnreadableQueueShowsUnknownCounts(t *testing.T) {
	v, _ := newQueueList()
	_, cmd := v.Update(sqsAllAttrsLoadedMsg{queues: []aws.Queue{
		{Name: "qa", URL: "ua", Type: "Standard", ApproximateMessageCount: 4},
		{Name: "qb", URL: "ub", Err: errDenied},
	}})
	require.True(t, v.table.SetCursorByKey("ub"))
	row := v.table.SelectedRow()
	assert.Equal(t, "unknown", row[1])
	assert.Equal(t, "?", row[2], "the count is unknown, not 0")

	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Equal(t, "Could not read 1 queue(s): qb (AccessDenied). Their counts show ?.", got[0].Text)
}

// L25: counts sorted as text put 10 before 3 and 8.
func TestSQSQueues_CountsSortAsNumbers(t *testing.T) {
	v, _ := newQueueList()
	threeQueues(v)
	names, _ := v.table.SortColumnNames()
	v.table.Sort(indexOf(names, "Messages"))
	assert.Equal(t, []string{"uc", "ua", "ub"}, v.table.VisibleKeys())
}

func indexOf(names []string, want string) int {
	for i, n := range names {
		if n == want {
			return i
		}
	}
	return -1
}

// M22: most DLQs have no RedriveAllowPolicy. A queue is a DLQ when another
// queue sends its failed messages to it.
func TestSQSMessages_DLQComesFromSourceQueues(t *testing.T) {
	v, svc := newTestSQSMessages()
	svc.On("GetQueueAttributes", mock.Anything, testQueueURL).Return(&aws.Queue{URL: testQueueURL, ARN: "arn"}, nil)
	svc.On("ListDeadLetterSourceQueues", mock.Anything, testQueueURL).Return([]string{"https://sqs.example/123/src"}, nil)
	v.Update(collectMsgs(v.fetchQueueAttrs())[0])
	assert.True(t, v.isDLQ)

	_, cmd := v.Update(keyPress('m'))
	_, ok := collectMsgs(cmd)[0].(msg.RequestActionPickerMsg)
	assert.True(t, ok, "a DLQ offers the redrive")
}

func TestSQSMessages_AllowPolicyAloneIsNoDLQ(t *testing.T) {
	v, _ := newTestSQSMessages()
	v.Update(sqsQueueAttrsMsg{queue: &aws.Queue{
		URL: testQueueURL, RedriveAllowPolicy: &aws.RedriveAllowPolicy{RedrivePermission: "allowAll"},
	}})
	_, cmd := v.Update(keyPress('m'))
	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Text, "No queue sends its failed messages to this queue")
}

func TestSQSMessages_UnknownSourcesSayWhy(t *testing.T) {
	v, _ := newTestSQSMessages()
	v.Update(sqsQueueAttrsMsg{queue: &aws.Queue{URL: testQueueURL}, sourcesErr: errDenied})
	_, cmd := v.Update(keyPress('m'))
	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Equal(t, "Cannot tell whether this queue is a DLQ: AccessDenied", got[0].Text)
}

func TestSQSMessages_NoSourceListWhenSettingsFail(t *testing.T) {
	v, svc := newTestSQSMessages()
	svc.On("GetQueueAttributes", mock.Anything, testQueueURL).Return(nil, errDenied)
	got := collectMsgs(v.fetchQueueAttrs())[0].(sqsQueueAttrsMsg)
	assert.Error(t, got.err)
	svc.AssertNotCalled(t, "ListDeadLetterSourceQueues", mock.Anything, mock.Anything)
}

func loadThreeMessages(v *SQSMessages) {
	v.Update(sqsMessagesLoadedMsg{queueURL: testQueueURL, messages: []aws.SQSMessage{
		{MessageID: "m1", ReceiptHandle: "h1", Body: "one"},
		{MessageID: "m2", ReceiptHandle: "h2", Body: "two"},
		{MessageID: "m3", ReceiptHandle: "h3", Body: "three"},
	}})
	v.table.SelectAll()
}

// M5: the rows of a delete stay until SQS answers, and only the deleted
// messages leave the list. A failed one stays, with its error code.
func TestSQSMessages_DeleteKeepsFailedMessages(t *testing.T) {
	v, svc := newTestSQSMessages()
	loadThreeMessages(v)
	svc.On("DeleteMessageBatch", mock.Anything, testQueueURL, []string{"h1", "h2", "h3"}).
		Return(&aws.BatchDeleteError{Failed: []aws.DeleteFailure{{ReceiptHandle: "h2", Code: "ReceiptHandleIsInvalid"}}})

	cmd := v.deleteSelectedMessages()
	assert.Len(t, v.table.VisibleKeys(), 3, "the rows stay until the result arrives")
	_, cmd = v.Update(collectMsgs(cmd)[0])
	assert.Equal(t, []string{"m2"}, v.table.VisibleKeys())
	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Equal(t, "SQS accepted the delete of 2 of 3 message(s). Failed: m2 (ReceiptHandleIsInvalid)", got[0].Text)
}

// On a FIFO queue, SQS rejects an old receipt handle, so a success means
// that the messages are deleted.
func TestSQSMessages_FIFODeleteSaysDeleted(t *testing.T) {
	svc := new(awstest.MockSQSService)
	v := NewSQSMessages(svc, testQueueURL, "orders.fifo", true)
	v.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	loadThreeMessages(v)
	svc.On("DeleteMessageBatch", mock.Anything, testQueueURL, mock.Anything).Return(nil)

	_, cmd := v.Update(collectMsgs(v.deleteSelectedMessages())[0])
	assert.Empty(t, v.table.VisibleKeys())
	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Equal(t, "Deleted 3 message(s)", got[0].Text)
}

// A call that does not run, for example in ReadOnly mode, deletes nothing.
func TestSQSMessages_FailedCallKeepsAllMessages(t *testing.T) {
	v, svc := newTestSQSMessages()
	loadThreeMessages(v)
	svc.On("DeleteMessageBatch", mock.Anything, testQueueURL, mock.Anything).
		Return(errors.New("DeleteMessageBatch: blocked in ReadOnly mode"))

	_, cmd := v.Update(collectMsgs(v.deleteSelectedMessages())[0])
	assert.Len(t, v.table.VisibleKeys(), 3)
	got := toasts(collectMsgs(cmd))
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Text, "SQS accepted the delete of 0 of 3 message(s)")
}

func TestSQSMessages_DeletePromptExplainsStaleHandles(t *testing.T) {
	v, _ := newTestSQSMessages()
	loadThreeMessages(v)
	_, cmd := v.Update(keyPress('x'))
	confirm := collectMsgs(cmd)[0].(msg.RequestConfirmMsg)
	assert.Contains(t, confirm.Message, staleHandleNote)

	fifo := NewSQSMessages(new(awstest.MockSQSService), testQueueURL, "orders.fifo", true)
	fifo.Update(tea.WindowSizeMsg{Width: 140, Height: 24})
	loadThreeMessages(fifo)
	_, cmd = fifo.Update(keyPress('x'))
	confirm = collectMsgs(cmd)[0].(msg.RequestConfirmMsg)
	assert.NotContains(t, confirm.Message, staleHandleNote, "FIFO queues reject old handles")
}

// M7: a bucket delete kept only the last error and hid the list behind an
// error screen.
func TestS3List_DeleteBucketsReportsEachBucket(t *testing.T) {
	v, svc := newTestS3List()
	loadBuckets(v)
	svc.On("EmptyAndDeleteBucket", mock.Anything, "test-bucket").Return(nil)
	svc.On("EmptyAndDeleteBucket", mock.Anything, "other-bucket").Return(errDenied)
	svc.On("ListBuckets", mock.Anything).Return([]aws.Bucket{{Name: "other-bucket"}}, nil)

	_, cmd := v.Update(collectMsgs(v.deleteBuckets([]string{"test-bucket", "other-bucket"}))[0])
	assert.Nil(t, v.err, "the list stays visible")
	got := collectMsgs(cmd)
	tt := toasts(got)
	require.Len(t, tt, 1)
	assert.Equal(t, "Deleted 1 of 2 bucket(s). Failed: other-bucket (AccessDenied)", tt[0].Text)
	svc.AssertCalled(t, "ListBuckets", mock.Anything)
}

// M7: an EC2 action can partly succeed. After an error, the view refreshes
// the instances instead of keeping the state that it guessed.
func TestEC2List_FailedActionRefreshesInstances(t *testing.T) {
	v, svc := newTestEC2List()
	loadInstances(v, []aws.Instance{testRunningInstance, {ID: "i-2", Name: "web-2", State: "running"}})
	ids := []string{"i-running", "i-2"}
	svc.On("RefreshInstances", mock.Anything, ids).Return([]aws.Instance{
		{ID: "i-running", State: "shutting-down"}, {ID: "i-2", State: "running"},
	}, nil)

	_, cmd := v.Update(ec2InstanceMutatedMsg{action: "terminated", ids: ids, instanceID: "i-running, i-2",
		err: &smithy.GenericAPIError{Code: "OperationNotPermitted"}})
	assert.Nil(t, v.err, "the list stays visible")
	got := collectMsgs(cmd)
	tt := toasts(got)
	require.Len(t, tt, 1)
	assert.Equal(t, "Terminate failed: OperationNotPermitted. The list shows the current state of 2 instance(s).", tt[0].Text)
	svc.AssertCalled(t, "RefreshInstances", mock.Anything, ids)
}

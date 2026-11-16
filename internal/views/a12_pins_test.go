package views

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// These tests pin the prompt behavior of the views before A12
// (docs/design/a12-view-tags.md). The tests named "Hazard" pin a bug of
// today. The PR named in each comment changes it, and that PR must flip
// the test. The other tests pin behavior that A12 must keep.

// ─── H1: a second Manage request no longer clears the first (fixed in PR 3)

// manageTwice presses m twice on v. The app shows the first picker and
// refuses the second request, and its decline goes to v. It returns the
// first picker request.
func manageTwice(t *testing.T, v tea.Model) msg.RequestActionPickerMsg {
	t.Helper()
	_, cmd := v.Update(keyPress('m'))
	first := requestOf[msg.RequestActionPickerMsg](t, cmd)
	_, cmd = v.Update(keyPress('m'))
	second := requestOf[msg.RequestActionPickerMsg](t, cmd)
	_, cmd = v.Update(cancelPick(second))
	assert.Nil(t, cmd, "a decline runs nothing")
	return first
}

// H1, fixed in PR 3. The decline of the second request carries its own
// tag, so the first picker keeps its targets, and a yes stops the instance.
func TestA12_EC2SecondManageKeepsTheTargets(t *testing.T) {
	setReadWrite(t)
	v, svc := newTestEC2List()
	loadInstances(v, []aws.Instance{testRunningInstance})

	first := manageTwice(t, v)
	_, cmd := v.Update(pickLabel(t, first, "Stop"))
	req := requestOf[msg.RequestConfirmMsg](t, cmd)
	assert.Equal(t, "Stop 1 instance(s)?\n  web-1 (i-running)\n", req.Message)

	svc.On("StopInstances", mock.Anything, []string{"i-running"}).Return(nil).Once()
	answerYes(t, v, req)
	svc.AssertExpectations(t)
}

// H1, fixed in PR 3, for SQS queues: a yes purges the queue of the first
// picker.
func TestA12_SQSSecondManageKeepsTheTargets(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)

	first := manageTwice(t, v)
	_, cmd := v.Update(pickLabel(t, first, "Purge Queue"))
	req := requestOf[msg.RequestConfirmMsg](t, cmd)
	assert.Contains(t, req.Message, "Purge 1 queue(s)?\n  qa\n")

	svc.On("PurgeQueue", mock.Anything, charSQSURL("qa")).Return(nil).Once()
	answerYes(t, v, req)
	svc.AssertExpectations(t)
}

// H1, fixed in PR 3, for the send form of SQS queues: the pick opens the
// form for the queue.
func TestA12_SQSSecondManageKeepsSend(t *testing.T) {
	charSQSReadWrite(t)
	v, _ := charSQSThree(t)

	first := manageTwice(t, v)
	_, cmd := v.Update(pickLabel(t, first, "Send Message"))
	form := requestOf[msg.RequestFormMsg](t, cmd)
	assert.Equal(t, "sqs_send_message", form.ID)
	assert.Equal(t, charSQSURL("qa"), v.sendTarget)
}

// ─── H2: the delete of SQS messages acts on what the prompt counted (fixed in PR 5)

// H2, fixed in PR 5. The selection changes while the confirm is open. A
// yes still deletes only m1, the message that the prompt counted (D4).
func TestA12_SQSMessageDeleteUsesTheCountedMessages(t *testing.T) {
	v, svc := newTestSQSMessages()
	v.Update(sqsMessagesLoadedMsg{queueURL: testQueueURL, messages: []aws.SQSMessage{
		{MessageID: "m1", ReceiptHandle: "h1", Body: "one"},
		{MessageID: "m2", ReceiptHandle: "h2", Body: "two"},
	}})
	_, cmd := v.Update(keyPress('x'))
	req := requestOf[msg.RequestConfirmMsg](t, cmd)
	require.Contains(t, req.Message, "Delete 1 message(s)?")

	v.table.SelectAll()
	svc.On("DeleteMessageBatch", mock.Anything, testQueueURL, []string{"h1"}).Return(nil).Once()
	answerYes(t, v, req)
	svc.AssertExpectations(t)
}

// D4: while the confirm is open, a new peek gives m1 a new receipt handle,
// and the result of an earlier delete removes m2. A yes deletes m1 with
// its newest handle and skips m2.
func TestA12_SQSMessageDeleteUsesTheNewestHandles(t *testing.T) {
	v, svc := newTestSQSMessages()
	loadThreeMessages(v) // m1 h1, m2 h2, m3 h3, all selected
	_, cmd := v.Update(keyPress('x'))
	req := requestOf[msg.RequestConfirmMsg](t, cmd)
	require.Contains(t, req.Message, "Delete 3 message(s)?")

	v.Update(sqsMessagesLoadedMsg{queueURL: testQueueURL, messages: []aws.SQSMessage{
		{MessageID: "m1", ReceiptHandle: "h1-new", Body: "one"},
	}})
	v.Update(sqsMessageDeletedMsg{ids: []string{"m2"}, failed: map[string]string{}})
	require.Nil(t, v.findMessage("m2"))
	svc.On("DeleteMessageBatch", mock.Anything, testQueueURL, []string{"h1-new", "h3"}).Return(nil).Once()
	answerYes(t, v, req)
	svc.AssertExpectations(t)
}

// ─── H3: a decline no longer keeps the overwrite question open (fixed in PR 4)

// H3, fixed in PR 4. S3 objects keeps no question state, so after a
// decline (also the bare decline that closePrompts sent before PR 1) the
// next conflict is asked. While a prompt is open, the app refuses the new
// question with a toast (D3).
func TestA12_S3NextConflictIsAskedAfterADecline(t *testing.T) {
	v, _ := newS3TransferView(t)
	_, cmd := v.Update(s3CopyCompleteMsg{bucket: "test-bucket", srcKey: "a.txt", dstKey: "b.txt", err: aws.ErrDestinationExists})
	requestOf[msg.RequestConfirmMsg](t, cmd)

	_, cmd = v.Update(msg.ConfirmResultMsg{Confirmed: false})
	assert.Nil(t, cmd)

	_, cmd = v.Update(s3CopyCompleteMsg{bucket: "test-bucket", srcKey: "a.txt", dstKey: "c.txt", err: aws.ErrDestinationExists})
	msgs := collectMsgs(cmd)
	require.Len(t, msgs, 1, "only the question, no toast of the view")
	req, ok := msgs[0].(msg.RequestConfirmMsg)
	require.True(t, ok)
	assert.Equal(t, &s3OverwriteTag{mode: "copy", srcKey: "a.txt", dstKey: "c.txt"}, req.Tag)
}

// ─── H6: a sort pick names its column by title (fixed in PR 2) ─────────────

// H6, fixed in PR 2. The columns of S3 versions change while the sort
// picker is open. A pick of a column that moved sorts by that column at its
// new index. A pick of a column that is gone keeps the current sort (D5).
func TestA12_S3VersionsSortPickAfterAResizeUsesTheTitle(t *testing.T) {
	for _, c := range []struct {
		name, pick, want string
		sortFirst        string // a column to sort by before the picker opens
	}{
		{name: "moved column", pick: "Latest", want: "Latest"},
		{name: "gone column, no sort", pick: "Modified", want: ""},
		{name: "gone column keeps the sort", pick: "Modified", want: "Size", sortFirst: "Size"},
	} {
		t.Run(c.name, func(t *testing.T) {
			v := newTestS3Versions()
			v.Update(tea.WindowSizeMsg{Width: 160, Height: 24})
			v.Update(s3VersionsLoadedMsg{versions: []aws.ObjectVersion{
				{Key: "file.txt", VersionID: "v1", Size: 300},
				{Key: "file.txt", VersionID: "v2", Size: 100, IsLatest: true},
				{Key: "file.txt", VersionID: "v3", Size: 200},
			}})
			if c.sortFirst != "" {
				v.Update(pickColumn(t, sortRequest(t, v), c.sortFirst))
			}
			req := sortRequest(t, v)
			require.Equal(t, []string{"Version ID", "Size", "Modified", "Latest", "Delete Marker"}, req.Columns)

			v.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			cols, _ := v.table.SortColumnNames()
			require.Equal(t, []string{"Version ID", "Size", "Latest"}, cols)
			before := v.table.VisibleKeys()

			v.Update(pickColumn(t, req, c.pick))
			if c.want == "" || c.sortFirst != "" {
				assert.Equal(t, before, v.table.VisibleKeys(), "the rows keep their order")
			}
			cols, sorted := v.table.SortColumnNames()
			got := ""
			if sorted >= 0 {
				got = cols[sorted]
			}
			assert.Equal(t, c.want, got)
		})
	}
}

// ─── Behavior that A12 keeps ──────────────────────────────────────────────

// Each reply to an EC2 prompt that is not a yes runs nothing. After it, a
// new prompt for the same instance works. Each test answers a prompt once,
// because each prompt gets exactly one reply (I10).
func TestA12Keep_EC2DeclinesRunNothing(t *testing.T) {
	setReadWrite(t)
	for name, reply := range map[string]func(msg.RequestConfirmMsg) tea.Msg{
		"no":      func(r msg.RequestConfirmMsg) tea.Msg { return confirmReply(r, false) },
		"bare no": func(msg.RequestConfirmMsg) tea.Msg { return msg.ConfirmResultMsg{} },
	} {
		t.Run(name, func(t *testing.T) {
			v, svc := newTestEC2List()
			loadInstances(v, []aws.Instance{testRunningInstance})
			_, cmd := v.Update(keyPress('m'))
			picker := requestOf[msg.RequestActionPickerMsg](t, cmd)
			_, cmd = v.Update(pickLabel(t, picker, "Stop"))
			req := requestOf[msg.RequestConfirmMsg](t, cmd)

			_, cmd = v.Update(reply(req))
			assert.Nil(t, cmd)
			svc.AssertNotCalled(t, "StopInstances", mock.Anything, mock.Anything)

			_, cmd = v.Update(keyPress('m'))
			picker = requestOf[msg.RequestActionPickerMsg](t, cmd)
			_, cmd = v.Update(pickLabel(t, picker, "Stop"))
			req = requestOf[msg.RequestConfirmMsg](t, cmd)
			svc.On("StopInstances", mock.Anything, []string{"i-running"}).Return(nil).Once()
			answerYes(t, v, req)
			svc.AssertExpectations(t)
		})
	}
}

// A yes runs the picked action on the targets of the key press, also when
// the cursor moves while the prompt is open.
func TestA12Keep_EC2TargetsAreFrozenAtTheKeyPress(t *testing.T) {
	setReadWrite(t)
	v, svc := newTestEC2List()
	loadInstances(v, []aws.Instance{testRunningInstance, {ID: "i-other", Name: "web-2", State: "running"}})
	_, cmd := v.Update(keyPress('m'))
	picker := requestOf[msg.RequestActionPickerMsg](t, cmd)
	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd = v.Update(pickLabel(t, picker, "Stop"))
	req := requestOf[msg.RequestConfirmMsg](t, cmd)
	assert.Contains(t, req.Message, "web-1 (i-running)")

	svc.On("StopInstances", mock.Anything, []string{"i-running"}).Return(nil).Once()
	answerYes(t, v, req)
	svc.AssertExpectations(t)
}

// A reply without the tag of the prompt runs nothing. It does not run the
// EC2 action.
func TestA12Keep_EC2ForeignRepliesRunNothing(t *testing.T) {
	setReadWrite(t)
	v, svc := newTestEC2List()
	loadInstances(v, []aws.Instance{testRunningInstance})
	_, cmd := v.Update(keyPress('m'))
	picker := requestOf[msg.RequestActionPickerMsg](t, cmd)
	_, cmd = v.Update(pickLabel(t, picker, "Stop"))
	requestOf[msg.RequestConfirmMsg](t, cmd)

	_, cmd = v.Update(msg.ConfirmResultMsg{Confirmed: true})
	assert.Nil(t, cmd, "an untagged yes runs nothing")
	svc.AssertNotCalled(t, "StopInstances", mock.Anything, mock.Anything)
}

// The sort pick of a list view sorts by the picked column, a second pick of
// the same column flips the direction, esc keeps the sort, and ctrl+x
// clears it.
func TestA12Keep_ListSortPickCancelAndClear(t *testing.T) {
	v, _ := newTestEC2List()
	loadInstances(v, []aws.Instance{testRunningInstance, testStoppedInstance})

	req := sortRequest(t, v)
	v.Update(pickColumn(t, req, "Name"))
	cols, sorted := v.table.SortColumnNames()
	require.GreaterOrEqual(t, sorted, 0)
	assert.Equal(t, "Name", cols[sorted])
	assert.Equal(t, []string{"i-stopped", "i-running"}, v.table.VisibleKeys(), "batch-1, web-1")

	req = sortRequest(t, v)
	v.Update(pickColumn(t, req, "Name"))
	assert.Equal(t, []string{"i-running", "i-stopped"}, v.table.VisibleKeys(), "the same column flips")

	req = sortRequest(t, v)
	v.Update(cancelPick(req))
	_, sorted = v.table.SortColumnNames()
	assert.Equal(t, "Name", cols[sorted], "esc keeps the sort")

	req = sortRequest(t, v)
	v.Update(clearSort(req))
	_, sorted = v.table.SortColumnNames()
	assert.Equal(t, -1, sorted, "ctrl+x clears the sort")
}

// The sort pick of S3 versions sorts by the picked column when the columns
// did not change.
func TestA12Keep_S3VersionsSortPick(t *testing.T) {
	v := newTestS3Versions()
	req := sortRequest(t, v)
	v.Update(pickColumn(t, req, "Latest"))
	cols, sorted := v.table.SortColumnNames()
	assert.Equal(t, "Latest", cols[sorted])
}

// The S3 objects delete: a no deletes nothing, and a yes deletes the
// entries of the key press.
func TestA12Keep_S3ObjectsDeleteFlow(t *testing.T) {
	v, svc := newS3TransferView(t)
	v.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // logs/ → a.txt
	_, cmd := v.Update(keyPress('x'))
	req := requestOf[msg.RequestConfirmMsg](t, cmd)
	assert.Contains(t, req.Message, "a.txt")

	_, cmd = v.Update(confirmReply(req, false))
	assert.Nil(t, cmd)
	svc.AssertNotCalled(t, "DeleteObjects", mock.Anything, mock.Anything, mock.Anything)

	_, cmd = v.Update(keyPress('x'))
	req = requestOf[msg.RequestConfirmMsg](t, cmd)
	v.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) // the cursor moves to b.txt
	svc.On("DeleteObjects", mock.Anything, "test-bucket", []string{"a.txt"}).Return(nil).Once()
	answerYes(t, v, req)
	svc.AssertExpectations(t)
}

// The redrive of SQS messages: a no runs nothing, and a cancelled picker
// runs nothing. TestSQSMessages_RedriveAsksFirst covers the yes.
func TestA12Keep_SQSRedriveDeclinesRunNothing(t *testing.T) {
	v, _ := newTestSQSMessages()
	v.Update(sqsQueueAttrsMsg{queue: &aws.Queue{URL: testQueueURL, ARN: "arn:aws:sqs:us-east-1:123:orders"},
		sources: []string{"https://sqs.us-east-1.amazonaws.com/123/src"}})
	_, cmd := v.Update(keyPress('m'))
	picker := requestOf[msg.RequestActionPickerMsg](t, cmd)
	_, cmd = v.Update(pickLabel(t, picker, "Start Redrive"))
	req := requestOf[msg.RequestConfirmMsg](t, cmd)
	assert.True(t, req.Quick)

	_, cmd = v.Update(confirmReply(req, false))
	assert.Nil(t, cmd)
	assert.False(t, v.redriving)

	_, cmd = v.Update(keyPress('m'))
	picker = requestOf[msg.RequestActionPickerMsg](t, cmd)
	_, cmd = v.Update(cancelPick(picker))
	assert.Nil(t, cmd, "a cancelled picker runs nothing")
	assert.False(t, v.redriving)
}

// A no to the delete of SQS messages deletes nothing.
func TestA12Keep_SQSMessageDeleteDeclineRunsNothing(t *testing.T) {
	v, svc := newTestSQSMessages()
	loadThreeMessages(v)
	_, cmd := v.Update(keyPress('x'))
	req := requestOf[msg.RequestConfirmMsg](t, cmd)
	_, cmd = v.Update(confirmReply(req, false))
	assert.Nil(t, cmd)
	svc.AssertNotCalled(t, "DeleteMessageBatch", mock.Anything, mock.Anything, mock.Anything)
}

// The reply helpers answer with the index of the option in the request.
// The views read the index from PR 3 on, so the helpers must get it right.
func TestA12Keep_ReplyHelpersUseTheIndexOfTheRequest(t *testing.T) {
	picker := msg.RequestActionPickerMsg{Options: []string{"Stop", "Start", "Reboot"}}
	assert.Equal(t, msg.PickerResultMsg{Selected: 2, Value: "Reboot"}, pickLabel(t, picker, "Reboot"))
	sort := msg.RequestSortPickerMsg{Columns: []string{"Name", "Size"}}
	assert.Equal(t, msg.PickerResultMsg{Selected: 1, Value: "Size"}, pickColumn(t, sort, "Size"))
	assert.Equal(t, msg.PickerResultMsg{Outcome: msg.PickCancelled}, cancelPick(sort))
	assert.Equal(t, msg.PickerResultMsg{Outcome: msg.PickCancelled}, cancelPick(picker))
	assert.Equal(t, msg.PickerResultMsg{Outcome: msg.PickCleared}, clearSort(sort))
	req := msg.RequestConfirmMsg{Message: "go"}
	assert.Equal(t, msg.ConfirmResultMsg{Confirmed: true}, confirmReply(req, true))
	assert.Equal(t, msg.ConfirmResultMsg{}, confirmReply(req, false))
}

// helperTag is a tag for the tests of the reply helpers.
type helperTag struct{}

func (*helperTag) PromptTag() {}

// Each reply helper copies the tag of the request, as the app does.
func TestA12Keep_ReplyHelpersCopyTheTag(t *testing.T) {
	tag := &helperTag{}
	picker := msg.RequestActionPickerMsg{Options: []string{"Stop"}, Tag: tag}
	sort := msg.RequestSortPickerMsg{Columns: []string{"Name"}, Tag: tag}
	confirm := msg.RequestConfirmMsg{Tag: tag}
	for name, got := range map[string]msg.PromptTag{
		"pickLabel":     pickLabel(t, picker, "Stop").Tag,
		"cancel action": cancelPick(picker).Tag,
		"pickColumn":    pickColumn(t, sort, "Name").Tag,
		"cancel sort":   cancelPick(sort).Tag,
		"clearSort":     clearSort(sort).Tag,
		"yes":           confirmReply(confirm, true).Tag,
		"no":            confirmReply(confirm, false).Tag,
	} {
		assert.Same(t, tag, got, name)
	}
}

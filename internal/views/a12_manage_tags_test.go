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

// Tests of the Manage tags of EC2 and SQS queues (A12 PR 3).

func TestPickedOption(t *testing.T) {
	opts := []string{"a", "b"}
	for name, c := range map[string]struct {
		r    msg.PickerResultMsg
		want string
		ok   bool
	}{
		"pick":      {msg.PickerResultMsg{Selected: 1}, "b", true},
		"first":     {msg.PickerResultMsg{Selected: 0}, "a", true},
		"cancel":    {msg.PickerResultMsg{Outcome: msg.PickCancelled, Selected: 1}, "", false},
		"clear":     {msg.PickerResultMsg{Outcome: msg.PickCleared, Selected: 1}, "", false},
		"negative":  {msg.PickerResultMsg{Selected: -1}, "", false},
		"too large": {msg.PickerResultMsg{Selected: 2}, "", false},
	} {
		got, ok := pickedOption(opts, c.r)
		assert.Equal(t, c.ok, ok, name)
		assert.Equal(t, c.want, got, name)
	}
}

// foreignReplies are replies that must not run a Manage action: no tag, a
// typed-nil tag of the view, and the tag of another prompt (I4, R4).
func foreignReplies[T msg.PromptTag](typedNil T) map[string]msg.PromptTag {
	return map[string]msg.PromptTag{
		"no tag":    nil,
		"typed nil": typedNil,
		"other tag": &helperTag{},
		"sort tag":  &sortTag{columns: []string{"Name"}},
	}
}

func TestEC2ManageIgnoresForeignTags(t *testing.T) {
	setReadWrite(t)
	v, svc := newTestEC2List()
	loadInstances(v, []aws.Instance{testRunningInstance})
	for name, tag := range foreignReplies((*ec2ManageTag)(nil)) {
		var cmd tea.Cmd
		require.NotPanics(t, func() {
			_, cmd = v.Update(msg.PickerResultMsg{Selected: 0, Value: "Stop", Tag: tag})
		}, name)
		_, asked := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
		assert.False(t, asked, "picker reply with %s", name)
	}
	for name, tag := range foreignReplies((*ec2ConfirmTag)(nil)) {
		require.NotPanics(t, func() {
			_, cmd := v.Update(msg.ConfirmResultMsg{Confirmed: true, Tag: tag})
			assert.Nil(t, cmd, "confirm reply with %s", name)
		}, name)
	}
	_, cmd := v.Update(msg.ConfirmResultMsg{Confirmed: true, Tag: &ec2ConfirmTag{op: ec2Stop}})
	assert.Nil(t, cmd, "a tag with no instances runs nothing")
	svc.AssertNotCalled(t, "StopInstances", mock.Anything, mock.Anything)
}

func TestSQSManageIgnoresForeignTags(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	for name, tag := range foreignReplies((*sqsManageTag)(nil)) {
		var cmd tea.Cmd
		require.NotPanics(t, func() {
			_, cmd = v.Update(msg.PickerResultMsg{Selected: 1, Value: "Purge Queue", Tag: tag})
		}, name)
		msgs := collectMsgs(cmd)
		_, asked := firstOfType[msg.RequestConfirmMsg](msgs)
		_, form := firstOfType[msg.RequestFormMsg](msgs)
		assert.False(t, asked || form, "picker reply with %s", name)
	}
	for name, tag := range foreignReplies((*sqsConfirmTag)(nil)) {
		require.NotPanics(t, func() {
			_, cmd := v.Update(msg.ConfirmResultMsg{Confirmed: true, Tag: tag})
			assert.Nil(t, cmd, "confirm reply with %s", name)
		}, name)
	}
	_, cmd := v.Update(msg.ConfirmResultMsg{Confirmed: true, Tag: &sqsConfirmTag{op: sqsPurge}})
	assert.Nil(t, cmd, "a tag with no queues runs nothing")
	svc.AssertNotCalled(t, "PurgeQueue", mock.Anything, mock.Anything)
	svc.AssertNotCalled(t, "DeleteQueue", mock.Anything, mock.Anything)
}

// A yes acts on the targets of its own tag (I3). The test opens a confirm
// for web-1, then a second Manage flow for web-2, and answers the first
// confirm. Only web-1 stops. Before A12, the second m replaced the targets
// that the view held, so the first yes stopped web-2.
func TestEC2ConfirmActsOnItsOwnTag(t *testing.T) {
	setReadWrite(t)
	v, svc := newTestEC2List()
	loadInstances(v, []aws.Instance{testRunningInstance, {ID: "i-other", Name: "web-2", State: "running"}})

	_, cmd := v.Update(keyPress('m'))
	picker := requestOf[msg.RequestActionPickerMsg](t, cmd)
	_, cmd = v.Update(pickLabel(t, picker, "Stop"))
	first := requestOf[msg.RequestConfirmMsg](t, cmd)

	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd = v.Update(keyPress('m'))
	picker = requestOf[msg.RequestActionPickerMsg](t, cmd)
	_, cmd = v.Update(pickLabel(t, picker, "Stop"))
	second := requestOf[msg.RequestConfirmMsg](t, cmd)
	require.Contains(t, second.Message, "web-2 (i-other)")

	svc.On("StopInstances", mock.Anything, []string{"i-running"}).Return(nil).Once()
	answerYes(t, v, first)
	svc.AssertExpectations(t)
}

// The same for SQS queues: a yes purges only the queue of its own tag.
func TestSQSConfirmActsOnItsOwnTag(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	first := charSQSConfirm(t, v, "Purge Queue")
	require.Contains(t, first.Message, "  qa\n")

	v.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	second := charSQSConfirm(t, v, "Purge Queue")
	require.Contains(t, second.Message, "  qb\n")

	svc.On("PurgeQueue", mock.Anything, charSQSURL("qa")).Return(nil).Once()
	answerYes(t, v, first)
	svc.AssertExpectations(t)
}

// A yes runs the action of its tag (I3). The reply carries nothing else
// that names an action.
func TestManageConfirmRunsTheOpOfTheTag(t *testing.T) {
	setReadWrite(t)
	v, svc := newTestEC2List()
	loadInstances(v, []aws.Instance{testRunningInstance})
	svc.On("StopInstances", mock.Anything, []string{"i-running"}).Return(nil).Once()
	_, cmd := v.Update(msg.ConfirmResultMsg{Confirmed: true,
		Tag: &ec2ConfirmTag{op: ec2Stop, ids: []string{"i-running"}}})
	require.NotNil(t, cmd)
	collectMsgs(cmd)
	svc.AssertExpectations(t)
	svc.AssertNotCalled(t, "TerminateInstances", mock.Anything, mock.Anything)

	charSQSReadWrite(t)
	q, qsvc := charSQSThree(t)
	qsvc.On("PurgeQueue", mock.Anything, charSQSURL("qa")).Return(nil).Once()
	_, cmd = q.Update(msg.ConfirmResultMsg{Confirmed: true,
		Tag: &sqsConfirmTag{op: sqsPurge, urls: []string{charSQSURL("qa")}}})
	require.NotNil(t, cmd)
	collectMsgs(cmd)
	qsvc.AssertExpectations(t)
	qsvc.AssertNotCalled(t, "DeleteQueue", mock.Anything, mock.Anything)
}

// The picker tag holds the targets and the actions that the picker offers.
func TestManageTagsHoldTargetsAndActions(t *testing.T) {
	setReadWrite(t)
	v, _ := newTestEC2List()
	loadInstances(v, []aws.Instance{testRunningInstance})
	_, cmd := v.Update(keyPress('m'))
	tag, ok := requestOf[msg.RequestActionPickerMsg](t, cmd).Tag.(*ec2ManageTag)
	require.True(t, ok)
	assert.Equal(t, []string{"i-running"}, tag.ids)
	assert.Equal(t, []ec2Op{ec2Stop, ec2Reboot, ec2Terminate}, tag.ops)

	charSQSReadWrite(t)
	q, _ := charSQSThree(t)
	_, cmd = q.Update(keyPress('m'))
	stag, ok := requestOf[msg.RequestActionPickerMsg](t, cmd).Tag.(*sqsManageTag)
	require.True(t, ok)
	assert.Equal(t, []string{charSQSURL("qa")}, stag.urls)
	assert.Equal(t, []sqsOp{sqsSend, sqsPurge, sqsDelete}, stag.ops)
}

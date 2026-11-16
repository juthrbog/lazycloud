package views

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// Tests of the prompt tags of SQS messages (A12 PR 5).

// dlqMessages returns an SQS messages view of a DLQ with three messages,
// all selected.
func dlqMessages(t *testing.T) (*SQSMessages, *awstest.MockSQSService) {
	t.Helper()
	v, svc := newTestSQSMessages()
	v.Update(sqsQueueAttrsMsg{queue: &aws.Queue{URL: testQueueURL, ARN: "arn:aws:sqs:us-east-1:123:orders"},
		sources: []string{"https://sqs.us-east-1.amazonaws.com/123/src"}})
	loadThreeMessages(v)
	return v, svc
}

// The delete confirm and the DLQ picker carry their tags.
func TestSQSMessagesRequestsAreTagged(t *testing.T) {
	v, _ := dlqMessages(t)
	_, cmd := v.Update(keyPress('x'))
	del := requestOf[msg.RequestConfirmMsg](t, cmd)
	assert.Equal(t, &sqsDeleteMsgsTag{ids: []string{"m1", "m2", "m3"}}, del.Tag)

	_, cmd = v.Update(keyPress('m'))
	picker := requestOf[msg.RequestActionPickerMsg](t, cmd)
	assert.Equal(t, []string{"Start Redrive"}, picker.Options)
	assert.Equal(t, &sqsDLQPickTag{ops: []sqsDLQOp{sqsStartRedrive}}, picker.Tag)

	_, cmd = v.Update(pickLabel(t, picker, "Start Redrive"))
	redrive := requestOf[msg.RequestConfirmMsg](t, cmd)
	assert.IsType(t, &sqsRedriveTag{}, redrive.Tag)
}

// While the delete confirm and the redrive confirm are open, a yes with no
// tag, a typed-nil tag or a foreign tag runs nothing. A pick with no tag, a
// typed-nil tag or a foreign tag asks nothing (I4, R4).
func TestSQSMessagesIgnoresForeignTags(t *testing.T) {
	v, svc := dlqMessages(t)
	_, cmd := v.Update(keyPress('x'))
	requestOf[msg.RequestConfirmMsg](t, cmd) // the delete confirm
	_, cmd = v.Update(keyPress('m'))
	picker := requestOf[msg.RequestActionPickerMsg](t, cmd)
	_, cmd = v.Update(pickLabel(t, picker, "Start Redrive"))
	requestOf[msg.RequestConfirmMsg](t, cmd) // the redrive confirm

	for _, c := range []struct {
		prompt   string
		typedNil msg.PromptTag
	}{
		{"delete", (*sqsDeleteMsgsTag)(nil)},
		{"redrive", (*sqsRedriveTag)(nil)},
	} {
		for name, tag := range foreignReplies(c.typedNil) {
			require.NotPanics(t, func() {
				_, cmd := v.Update(msg.ConfirmResultMsg{Confirmed: true, Tag: tag})
				assert.Nil(t, cmd, "%s %s", c.prompt, name)
			})
		}
	}
	for name, tag := range foreignReplies((*sqsDLQPickTag)(nil)) {
		var cmd tea.Cmd
		require.NotPanics(t, func() {
			_, cmd = v.Update(msg.PickerResultMsg{Selected: 0, Value: "Start Redrive", Tag: tag})
		}, name)
		_, asked := firstOfType[msg.RequestConfirmMsg](collectMsgs(cmd))
		assert.False(t, asked, "pick with %s", name)
	}
	assert.False(t, v.redriving)
	svc.AssertNotCalled(t, "DeleteMessageBatch", mock.Anything, mock.Anything, mock.Anything)
	svc.AssertNotCalled(t, "StartMessageMoveTask", mock.Anything, mock.Anything)
}

// Each yes runs only its own action (I3). With messages selected, a redrive
// yes deletes nothing, and a delete yes starts no redrive.
func TestSQSMessagesYesRunsOnlyItsOwnAction(t *testing.T) {
	setReadOnly(t, false)
	v, svc := dlqMessages(t)
	_, cmd := v.Update(keyPress('x'))
	del := requestOf[msg.RequestConfirmMsg](t, cmd)
	_, cmd = v.Update(keyPress('m'))
	picker := requestOf[msg.RequestActionPickerMsg](t, cmd)
	_, cmd = v.Update(pickLabel(t, picker, "Start Redrive"))
	redrive := requestOf[msg.RequestConfirmMsg](t, cmd)

	svc.On("StartMessageMoveTask", mock.Anything, mock.Anything, mock.Anything).Return("task", nil).Maybe()
	_, cmd = v.Update(confirmReply(redrive, true))
	require.NotNil(t, cmd)
	assert.True(t, v.redriving)
	svc.AssertNotCalled(t, "DeleteMessageBatch", mock.Anything, mock.Anything, mock.Anything)

	v.redriving = false
	svc.On("DeleteMessageBatch", mock.Anything, testQueueURL, []string{"h1", "h2", "h3"}).Return(nil).Once()
	answerYes(t, v, del)
	assert.False(t, v.redriving, "a delete starts no redrive")
	svc.AssertExpectations(t)
}

// A delete tag whose messages are all gone deletes nothing (D4).
func TestSQSMessagesDeleteOfGoneMessagesRunsNothing(t *testing.T) {
	setReadOnly(t, false)
	v, svc := dlqMessages(t)
	_, cmd := v.Update(msg.ConfirmResultMsg{Confirmed: true, Tag: &sqsDeleteMsgsTag{ids: []string{"gone"}}})
	assert.Nil(t, cmd)
	svc.AssertNotCalled(t, "DeleteMessageBatch", mock.Anything, mock.Anything, mock.Anything)
}

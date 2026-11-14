package views

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// confirmSQS presses m, picks action and answers the confirm with yes. It
// returns the command of the confirm reply, not run.
func confirmSQS(t *testing.T, v *SQSQueues, action, actionID string) tea.Cmd {
	t.Helper()
	charSQSConfirm(t, v, action)
	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: actionID})
	require.NotNil(t, cmd)
	return cmd
}

func TestSQSQueues_PurgeSpinnerHidesWhenAllFail(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	svc.On("PurgeQueue", mock.Anything, charSQSURL("qa")).Return(charSQSDenied).Once()

	cmd := confirmSQS(t, v, "Purge Queue", "sqs_purge")
	require.True(t, v.spinner.Visible())
	seen := pump(v, cmd, 3)

	assert.Equal(t, []msg.ToastMsg{msg.ToastError("Purged 0 of 1 queue(s). Failed: qa (AccessDenied)")}, toasts(seen))
	assert.False(t, v.spinner.Visible(), "no refresh follows, so the purge reply hides the spinner")
	svc.AssertNotCalled(t, "GetQueueAttributes", mock.Anything, mock.Anything)
}

func TestSQSQueues_ActionSpinnerTicks(t *testing.T) {
	charSQSReadWrite(t)
	v, _ := charSQSThree(t)

	cmd := confirmSQS(t, v, "Delete Queue", "sqs_delete")
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	tick, ok := batch[0]().(spinner.TickMsg)
	require.True(t, ok)

	_, next := v.Update(tick)
	assert.NotNil(t, next, "the spinner asks for the next tick while the delete runs")
}

func TestSQSQueues_ActionReplyKeepsLoadSpinner(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	svc.On("DeleteQueue", mock.Anything, charSQSURL("qa")).Return(nil).Once()

	cmd := confirmSQS(t, v, "Delete Queue", "sqs_delete")
	replies := charSQSRun(cmd)
	require.Len(t, replies, 1)

	_, _ = v.Update(keyPress('r')) // full reload, not run
	require.True(t, v.loading)
	charSQSDeliver(v, replies...)
	assert.True(t, v.spinner.Visible(), "the full load keeps its spinner")
	svc.AssertExpectations(t)
}

func TestSQSQueues_TwoActionsHideSpinnerAfterBoth(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	svc.On("DeleteQueue", mock.Anything, mock.Anything).Return(nil)

	first := charSQSRun(confirmSQS(t, v, "Delete Queue", "sqs_delete"))
	charSQSDown(v)
	second := charSQSRun(confirmSQS(t, v, "Delete Queue", "sqs_delete"))

	charSQSDeliver(v, first...)
	assert.True(t, v.spinner.Visible(), "one delete still runs")
	charSQSDeliver(v, second...)
	assert.False(t, v.spinner.Visible())
}

func TestSQSQueues_PromptNamesComeFromRows(t *testing.T) {
	charSQSReadWrite(t)
	v, _ := charSQSThree(t)
	charSQSDown(v)
	charSQSDown(v)
	// The row name wins over the name in the URL, and it is made safe.
	v.items[2].Name = "shown\x1b[31m"
	req := charSQSConfirm(t, v, "Purge Queue")
	assert.Equal(t, "Purge 1 queue(s)?\n  shown␛[31m\nThis removes all messages and cannot be undone.", req.Message)
}

func TestSQSQueues_OtherFormClearsSendTarget(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	charSQSSendForm(t, v)

	_, cmd := v.Update(msg.FormResultMsg{ID: "other_form", Values: map[string]string{"body": "x"}})
	assert.Nil(t, cmd)
	_, cmd = v.Update(msg.FormResultMsg{ID: "sqs_send_message", Values: map[string]string{"body": "late"}})
	assert.Nil(t, cmd, "the target was cleared")
	svc.AssertNotCalled(t, "SendMessage", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestSQSQueues_SendClearsConfirmTargets(t *testing.T) {
	charSQSReadWrite(t)
	v, svc := charSQSThree(t)
	charSQSSendForm(t, v)

	_, cmd := v.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "sqs_purge"})
	assert.Nil(t, cmd)
	svc.AssertNotCalled(t, "PurgeQueue", mock.Anything, mock.Anything)
}

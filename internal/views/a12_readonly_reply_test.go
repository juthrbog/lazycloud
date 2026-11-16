package views

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// A12 PR 7 (D7): the mode can change to ReadOnly while a prompt of S3
// objects or SQS messages is open. A yes then shows the ReadOnly toast and
// calls nothing. Before PR 7 the guarded service refused the call, and the
// view showed a service error. The mocks have no expectations, so a call
// to AWS fails the test.
func TestA12_ReadOnlyAtTheReplyShowsTheToast(t *testing.T) {
	for _, c := range []struct {
		name string
		// ask opens the prompt in ReadWrite mode and returns its request
		// and a check that the view did not start the action.
		ask func(t *testing.T) (v tea.Model, req msg.RequestConfirmMsg, idle func() bool)
	}{
		{"s3 overwrite", func(t *testing.T) (tea.Model, msg.RequestConfirmMsg, func() bool) {
			v, _ := newS3TransferView(t)
			_, cmd := v.Update(s3CopyCompleteMsg{bucket: "test-bucket", srcKey: "a.txt", dstKey: "b.txt", err: aws.ErrDestinationExists})
			req := requestOf[msg.RequestConfirmMsg](t, cmd)
			require.IsType(t, &s3OverwriteTag{}, req.Tag)
			return v, req, func() bool { return !v.spinner.Visible() }
		}},
		{"s3 delete", func(t *testing.T) (tea.Model, msg.RequestConfirmMsg, func() bool) {
			v, _ := newS3TransferView(t)
			_, cmd := v.Update(keyPress('x'))
			req := requestOf[msg.RequestConfirmMsg](t, cmd)
			require.NotEmpty(t, s3DeleteEntriesOf(t, req))
			return v, req, func() bool { return !v.spinner.Visible() && len(v.entries) == 3 }
		}},
		{"sqs delete", func(t *testing.T) (tea.Model, msg.RequestConfirmMsg, func() bool) {
			setReadOnly(t, false)
			v, _ := dlqMessages(t)
			_, cmd := v.Update(keyPress('x'))
			req := requestOf[msg.RequestConfirmMsg](t, cmd)
			require.IsType(t, &sqsDeleteMsgsTag{}, req.Tag)
			return v, req, func() bool { return v.table.SelectionCount() == 3 }
		}},
		{"sqs redrive", func(t *testing.T) (tea.Model, msg.RequestConfirmMsg, func() bool) {
			setReadOnly(t, false)
			v, _ := dlqMessages(t)
			_, cmd := v.Update(keyPress('m'))
			picker := requestOf[msg.RequestActionPickerMsg](t, cmd)
			_, cmd = v.Update(pickLabel(t, picker, "Start Redrive"))
			req := requestOf[msg.RequestConfirmMsg](t, cmd)
			require.IsType(t, &sqsRedriveTag{}, req.Tag)
			return v, req, func() bool { return !v.redriving && !v.spinner.Visible() }
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			v, req, idle := c.ask(t)
			require.False(t, ui.IsReadOnly())

			ui.SetReadOnly(true)
			_, cmd := v.Update(confirmReply(req, true))
			assert.Equal(t, []tea.Msg{msg.ToastError(readOnlyText)}, collectMsgs(cmd))
			assert.True(t, idle(), "the view starts nothing")
		})
	}
}

// In ReadWrite mode, the same yes still runs the action. This keeps the
// test above honest: its prompts are ones that act on a yes.
func TestA12_ReadWriteAtTheReplyRunsTheAction(t *testing.T) {
	t.Run("s3 delete", func(t *testing.T) {
		v, _ := newS3TransferView(t)
		_, cmd := v.Update(keyPress('x'))
		req := requestOf[msg.RequestConfirmMsg](t, cmd)
		_, cmd = v.Update(confirmReply(req, true))
		require.NotNil(t, cmd)
		assert.True(t, v.spinner.Visible())
	})
	t.Run("sqs redrive", func(t *testing.T) {
		setReadOnly(t, false)
		v, _ := dlqMessages(t)
		_, cmd := v.Update(keyPress('m'))
		picker := requestOf[msg.RequestActionPickerMsg](t, cmd)
		_, cmd = v.Update(pickLabel(t, picker, "Start Redrive"))
		req := requestOf[msg.RequestConfirmMsg](t, cmd)
		_, cmd = v.Update(confirmReply(req, true))
		require.NotNil(t, cmd)
		assert.True(t, v.redriving)
	})
}

// A no, a yes with a tag of another view, and a yes with a typed-nil tag
// do nothing in ReadOnly mode too. The toast is only for a yes to a prompt of the view.
func TestA12_ReadOnlyToastOnlyForAYesOfTheView(t *testing.T) {
	setReadOnly(t, true)
	s3, _ := newTestS3Objects()
	sqs, _ := dlqMessages(t)
	for _, c := range []struct {
		name  string
		v     tea.Model
		reply msg.ConfirmResultMsg
	}{
		{"s3 no", s3, msg.ConfirmResultMsg{Confirmed: false, Tag: &s3DeleteTag{entries: []s3TableEntry{{}}}}},
		{"s3 foreign yes", s3, msg.ConfirmResultMsg{Confirmed: true, Tag: &sqsRedriveTag{}}},
		{"s3 empty delete", s3, msg.ConfirmResultMsg{Confirmed: true, Tag: &s3DeleteTag{}}},
		{"sqs no", sqs, msg.ConfirmResultMsg{Confirmed: false, Tag: &sqsRedriveTag{}}},
		{"sqs foreign yes", sqs, msg.ConfirmResultMsg{Confirmed: true, Tag: &s3DeleteTag{entries: []s3TableEntry{{}}}}},
		{"s3 typed-nil overwrite", s3, msg.ConfirmResultMsg{Confirmed: true, Tag: (*s3OverwriteTag)(nil)}},
		{"s3 typed-nil delete", s3, msg.ConfirmResultMsg{Confirmed: true, Tag: (*s3DeleteTag)(nil)}},
		{"sqs typed-nil delete", sqs, msg.ConfirmResultMsg{Confirmed: true, Tag: (*sqsDeleteMsgsTag)(nil)}},
		{"sqs typed-nil redrive", sqs, msg.ConfirmResultMsg{Confirmed: true, Tag: (*sqsRedriveTag)(nil)}},
	} {
		_, cmd := c.v.Update(c.reply)
		assert.Nil(t, cmd, c.name)
	}
}

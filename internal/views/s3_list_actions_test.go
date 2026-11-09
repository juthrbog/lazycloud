package views

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func s3SetReadOnly(t *testing.T, ro bool) {
	t.Helper()
	prev := ui.IsReadOnly()
	t.Cleanup(func() { ui.SetReadOnly(prev) })
	ui.SetReadOnly(ro)
}

func TestS3List_DeleteHoldsTargetsAndAction(t *testing.T) {
	s3SetReadOnly(t, false)
	view, _ := newTestS3List()
	loadBuckets(view)
	view.Update(keyPress(' '))
	view.Update(keyPress('j'))
	view.Update(keyPress(' '))

	_, cmd := view.Update(keyPress('x'))
	require.NotNil(t, cmd)
	_, ok := cmd().(msg.RequestConfirmMsg)
	require.True(t, ok)
	assert.Equal(t, "delete_bucket", view.pendingDelete.action)
	assert.Equal(t, []string{"test-bucket", "other-bucket"}, view.pendingDelete.keys)
}

func TestS3List_ConfirmForOtherActionDoesNotDelete(t *testing.T) {
	s3SetReadOnly(t, false)
	view, svc := newTestS3List()
	loadBuckets(view)
	view.Update(keyPress('x'))

	_, cmd := view.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "ec2_stop"})
	assert.Nil(t, cmd)
	assert.Empty(t, view.pendingDelete.keys)
	assert.Empty(t, view.pendingDelete.action)
	svc.AssertNotCalled(t, "EmptyAndDeleteBucket", mock.Anything, mock.Anything)
}

func TestS3List_ReadOnlyBetweenPromptAndConfirmDoesNotDelete(t *testing.T) {
	s3SetReadOnly(t, false)
	view, svc := newTestS3List()
	loadBuckets(view)
	view.Update(keyPress('x'))

	ui.SetReadOnly(true)
	_, cmd := view.Update(ui.ConfirmResultMsg{Confirmed: true, Action: "delete_bucket"})
	require.NotNil(t, cmd)
	assert.Equal(t, msg.ToastError("ReadOnly mode — press W to switch"), cmd())
	assert.Empty(t, view.pendingDelete.keys)
	svc.AssertNotCalled(t, "EmptyAndDeleteBucket", mock.Anything, mock.Anything)
}

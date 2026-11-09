package views

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
)

// A view with no SQS client shows an error, it does not panic (#47).
func TestSQSQueues_NilClient(t *testing.T) {
	v := NewSQSQueues(nil)
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	e, ok := firstOfType[msg.ErrorMsg](collectMsgs(v.Init()))
	require.True(t, ok)
	assert.Equal(t, "SQS", e.Context)
	require.ErrorIs(t, e.Err, errNoClient)

	assert.NotPanics(t, func() {
		assert.Equal(t, sqsQueueDetailMsg{err: errNoClient}, v.fetchQueueDetail("u")())
		assert.Equal(t, sqsQueueRefreshedMsg{err: errNoClient}, v.refreshQueue("u")())
		got, ok := v.fetchAllAttributes([]string{"u"})().(msg.ErrorMsg)
		require.True(t, ok)
		require.ErrorIs(t, got.Err, errNoClient)
	})
}

func TestDetailErrorToast(t *testing.T) {
	cmd := detailErrorToast(errors.New("AccessDenied"))
	require.NotNil(t, cmd)
	assert.Equal(t, msg.ToastError("Details failed: AccessDenied"), cmd())

	// The user left the view: no toast.
	assert.Nil(t, detailErrorToast(context.Canceled))
	assert.Nil(t, detailErrorToast(fmt.Errorf("get: %w", context.Canceled)))
}

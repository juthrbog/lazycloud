package views

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws"
	"github.com/juthrbog/lazycloud/internal/msg"
)

// A view with no SQS client shows an error, it does not panic (#47).
func TestSQSQueues_NilClient(t *testing.T) {
	v := NewSQSQueues(nil)
	v.Update(tea.WindowSizeMsg{Width: 120, Height: 20})
	e, ok := firstOfType[msg.ErrorMsg](pump(v, v.Init(), 3))
	require.True(t, ok)
	// Design change in PR 8: the context is the one of the load, as in the
	// other ported lists.
	assert.Equal(t, "listing SQS queues", e.Context)
	require.ErrorIs(t, e.Err, errNoClient)

	assert.NotPanics(t, func() {
		assert.Equal(t, sqsQueueDetailMsg{err: errNoClient}, v.fetchQueueDetail("u")())
		_, err := v.cfg.Refresh(context.Background(), []string{"u"})
		require.ErrorIs(t, err, errNoClient)
		_, err = v.cfg.Complete.Run(context.Background(), []aws.Queue{{URL: "u"}})
		require.ErrorIs(t, err, errNoClient)
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

package ops

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBeginAndDone(t *testing.T) {
	r := NewRegistry()
	a := r.Begin("Download a", "default/us-east-1")
	b := r.Begin("Delete b", "prod/eu-west-1")

	require.Equal(t, 2, r.Count())
	running := r.Running()
	require.Len(t, running, 2)
	assert.Equal(t, "Download a", running[0].Label, "oldest first")
	assert.Equal(t, "prod/eu-west-1", running[1].Session)
	require.NoError(t, a.Ctx.Err())

	a.Done()
	require.ErrorIs(t, a.Ctx.Err(), context.Canceled, "Done releases the context")
	assert.Equal(t, 1, r.Count())
	a.Done() // a second call does nothing
	assert.Equal(t, 1, r.Count())

	b.Done()
	assert.Equal(t, 0, r.Count())
}

func TestCancelAllCancelsAndCloses(t *testing.T) {
	r := NewRegistry()
	op := r.Begin("Download a", "")
	r.CancelAll()

	require.ErrorIs(t, op.Ctx.Err(), context.Canceled)
	assert.Equal(t, 1, r.Count(), "an operation stays registered until it calls Done")

	late := r.Begin("Download b", "")
	require.ErrorIs(t, late.Ctx.Err(), context.Canceled, "nothing starts after CancelAll")
	assert.Equal(t, 1, r.Count())
	late.Done()
	op.Done()
	assert.Equal(t, 0, r.Count())
}

func TestWait(t *testing.T) {
	r := NewRegistry()
	assert.True(t, r.Wait(time.Millisecond), "an empty registry is idle")

	op := r.Begin("Download a", "")
	assert.False(t, r.Wait(10*time.Millisecond), "times out while an operation runs")

	go func() {
		<-op.Ctx.Done()
		op.Done()
	}()
	r.CancelAll()
	assert.True(t, r.Wait(time.Second))
}

func TestWaitAfterRestart(t *testing.T) {
	r := NewRegistry()
	r.Begin("a", "").Done()
	op := r.Begin("b", "")
	assert.False(t, r.Wait(10*time.Millisecond), "idle again only after the second operation ends")
	op.Done()
	assert.True(t, r.Wait(time.Second))
}

func TestCancelOne(t *testing.T) {
	r := NewRegistry()
	a := r.Begin("Download a", "")
	b := r.Begin("Download b", "")

	require.True(t, r.Cancel(a.ID))
	require.ErrorIs(t, a.Ctx.Err(), context.Canceled)
	assert.True(t, CanceledByUser(a.Ctx))
	require.NoError(t, b.Ctx.Err(), "the other operation goes on")

	running := r.Running()
	require.Len(t, running, 2, "a cancelled operation stays until it calls Done")
	assert.True(t, running[0].Canceling)
	assert.False(t, running[1].Canceling)

	a.Done()
	assert.False(t, r.Cancel(a.ID), "an ended operation cannot be cancelled")
	b.Done()
}

func TestQuitIsNotACancelByTheUser(t *testing.T) {
	r := NewRegistry()
	op := r.Begin("Redrive dlq", "")
	r.CancelAll()
	require.ErrorIs(t, op.Ctx.Err(), context.Canceled)
	assert.False(t, CanceledByUser(op.Ctx))
	require.ErrorIs(t, context.Cause(op.Ctx), ErrQuit)
	op.Done()
}

// An HTTP body read returns the cause of the context, not ctx.Err(), so
// the causes must still count as a cancel.
func TestCausesAreCancels(t *testing.T) {
	require.ErrorIs(t, ErrCanceledByUser, context.Canceled)
	require.ErrorIs(t, ErrQuit, context.Canceled)
}

func TestReport(t *testing.T) {
	r := NewRegistry()
	op := r.Begin("Download a", "")
	Report(op.Ctx, Progress{Done: 5, Total: 10, Bytes: true})
	assert.Equal(t, Progress{Done: 5, Total: 10, Bytes: true}, r.Running()[0].Progress)

	// A context of no operation is fine, and a derived context still
	// reaches the operation.
	Report(context.Background(), Progress{Done: 1})
	ctx, cancel := context.WithTimeout(op.Ctx, time.Minute)
	defer cancel()
	Report(ctx, Progress{Done: 7, Total: 10})
	assert.Equal(t, int64(7), r.Running()[0].Progress.Done)
	op.Done()
}

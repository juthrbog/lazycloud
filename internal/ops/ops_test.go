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

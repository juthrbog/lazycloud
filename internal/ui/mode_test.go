package ui

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// useMode makes m the current mode for one test.
func useMode(t *testing.T, m *SessionMode) {
	t.Helper()
	before := CurrentMode()
	SetCurrentMode(m)
	t.Cleanup(func() { SetCurrentMode(before) })
}

func TestSessionModeStartsReadOnly(t *testing.T) {
	m := NewSessionMode()
	assert.True(t, m.ReadOnly())

	require.NoError(t, m.SetReadWrite())
	assert.False(t, m.ReadOnly())

	m.SetReadOnly()
	assert.True(t, m.ReadOnly())
}

// IsReadOnly and SetReadOnly act on the current mode only.
func TestReadOnlyActsOnTheCurrentMode(t *testing.T) {
	a, b := NewSessionMode(), NewSessionMode()
	useMode(t, a)

	SetReadOnly(false)
	assert.False(t, IsReadOnly())
	assert.False(t, a.ReadOnly())
	assert.True(t, b.ReadOnly(), "another session keeps its mode")

	SetCurrentMode(b)
	assert.True(t, IsReadOnly())
	SetReadOnly(false)
	SetReadOnly(true)
	assert.True(t, b.ReadOnly())
	assert.False(t, a.ReadOnly(), "the old session keeps its mode")
}

// The guards read the mode from command goroutines while the app changes
// it. Run with -race.
func TestSessionModeIsSafeForConcurrentUse(t *testing.T) {
	m := NewSessionMode()
	useMode(t, m)
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for range 100 {
				if i%2 == 0 {
					_ = m.SetReadWrite()
					SetReadOnly(true)
				} else {
					_ = IsReadOnly()
					SetCurrentMode(m)
				}
			}
		})
	}
	wg.Wait()
}

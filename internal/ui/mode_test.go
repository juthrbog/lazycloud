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

// A lock refuses ReadWrite, also through the SetReadOnly wrapper.
func TestLockRefusesReadWrite(t *testing.T) {
	m := NewSessionMode()
	require.NoError(t, m.SetReadWrite())
	locked, _ := m.Locked()
	require.False(t, locked)

	m.Lock("prod")

	assert.True(t, m.ReadOnly(), "a lock sets ReadOnly")
	locked, rule := m.Locked()
	assert.True(t, locked)
	assert.Equal(t, "prod", rule)
	require.EqualError(t, m.SetReadWrite(), "locked to ReadOnly by rule prod")
	assert.True(t, m.ReadOnly())

	useMode(t, m)
	SetReadOnly(false)
	assert.True(t, IsReadOnly(), "the wrapper cannot pass the lock")
}

// An unchecked account refuses ReadWrite until the check ends. A lock
// names the rule first.
func TestUncheckedRefusesReadWrite(t *testing.T) {
	m := NewSessionMode()
	assert.True(t, m.Checked(), "a new mode needs no check")

	m.SetChecked(false)

	assert.False(t, m.Checked())
	require.ErrorIs(t, m.SetReadWrite(), ErrNotChecked)
	assert.True(t, m.ReadOnly())
	useMode(t, m)
	SetReadOnly(false)
	assert.True(t, IsReadOnly(), "the wrapper cannot pass the check")

	m.Lock("prod")
	require.EqualError(t, m.SetReadWrite(), "locked to ReadOnly by rule prod")

	n := NewSessionMode()
	n.SetChecked(false)
	n.SetChecked(true)
	require.NoError(t, n.SetReadWrite())
	assert.False(t, n.ReadOnly())
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
					m.SetChecked(i%4 == 0)
					SetReadOnly(true)
				} else {
					_ = IsReadOnly()
					_ = m.Checked()
					SetCurrentMode(m)
				}
			}
		})
	}
	wg.Wait()
}

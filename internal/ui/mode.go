package ui

import "sync/atomic"

// SessionMode is the access mode of one session (a profile and a region).
// Each session has its own mode, so the guards of a session that runs an
// operation in the background read the mode of that session, not the mode
// of the session on screen. The guards read it from command goroutines, so
// it is safe for concurrent use.
type SessionMode struct {
	readOnly atomic.Bool
}

// NewSessionMode returns a mode that starts in ReadOnly.
func NewSessionMode() *SessionMode {
	m := &SessionMode{}
	m.readOnly.Store(true)
	return m
}

// ReadOnly reports whether the session refuses changes to AWS resources.
func (m *SessionMode) ReadOnly() bool { return m.readOnly.Load() }

// SetReadOnly switches the session to ReadOnly.
func (m *SessionMode) SetReadOnly() { m.readOnly.Store(true) }

// SetReadWrite switches the session to ReadWrite. It returns an error and
// changes nothing when the session cannot go to ReadWrite. Today no
// session refuses. The locks of docs/design/phase9-profiles.md come here.
func (m *SessionMode) SetReadWrite() error {
	m.readOnly.Store(false)
	return nil
}

// current is the mode of the session on screen. It starts with a ReadOnly
// mode, which main can change for --read-write before the first session.
var current atomic.Pointer[SessionMode]

func init() { current.Store(NewSessionMode()) }

// CurrentMode returns the mode of the session on screen.
func CurrentMode() *SessionMode { return current.Load() }

// SetCurrentMode makes m the mode of the session on screen. The app calls
// it when a session starts.
func SetCurrentMode(m *SessionMode) { current.Store(m) }

// IsReadOnly reports whether the session on screen is in ReadOnly mode. It
// is safe to call from any goroutine.
func IsReadOnly() bool { return CurrentMode().ReadOnly() }

// SetReadOnly switches the session on screen between ReadOnly (true) and
// ReadWrite (false). A switch to ReadWrite goes through SetReadWrite, so it
// cannot pass a refusal of the session.
func SetReadOnly(v bool) {
	if v {
		CurrentMode().SetReadOnly()
		return
	}
	_ = CurrentMode().SetReadWrite()
}

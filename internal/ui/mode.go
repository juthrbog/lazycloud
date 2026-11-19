package ui

import (
	"errors"
	"fmt"
	"sync/atomic"
)

// SessionMode is the access mode of one session (a profile and a region).
// Each session has its own mode, so the guards of a session that runs an
// operation in the background read the mode of that session, not the mode
// of the session on screen. The guards read it from command goroutines, so
// it is safe for concurrent use.
type SessionMode struct {
	readOnly atomic.Bool
	lock     atomic.Pointer[string] // the rule that locks the session, nil when none does
	// unchecked is true while the app waits for STS to name the account of
	// the session. Only a config with rules by account needs the check.
	unchecked atomic.Bool
}

// ErrNotChecked is the error of SetReadWrite while the account of the
// session is not checked.
var ErrNotChecked = errors.New("the account is not checked yet")

// NewSessionMode returns a mode that starts in ReadOnly, with its account
// checked.
func NewSessionMode() *SessionMode {
	m := &SessionMode{}
	m.readOnly.Store(true)
	return m
}

// Checked reports whether the account of the session is checked, or
// needs no check.
func (m *SessionMode) Checked() bool { return !m.unchecked.Load() }

// SetChecked records whether the account of the session is checked. While
// it is not, SetReadWrite refuses, so a rule by account can lock the
// session before the user can change resources.
func (m *SessionMode) SetChecked(v bool) { m.unchecked.Store(!v) }

// ReadOnly reports whether the session refuses changes to AWS resources.
func (m *SessionMode) ReadOnly() bool { return m.readOnly.Load() }

// SetReadOnly switches the session to ReadOnly.
func (m *SessionMode) SetReadOnly() { m.readOnly.Store(true) }

// Lock locks the session to ReadOnly. rule names the rule of the config
// that locks it. A lock stays for the life of the session.
func (m *SessionMode) Lock(rule string) {
	m.lock.Store(&rule)
	m.readOnly.Store(true)
}

// Locked reports whether a rule locks the session, and which rule.
func (m *SessionMode) Locked() (bool, string) {
	if r := m.lock.Load(); r != nil {
		return true, *r
	}
	return false, ""
}

// SetReadWrite switches the session to ReadWrite. It returns an error and
// changes nothing when a rule locks the session, or ErrNotChecked when the
// account of the session is not checked.
func (m *SessionMode) SetReadWrite() error {
	if locked, rule := m.Locked(); locked {
		return fmt.Errorf("locked to ReadOnly by rule %s", rule)
	}
	if !m.Checked() {
		return ErrNotChecked
	}
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
// cannot pass a lock.
func SetReadOnly(v bool) {
	if v {
		CurrentMode().SetReadOnly()
		return
	}
	_ = CurrentMode().SetReadWrite()
}

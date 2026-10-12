package ui

import "sync/atomic"

// readOnly controls whether mutating operations are blocked. It starts true
// and changes at runtime through the mode picker. The ReadOnly guards of the
// AWS services read it from command goroutines, so it is atomic.
var readOnly atomic.Bool

func init() { readOnly.Store(true) }

// IsReadOnly reports whether the app is in ReadOnly mode. It is safe to call
// from any goroutine.
func IsReadOnly() bool { return readOnly.Load() }

// SetReadOnly switches between ReadOnly (true) and ReadWrite (false) mode.
func SetReadOnly(v bool) { readOnly.Store(v) }

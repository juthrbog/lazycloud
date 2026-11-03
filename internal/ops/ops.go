// Package ops keeps track of long operations, such as an S3 download or a
// bucket delete, that run in the background.
//
// An operation does not belong to a view. It keeps running when the user
// leaves the view that started it, and when the user switches the profile or
// the region. The app registers each operation on the event loop (see
// Registry.Begin), so the quit and session-switch prompts always see an
// operation that the user has started.
package ops

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

// ErrCanceledByUser is the cause of the context of an operation that the
// user cancelled (see Registry.Cancel). Some calls, such as a read of an
// HTTP body, return the cause instead of context.Canceled, so the cause
// wraps context.Canceled: errors.Is(err, context.Canceled) is true for both.
var ErrCanceledByUser = fmt.Errorf("cancelled by the user: %w", context.Canceled)

// ErrQuit is the cause of the context of an operation that ended because
// the app quits (see Registry.CancelAll). It wraps context.Canceled too.
var ErrQuit = fmt.Errorf("the app quits: %w", context.Canceled)

// Progress tells how far an operation is. Total is 0 when it is not known.
type Progress struct {
	Done  int64
	Total int64
	Bytes bool // Done and Total count bytes, not items
}

// Op is one running operation.
type Op struct {
	ID      int
	Label   string // what the operation does, for example "Download a/b.txt"
	Session string // the profile and region it runs in, for example "default/us-east-1"
	Started time.Time

	// Ctx is the context the operation must use. It is cancelled when the
	// user quits, never when a view closes or the session changes.
	// Report and CanceledByUser find the operation through it.
	Ctx context.Context

	// Progress is the last progress that the operation reported. It is
	// zero until the operation calls Report.
	Progress Progress

	// Canceling is true after the user cancelled the operation and before
	// it ends.
	Canceling bool

	cancel context.CancelCauseFunc
	reg    *Registry
}

type opKey struct{}

// Report records the progress of the operation that runs with ctx. It does
// nothing when ctx belongs to no operation, so the code that reports
// progress also works outside an operation, for example in tests.
func Report(ctx context.Context, p Progress) {
	op, ok := ctx.Value(opKey{}).(*Op)
	if !ok {
		return
	}
	op.reg.mu.Lock()
	defer op.reg.mu.Unlock()
	op.Progress = p
}

// CanceledByUser reports whether the user cancelled the operation that
// runs with ctx. An operation can then undo work that goes on without it,
// such as an SQS move task. Quitting is not a cancel by the user.
func CanceledByUser(ctx context.Context) bool {
	return errors.Is(context.Cause(ctx), ErrCanceledByUser)
}

// Done removes the operation from the registry. Call it once when the
// operation ends, also after an error. A second call does nothing.
func (o *Op) Done() {
	o.cancel(nil)
	o.reg.remove(o.ID)
}

// Registry holds the running operations. It is safe for concurrent use.
type Registry struct {
	mu     sync.Mutex
	next   int
	ops    map[int]*Op
	closed bool
	idle   chan struct{} // closed while no operation runs
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	idle := make(chan struct{})
	close(idle)
	return &Registry{ops: map[int]*Op{}, idle: idle}
}

// Begin registers a new operation and returns it. After CancelAll, Begin
// returns an operation whose context is already cancelled and does not
// register it, so nothing starts while the app quits.
func (r *Registry) Begin(label, session string) *Op {
	ctx, cancel := context.WithCancelCause(context.Background())
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	op := &Op{ID: r.next, Label: label, Session: session, Started: time.Now(), cancel: cancel, reg: r}
	op.Ctx = context.WithValue(ctx, opKey{}, op)
	if r.closed {
		cancel(ErrQuit)
		return op
	}
	if len(r.ops) == 0 {
		r.idle = make(chan struct{})
	}
	r.ops[op.ID] = op
	return op
}

func (r *Registry) remove(id int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.ops[id]; !ok {
		return
	}
	delete(r.ops, id)
	if len(r.ops) == 0 {
		close(r.idle)
	}
}

// Running returns a copy of the running operations, oldest first.
func (r *Registry) Running() []Op {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Op, 0, len(r.ops))
	for _, op := range r.ops {
		out = append(out, *op)
	}
	slices.SortFunc(out, func(a, b Op) int { return a.ID - b.ID })
	return out
}

// Count returns the number of running operations.
func (r *Registry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ops)
}

// Cancel cancels the running operation with the given ID, because the user
// asked for it. The operation stays registered until it calls Done. Cancel
// reports false when no such operation runs.
func (r *Registry) Cancel(id int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	op, ok := r.ops[id]
	if !ok {
		return false
	}
	op.Canceling = true
	op.cancel(ErrCanceledByUser)
	return true
}

// CancelAll cancels every running operation and closes the registry, so
// that Begin starts nothing new. The operations stay registered until they
// call Done.
func (r *Registry) CancelAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for _, op := range r.ops {
		op.cancel(ErrQuit)
	}
}

// Wait blocks until no operation runs or the timeout passes. It reports
// whether every operation ended.
func (r *Registry) Wait(timeout time.Duration) bool {
	r.mu.Lock()
	idle := r.idle
	r.mu.Unlock()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-idle:
		return true
	case <-t.C:
		return false
	}
}

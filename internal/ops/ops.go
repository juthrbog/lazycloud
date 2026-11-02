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
	"slices"
	"sync"
	"time"
)

// Op is one running operation.
type Op struct {
	ID      int
	Label   string // what the operation does, for example "Download a/b.txt"
	Session string // the profile and region it runs in, for example "default/us-east-1"
	Started time.Time

	// Ctx is the context the operation must use. It is cancelled when the
	// user quits, never when a view closes or the session changes.
	Ctx context.Context

	cancel context.CancelFunc
	reg    *Registry
}

// Done removes the operation from the registry. Call it once when the
// operation ends, also after an error. A second call does nothing.
func (o *Op) Done() {
	o.cancel()
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
	ctx, cancel := context.WithCancel(context.Background())
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next++
	op := &Op{ID: r.next, Label: label, Session: session, Started: time.Now(), Ctx: ctx, cancel: cancel, reg: r}
	if r.closed {
		cancel()
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

// CancelAll cancels every running operation and closes the registry, so
// that Begin starts nothing new. The operations stay registered until they
// call Done.
func (r *Registry) CancelAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for _, op := range r.ops {
		op.cancel()
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

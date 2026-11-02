package nav

import (
	"context"
	"reflect"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// View is the interface that all navigable views must implement.
type View interface {
	tea.Model
	ID() string               // identifier, e.g. "ec2_list" or "s3_objects:<bucket>:<prefix>"
	Title() string            // human-readable title for breadcrumb display
	KeyMap() []ui.HintBinding // view-specific keybindings for status bar display
	Footer() string           // resource count / context info for footer line
}

// InputCapturer is implemented by views that take text input, for example a
// filter or a form. While CapturingInput returns true, the app sends every key
// except ctrl+c to the view, so a typed letter never runs an app-wide key such
// as q, W, or tab.
type InputCapturer interface {
	CapturingInput() bool
}

// ContextSetter is implemented by views that make AWS calls. The navigator
// calls SetContext before Init with a context that it cancels when the view
// leaves the stack, so the view's loads stop when the user leaves it.
type ContextSetter interface {
	SetContext(ctx context.Context)
}

// Navigator manages a stack of views. Each view on the stack has its own
// context (see ContextSetter). A view that leaves the stack is gone: its
// context is cancelled and its late results are dropped. Navigating to the
// same ID again builds a new view.
type Navigator struct {
	stack   []View
	ctxs    []context.Context    // parallel to stack
	cancels []context.CancelFunc // parallel to stack
}

// New creates a Navigator with the given root view.
func New(root View) *Navigator {
	n := &Navigator{}
	n.push(root)
	return n
}

// push adds v to the stack with a new context.
func (n *Navigator) push(v View) {
	ctx, cancel := context.WithCancel(context.Background())
	if cs, ok := v.(ContextSetter); ok {
		cs.SetContext(ctx)
	}
	n.stack = append(n.stack, v)
	n.ctxs = append(n.ctxs, ctx)
	n.cancels = append(n.cancels, cancel)
}

// Routed carries the result of a command back to the view that issued it.
//
// The navigator tags every command a view returns (see Tag). The app then
// delivers the result to that exact view instance while it is on the stack,
// also when another view is on top of it. It drops the result when the
// instance is gone: popped, or from an earlier navigator after a profile or
// region switch. Without this, an
// async result would reach whichever view happens to be on top.
type Routed struct {
	Origin View
	Msg    tea.Msg
}

// Tag wraps cmd so that its result arrives as Routed{Origin: origin}.
//
// Bubble Tea runtime messages (exec, clipboard, quit, sequences, and other
// types from the tea package) pass through unwrapped, because the runtime
// must see them. A tea.BatchMsg is rebuilt with each inner command tagged.
func Tag(origin View, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg { return tagMsg(origin, cmd()) }
}

func tagMsg(origin View, m tea.Msg) tea.Msg {
	switch m := m.(type) {
	case nil:
		return nil
	case Routed:
		return m
	case tea.BatchMsg:
		out := make(tea.BatchMsg, len(m))
		for i, c := range m {
			out[i] = Tag(origin, c)
		}
		return out
	}
	if isRuntimeMsg(m) {
		return m
	}
	return Routed{Origin: origin, Msg: m}
}

var teaPkgPath = reflect.TypeFor[tea.BatchMsg]().PkgPath()

// isRuntimeMsg reports whether m is a type from the Bubble Tea package.
// Several of these are unexported and handled only by the runtime.
func isRuntimeMsg(m tea.Msg) bool {
	t := reflect.TypeOf(m)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.PkgPath() == teaPkgPath
}

// Push adds a view to the stack, gives it a context, and runs its Init.
func (n *Navigator) Push(v View) tea.Cmd {
	n.push(v)
	return Tag(v, v.Init())
}

// InitCurrent runs Init on the current view and tags the returned command.
func (n *Navigator) InitCurrent() tea.Cmd {
	v := n.Current()
	return Tag(v, v.Init())
}

// Contains reports whether v is on the stack. A view that was popped, or a
// view from an earlier navigator, is never contained.
func (n *Navigator) Contains(v View) bool {
	return n.index(v) >= 0
}

// index returns the highest stack position of v, or -1.
func (n *Navigator) index(v View) int {
	for i, sv := range slices.Backward(n.stack) {
		if sv == v {
			return i
		}
	}
	return -1
}

// IsCurrent reports whether v is the view on top of the stack.
func (n *Navigator) IsCurrent(v View) bool {
	return n.Current() == v
}

// UpdateView sends msg to the given view on the stack and stores the
// returned model. It returns nil without calling Update when v is not on
// the stack.
func (n *Navigator) UpdateView(v View, msg tea.Msg) tea.Cmd {
	i := n.index(v)
	if i < 0 {
		return nil
	}
	return n.update(i, msg)
}

// update sends msg to the view at stack position i. When Update returns a
// different instance, the new instance takes the old one's place and its
// context.
func (n *Navigator) update(i int, msg tea.Msg) tea.Cmd {
	v := n.stack[i]
	updated, cmd := v.Update(msg)
	uv, ok := updated.(View)
	if !ok {
		return Tag(v, cmd)
	}
	if uv != v {
		for j := range n.stack {
			if n.stack[j] == v {
				n.stack[j] = uv
				if cs, ok := uv.(ContextSetter); ok {
					cs.SetContext(n.ctxs[j])
				}
			}
		}
	}
	return Tag(uv, cmd)
}

// Pop removes the top view, cancels its context, and returns the view
// below it.
func (n *Navigator) Pop() View {
	if len(n.stack) > 1 {
		n.PopTo(len(n.stack) - 1)
	}
	return n.Current()
}

// PopTo removes views from the top until depth views are left and cancels
// their contexts. The root view always stays.
func (n *Navigator) PopTo(depth int) {
	depth = max(1, min(depth, len(n.stack)))
	for i := len(n.stack) - 1; i >= depth; i-- {
		n.cancels[i]()
	}
	clear(n.stack[depth:])
	clear(n.ctxs[depth:])
	clear(n.cancels[depth:])
	n.stack = n.stack[:depth]
	n.ctxs = n.ctxs[:depth]
	n.cancels = n.cancels[:depth]
}

// Close cancels the context of every view. Call it when the app replaces
// the navigator, for example after a profile switch.
func (n *Navigator) Close() {
	for _, cancel := range n.cancels {
		cancel()
	}
}

// Current returns the top of the stack.
func (n *Navigator) Current() View {
	return n.stack[len(n.stack)-1]
}

// Views returns the views on the stack, from the root to the top.
func (n *Navigator) Views() []View {
	return slices.Clone(n.stack)
}

// Breadcrumbs returns the Title() of each view in the stack.
func (n *Navigator) Breadcrumbs() []string {
	crumbs := make([]string, len(n.stack))
	for i, v := range n.stack {
		crumbs[i] = v.Title()
	}
	return crumbs
}

// Depth returns the stack depth.
func (n *Navigator) Depth() int {
	return len(n.stack)
}

// UpdateCurrent sends a message to the current view and replaces it
// on the stack with the returned model. The returned command is tagged
// with the view, so its result is routed back to it.
func (n *Navigator) UpdateCurrent(msg tea.Msg) tea.Cmd {
	return n.update(len(n.stack)-1, msg)
}

package nav

import (
	"reflect"

	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// View is the interface that all navigable views must implement.
type View interface {
	tea.Model
	ID() string               // unique identifier for caching, e.g. "ec2_list"
	Title() string            // human-readable title for breadcrumb display
	KeyMap() []ui.HintBinding // view-specific keybindings for status bar display
	Footer() string           // resource count / context info for footer line
}

// Navigator manages a stack of views with caching.
type Navigator struct {
	stack []View
	cache map[string]View
}

// New creates a Navigator with the given root view.
func New(root View) *Navigator {
	n := &Navigator{
		stack: []View{root},
		cache: map[string]View{root.ID(): root},
	}
	return n
}

// Routed carries the result of a command back to the view that issued it.
//
// The navigator tags every command a view returns (see Tag). The app then
// delivers the result to that exact view instance, even when the user has
// navigated away from it, and drops it when the instance is gone (for example
// after a profile or region switch built a new navigator). Without this, an
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

var teaPkgPath = reflect.TypeOf(tea.BatchMsg(nil)).PkgPath()

// isRuntimeMsg reports whether m is a type from the Bubble Tea package.
// Several of these are unexported and handled only by the runtime.
func isRuntimeMsg(m tea.Msg) bool {
	t := reflect.TypeOf(m)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.PkgPath() == teaPkgPath
}

// Push adds a view to the stack. If a cached version with the same ID exists,
// it is reused. Init is always called — views should be idempotent if already loaded.
func (n *Navigator) Push(v View) tea.Cmd {
	if cached, ok := n.cache[v.ID()]; ok {
		n.stack = append(n.stack, cached)
		return Tag(cached, cached.Init())
	}
	n.cache[v.ID()] = v
	n.stack = append(n.stack, v)
	return Tag(v, v.Init())
}

// InitCurrent runs Init on the current view and tags the returned command.
func (n *Navigator) InitCurrent() tea.Cmd {
	v := n.Current()
	return Tag(v, v.Init())
}

// Contains reports whether v is a live view of this navigator, on the stack
// or in the cache. A view from an earlier navigator is never contained.
func (n *Navigator) Contains(v View) bool {
	cached, ok := n.cache[v.ID()]
	return ok && cached == v
}

// IsCurrent reports whether v is the view on top of the stack.
func (n *Navigator) IsCurrent(v View) bool {
	return n.Current() == v
}

// UpdateView sends msg to the given view instance, on the stack or in the
// cache, and stores the returned model. It returns nil without calling Update
// when v is not a live view of this navigator.
func (n *Navigator) UpdateView(v View, msg tea.Msg) tea.Cmd {
	if !n.Contains(v) {
		return nil
	}
	updated, cmd := v.Update(msg)
	uv, ok := updated.(View)
	if !ok {
		return Tag(v, cmd)
	}
	if uv != v {
		for i := range n.stack {
			if n.stack[i] == v {
				n.stack[i] = uv
			}
		}
		delete(n.cache, v.ID())
	}
	n.cache[uv.ID()] = uv
	return Tag(uv, cmd)
}

// Pop removes the top view and returns to the previous one.
// The popped view stays in the cache. Returns the revealed view and nil cmd.
func (n *Navigator) Pop() (View, tea.Cmd) {
	if len(n.stack) <= 1 {
		return n.stack[0], nil
	}
	n.stack = n.stack[:len(n.stack)-1]
	return n.Current(), nil
}

// Current returns the top of the stack.
func (n *Navigator) Current() View {
	return n.stack[len(n.stack)-1]
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
	current := n.Current()
	updated, cmd := current.Update(msg)
	if v, ok := updated.(View); ok {
		n.stack[len(n.stack)-1] = v
		n.cache[v.ID()] = v
		return Tag(v, cmd)
	}
	return Tag(current, cmd)
}

// ClearCache removes all cached views except the current stack.
// Use this when profile or region changes to force data refresh.
func (n *Navigator) ClearCache() {
	n.cache = make(map[string]View)
	for _, v := range n.stack {
		n.cache[v.ID()] = v
	}
}

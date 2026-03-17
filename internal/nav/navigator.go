package nav

import (
	tea "charm.land/bubbletea/v2"
)

// View is the interface that all navigable views must implement.
type View interface {
	tea.Model
	ID() string    // unique identifier for caching, e.g. "ec2_list"
	Title() string // human-readable title for breadcrumb display
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

// Push adds a view to the stack. If a cached version with the same ID exists,
// it is reused. Returns the Init command for new (non-cached) views.
func (n *Navigator) Push(v View) tea.Cmd {
	if cached, ok := n.cache[v.ID()]; ok {
		n.stack = append(n.stack, cached)
		return nil
	}
	n.cache[v.ID()] = v
	n.stack = append(n.stack, v)
	return v.Init()
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
// on the stack with the returned model.
func (n *Navigator) UpdateCurrent(msg tea.Msg) tea.Cmd {
	current := n.Current()
	updated, cmd := current.Update(msg)
	if v, ok := updated.(View); ok {
		n.stack[len(n.stack)-1] = v
		n.cache[v.ID()] = v
	}
	return cmd
}

// ClearCache removes all cached views except the current stack.
// Use this when profile or region changes to force data refresh.
func (n *Navigator) ClearCache() {
	n.cache = make(map[string]View)
	for _, v := range n.stack {
		n.cache[v.ID()] = v
	}
}

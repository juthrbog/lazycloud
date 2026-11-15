package ui

import tea "charm.land/bubbletea/v2"

// Overlay is a component that the app draws over the views and that gets
// the key presses while it is open. The app keeps the open overlays in a
// stack.
type Overlay interface {
	Visible() bool
	// Update changes the overlay in place.
	Update(tea.Msg) tea.Cmd
	View() string
	SetSize(width, height int)
}

// overlayModel is a value component, such as Confirm, whose Update returns
// a new value. P is the pointer type of the component.
type overlayModel[T any] interface {
	*T
	Visible() bool
	Update(tea.Msg) (T, tea.Cmd)
	View() string
	SetSize(width, height int)
}

// AsOverlay adapts a value component to Overlay. The adapter writes each
// change through p, so p must point to the component that the caller keeps.
// Do not keep the adapter: build a new one each time, so it never points to
// an old copy.
func AsOverlay[T any, P overlayModel[T]](p P) Overlay {
	return valueOverlay[T, P]{p: p}
}

type valueOverlay[T any, P overlayModel[T]] struct{ p P }

func (o valueOverlay[T, P]) Visible() bool             { return o.p.Visible() }
func (o valueOverlay[T, P]) View() string              { return o.p.View() }
func (o valueOverlay[T, P]) SetSize(width, height int) { o.p.SetSize(width, height) }

func (o valueOverlay[T, P]) Update(msg tea.Msg) tea.Cmd {
	next, cmd := o.p.Update(msg)
	*o.p = next
	return cmd
}

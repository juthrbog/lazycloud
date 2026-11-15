package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/juthrbog/lazycloud/internal/eventlog"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// overlayKind names one of the four overlays of the app. The app keeps one
// instance of each kind, as a value field of Model.
type overlayKind uint8

const (
	ovHelp overlayKind = iota
	ovCommandBar
	ovConfirm
	ovPicker
)

var overlayKinds = []overlayKind{ovHelp, ovCommandBar, ovConfirm, ovPicker}

// replyRoute says where the reply of an overlay goes.
type replyRoute struct {
	// replyTo is the view that asked for the prompt. The reply goes to
	// this view, also when another view is on top by then. When replyTo is
	// nil, the reply goes the old way: the app handles it, or sends it to
	// the view on top.
	replyTo nav.View
}

// overlayEntry is an open overlay in the stack.
type overlayEntry struct {
	kind  overlayKind
	route replyRoute
}

// overlayReplyMsg carries the reply of an overlay with its route. The app
// removes the entry when the overlay closes, so the reply must carry the
// route itself.
type overlayReplyMsg struct {
	route replyRoute
	msg   tea.Msg
}

// overlayStack holds the open overlays, bottom first. The top one gets the
// key presses, and the app draws them from bottom to top. push and prune
// make a new slice, so two copies of Model never share one.
type overlayStack []overlayEntry

// push puts an overlay of kind k on top. An overlay that opens again moves
// to the top, so the stack never holds a kind twice.
func (s overlayStack) push(k overlayKind, route replyRoute) overlayStack {
	out := make(overlayStack, 0, len(s)+1)
	for _, e := range s {
		if e.kind != k {
			out = append(out, e)
		}
	}
	return append(out, overlayEntry{kind: k, route: route})
}

// top returns the overlay on top of the stack.
func (s overlayStack) top() (overlayEntry, bool) {
	if len(s) == 0 {
		return overlayEntry{}, false
	}
	return s[len(s)-1], true
}

// overlay adapts the component of kind k. The adapter points into m, so
// build it where it is used and do not keep it.
func (m *Model) overlay(k overlayKind) ui.Overlay {
	switch k {
	case ovHelp:
		return ui.AsOverlay(&m.help)
	case ovCommandBar:
		return ui.AsOverlay(&m.commandBar)
	case ovConfirm:
		return ui.AsOverlay(&m.confirm)
	default:
		return ui.AsOverlay(&m.picker)
	}
}

// openOverlay puts the overlay of kind k on top of the stack. Call it each
// time the app shows an overlay. The reply goes the old way.
func (m *Model) openOverlay(k overlayKind) {
	m.overlays = m.overlays.push(k, replyRoute{})
}

// openOverlayFor puts the overlay of kind k on top of the stack, and its
// reply goes to asker. A nil asker means a request without an origin, and
// its reply goes the old way.
func (m *Model) openOverlayFor(k overlayKind, asker nav.View) {
	m.overlays = m.overlays.push(k, replyRoute{replyTo: asker})
}

// sendOverlayKey gives a key press to the overlay on top. If the key closes
// an overlay that a view asked for, the reply goes out with its route.
func (m Model) sendOverlayKey(top overlayEntry, k tea.KeyPressMsg) (Model, tea.Cmd) {
	o := m.overlay(top.kind)
	cmd := o.Update(k)
	closed := !o.Visible()
	m.pruneOverlays()
	if !closed || top.route.replyTo == nil || cmd == nil {
		return m, cmd
	}
	route := top.route
	return m, func() tea.Msg { return overlayReplyMsg{route: route, msg: cmd()} }
}

// deliverReply sends a reply to the view that asked. If that view is gone,
// the reply is dropped: no other view may take it for its own.
func (m Model) deliverReply(r overlayReplyMsg) (Model, tea.Cmd) {
	if r.msg == nil {
		return m, nil
	}
	if !m.nav.Contains(r.route.replyTo) {
		eventlog.Debugf(eventlog.CatNav, "Dropped %T for %s: view no longer exists", r.msg, r.route.replyTo.ID())
		return m, nil
	}
	return m, m.nav.UpdateView(r.route.replyTo, r.msg)
}

// pruneOverlays removes the overlays that closed.
func (m *Model) pruneOverlays() {
	if !slices.ContainsFunc(m.overlays, func(e overlayEntry) bool { return !m.overlay(e.kind).Visible() }) {
		return
	}
	out := make(overlayStack, 0, len(m.overlays))
	for _, e := range m.overlays {
		if m.overlay(e.kind).Visible() {
			out = append(out, e)
		}
	}
	m.overlays = out
}

// resizeOverlays gives every overlay the terminal size, also the closed
// ones, so each one opens at the right size.
func (m *Model) resizeOverlays() {
	for _, k := range overlayKinds {
		m.overlay(k).SetSize(m.width, m.height)
	}
}

// drawOverlays draws the open overlays over body, from bottom to top. The
// command bar draws its suggestions above the input line. The other
// overlays draw in the middle of the screen.
func (m Model) drawOverlays(body string) string {
	for _, e := range m.overlays {
		view := m.overlay(e.kind).View()
		if view == "" {
			continue
		}
		if e.kind != ovCommandBar {
			body = composeOverlay(body, view, m.width, m.height)
			continue
		}
		y := max(m.height-lipgloss.Height(view)-1, 0)
		body = lipgloss.NewCompositor(
			lipgloss.NewLayer(body).Z(0),
			lipgloss.NewLayer(view).X(1).Y(y).Z(1),
		).Render()
	}
	return body
}

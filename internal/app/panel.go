package app

import (
	tea "charm.land/bubbletea/v2"

	appmsg "github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Side panel layout limits.
const (
	panelMinWidth     = 30
	panelMaxWidth     = 80
	panelMinMainWidth = 30 // minimum width for main view when panel is open

	// historyMax is the most back entries that the panel controller keeps.
	historyMax = 10
)

// panelController holds the side panel and the panels that the app saved
// (docs/design/a6-panel-controller.md). The app calls its methods and does
// not touch the fields. The controller does not hold the navigator. The
// methods that need it take its views. The Model wrappers in app.go resize
// the views after a change.
type panelController struct {
	open    panelState     // the panel on screen; open.panel is nil when none
	stash   *panelState    // the panel that a narrow terminal hides
	history []historyEntry // back traversal, oldest first, at most historyMax

	widthOverride int    // 0 = 40% of the window
	gen           uint64 // counts the panels that opened
}

// panelRouted is the result of a command of the panel whose number is gen.
// The app drops it when that panel is no longer on screen, so a late link
// of a closed or older panel does nothing (H4).
type panelRouted struct {
	gen uint64
	msg tea.Msg
}

// panelState is one panel and the view that showed it. A saved panel keeps
// the panel itself, so a restore brings back its tab, its scroll position,
// its cursor and its focus. gen is the number of the panel, which the
// results of its commands carry.
type panelState struct {
	panel   *ui.TabbedPanel
	title   string
	owner   nav.View
	focused bool
	gen     uint64
}

// historyEntry is a panel that a link left. depth is the navigator depth of
// its owner.
type historyEntry struct {
	state panelState
	depth int
}

// ownerAt reports whether owner is the view at depth in views.
func ownerAt(views []nav.View, depth int, owner nav.View) bool {
	return depth >= 1 && depth <= len(views) && views[depth-1] == owner
}

// IsOpen reports whether a panel is on screen.
func (p *panelController) IsOpen() bool { return p.open.panel != nil }

// Panel returns the panel on screen, or nil.
func (p *panelController) Panel() *ui.TabbedPanel { return p.open.panel }

// Title returns the title of the panel on screen, or "".
func (p *panelController) Title() string { return p.open.title }

// Owner returns the view that showed the panel on screen, or nil.
func (p *panelController) Owner() nav.View { return p.open.owner }

// Focused reports whether the panel on screen has the focus.
func (p *panelController) Focused() bool { return p.open.panel != nil && p.open.focused }

// SetFocus gives the focus to the panel on screen, or takes it away.
func (p *panelController) SetFocus(f bool) { p.open.focused = f && p.open.panel != nil }

// Open shows a new panel for owner. The panel gets the focus when focus is
// true. The app passes false while the view takes text input, because the
// keys go to the view then (H2). Open drops the stash, because the stash
// belongs to an older panel.
func (p *panelController) Open(title string, tabs []appmsg.TabContent, owner nav.View, focus bool) {
	tabInputs := make([]ui.TabInput, len(tabs))
	for i, t := range tabs {
		var links map[int]ui.ContentLink
		if len(t.Links) > 0 {
			links = make(map[int]ui.ContentLink)
			for _, l := range t.Links {
				links[l.Line] = ui.ContentLink{ViewID: l.ViewID, Params: l.Params}
			}
		}
		tabInputs[i] = ui.TabInput{
			Title:   t.Title,
			Content: t.Content,
			Format:  t.Format,
			Links:   links,
		}
	}
	tp := ui.NewTabbedPanel(title, tabInputs)
	p.gen++
	p.open = panelState{panel: &tp, title: title, owner: owner, focused: focus, gen: p.gen}
	p.stash = nil
}

// Update gives a message to the panel on screen. The results of the command
// arrive as panelRouted.
func (p *panelController) Update(msg tea.Msg) tea.Cmd {
	if p.open.panel == nil {
		return nil
	}
	updated, cmd := p.open.panel.Update(msg)
	p.open.panel = &updated
	return p.tag(cmd)
}

// EditorCmd opens the active tab of the panel on screen in $EDITOR.
func (p *panelController) EditorCmd() tea.Cmd {
	if p.open.panel == nil {
		return nil
	}
	return p.tag(p.open.panel.OpenInEditorCmd())
}

// tag wraps cmd so that its results arrive as panelRouted with the number of
// the panel on screen.
func (p *panelController) tag(cmd tea.Cmd) tea.Cmd {
	gen := p.open.gen
	return nav.Wrap(cmd, func(m tea.Msg) tea.Msg {
		if r, ok := m.(panelRouted); ok {
			return r
		}
		return panelRouted{gen: gen, msg: m}
	})
}

// IsCurrent reports whether gen is the number of the panel on screen.
func (p *panelController) IsCurrent(gen uint64) bool {
	return p.open.panel != nil && p.open.gen == gen
}

// Close closes the panel on screen. It keeps the stash and the history.
func (p *panelController) Close() { p.open = panelState{} }

// Dismiss closes the panel on screen and drops the stash.
func (p *panelController) Dismiss() {
	p.stash = nil
	p.Close()
}

// Hide moves the panel on screen to the stash, for a narrow terminal. A
// panel without a title is not saved.
func (p *panelController) Hide() {
	p.stash = nil
	if p.open.panel != nil && p.open.title != "" {
		s := p.open
		p.stash = &s
	}
	p.Close()
}

// HasStash reports whether a narrow terminal hides a panel.
func (p *panelController) HasStash() bool { return p.stash != nil }

// StashTitle returns the title of the hidden panel, or "".
func (p *panelController) StashTitle() string {
	if p.stash == nil {
		return ""
	}
	return p.stash.title
}

// Unhide opens the stash again when its owner is top, and drops the stash.
// A stash of another view is dropped and does not open. The panel keeps its
// focus only when focus is true, as in Open. It reports whether a panel
// opened.
func (p *panelController) Unhide(top nav.View, focus bool) bool {
	if p.open.panel != nil || p.stash == nil {
		return false
	}
	s := *p.stash
	p.stash = nil
	if top == nil || s.owner != top {
		return false
	}
	s.focused = s.focused && focus
	p.open = s
	return true
}

// Push adds the panel on screen to the history, with the depth of its owner.
// A panel without a title is not saved.
func (p *panelController) Push(depth int) {
	if p.open.panel == nil || p.open.title == "" {
		return
	}
	p.history = append(p.history, historyEntry{state: p.open, depth: depth})
	if len(p.history) > historyMax {
		p.history = p.history[1:]
	}
}

// HasHistory reports whether back traversal has an entry.
func (p *panelController) HasHistory() bool { return len(p.history) > 0 }

// BackTitle returns the title of the newest history entry, or "".
func (p *panelController) BackTitle() string {
	if n := len(p.history); n > 0 {
		return p.history[n-1].state.title
	}
	return ""
}

// Back removes history entries from the newest end until it finds one whose
// owner is still at its depth in views. It returns that entry. The entries
// whose owner is gone are dropped.
func (p *panelController) Back(views []nav.View) (historyEntry, bool) {
	for n := len(p.history); n > 0; n = len(p.history) {
		e := p.history[n-1]
		p.history = p.history[:n-1]
		if ownerAt(views, e.depth, e.state.owner) {
			return e, true
		}
	}
	return historyEntry{}, false
}

// Restore opens a saved panel and drops the stash. The panel keeps its focus
// only when focus is true, as in Open.
func (p *panelController) Restore(s panelState, focus bool) {
	s.focused = s.focused && focus
	p.open = s
	p.stash = nil
}

// StashState puts a saved panel into the stash. Back traversal uses it when
// the terminal is too narrow for the panel.
func (p *panelController) StashState(s panelState) { p.stash = &s }

// Trim drops each history entry whose owner is not at its depth in views,
// and each entry whose depth is not below the top. So q or esc in the view
// that a link opened drops the entry of the link (decision D7).
func (p *panelController) Trim(views []nav.View) {
	n := 0
	for _, e := range p.history {
		if e.depth < len(views) && ownerAt(views, e.depth, e.state.owner) {
			p.history[n] = e
			n++
		}
	}
	clear(p.history[n:])
	p.history = p.history[:n]
}

// Reset drops the panel, the stash and the history. It keeps the width.
func (p *panelController) Reset() {
	p.Dismiss()
	p.history = nil
}

// Width returns the width of the panel for a terminal of termWidth columns.
func (p *panelController) Width(termWidth int) int {
	if p.widthOverride > 0 {
		maxW := termWidth - panelMinMainWidth - 3 // gap + main borders
		return min(max(p.widthOverride, panelMinWidth), maxW)
	}
	return min(max(termWidth*40/100, panelMinWidth), panelMaxWidth)
}

// SetWidth sets the width override. 0 goes back to 40% of the window.
func (p *panelController) SetWidth(w int) { p.widthOverride = w }

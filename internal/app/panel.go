package app

import (
	tea "charm.land/bubbletea/v2"

	appmsg "github.com/juthrbog/lazycloud/internal/msg"
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
// not touch the fields. The controller does not know the navigator or the
// views. The Model wrappers in app.go resize the views after a change.
type panelController struct {
	panel   *ui.TabbedPanel // the panel on screen; nil when none
	title   string
	focused bool

	stash   *panelSnapshot // the panel that a narrow terminal hides
	history []historyEntry // back traversal, oldest first, at most historyMax

	widthOverride int // 0 = 40% of the window
}

// panelSnapshot is a copy of the title and the tabs of a panel.
type panelSnapshot struct {
	title string
	tabs  []appmsg.TabContent
}

// historyEntry is a panel that a link left. depth is the navigator depth of
// the view that showed the panel.
type historyEntry struct {
	snap  panelSnapshot
	depth int
}

// IsOpen reports whether a panel is on screen.
func (p *panelController) IsOpen() bool { return p.panel != nil }

// Panel returns the panel on screen, or nil.
func (p *panelController) Panel() *ui.TabbedPanel { return p.panel }

// Title returns the title of the panel on screen, or "".
func (p *panelController) Title() string { return p.title }

// Focused reports whether the panel on screen has the focus.
func (p *panelController) Focused() bool { return p.panel != nil && p.focused }

// SetFocus gives the focus to the panel on screen, or takes it away.
func (p *panelController) SetFocus(f bool) { p.focused = f && p.panel != nil }

// Open shows a new panel with the focus.
func (p *panelController) Open(title string, tabs []appmsg.TabContent) {
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
	p.panel = &tp
	p.focused = true
	p.title = title
}

// Update gives a message to the panel on screen.
func (p *panelController) Update(msg tea.Msg) tea.Cmd {
	if p.panel == nil {
		return nil
	}
	updated, cmd := p.panel.Update(msg)
	p.panel = &updated
	return cmd
}

// Close closes the panel on screen. It keeps the stash and the history.
func (p *panelController) Close() {
	p.panel = nil
	p.focused = false
	p.title = ""
}

// Dismiss closes the panel on screen and drops the stash.
func (p *panelController) Dismiss() {
	p.stash = nil
	p.Close()
}

// snapshot copies the title and the tabs of the panel on screen.
func (p *panelController) snapshot() (panelSnapshot, bool) {
	if p.panel == nil {
		return panelSnapshot{}, false
	}
	tabData := p.panel.TabData()
	tabs := make([]appmsg.TabContent, len(tabData))
	for i, t := range tabData {
		var links []appmsg.TabLink
		for line, link := range t.Links {
			links = append(links, appmsg.TabLink{
				Line:   line,
				ViewID: link.ViewID,
				Params: link.Params,
			})
		}
		tabs[i] = appmsg.TabContent{
			Title:   t.Title,
			Content: t.Content,
			Format:  t.Format,
			Links:   links,
		}
	}
	return panelSnapshot{title: p.title, tabs: tabs}, true
}

// Hide moves the panel on screen to the stash, for a narrow terminal.
func (p *panelController) Hide() {
	if snap, ok := p.snapshot(); ok {
		p.stash = &snap
	} else {
		p.stash = nil
	}
	p.Close()
}

// HasStash reports whether a narrow terminal hides a panel.
func (p *panelController) HasStash() bool { return p.stash != nil && p.stash.title != "" }

// StashTitle returns the title of the hidden panel, or "".
func (p *panelController) StashTitle() string {
	if p.stash == nil {
		return ""
	}
	return p.stash.title
}

// Unhide opens the stash again and drops it. It reports whether a panel
// opened.
func (p *panelController) Unhide() bool {
	if p.panel != nil || !p.HasStash() {
		return false
	}
	snap := *p.stash
	p.stash = nil
	p.Open(snap.title, snap.tabs)
	return true
}

// Push adds the panel on screen to the history, with the depth of the view
// that shows it.
func (p *panelController) Push(depth int) {
	snap, ok := p.snapshot()
	if !ok || snap.title == "" {
		return
	}
	p.history = append(p.history, historyEntry{snap: snap, depth: depth})
	if len(p.history) > historyMax {
		p.history = p.history[1:]
	}
}

// HasHistory reports whether back traversal has an entry.
func (p *panelController) HasHistory() bool { return len(p.history) > 0 }

// BackTitle returns the title of the newest history entry, or "".
func (p *panelController) BackTitle() string {
	if n := len(p.history); n > 0 {
		return p.history[n-1].snap.title
	}
	return ""
}

// PopHistory removes the newest history entry and returns it.
func (p *panelController) PopHistory() (historyEntry, bool) {
	n := len(p.history)
	if n == 0 {
		return historyEntry{}, false
	}
	e := p.history[n-1]
	p.history = p.history[:n-1]
	return e, true
}

// StashSnapshot puts a saved panel into the stash. Back traversal uses it
// when the terminal is too narrow for the panel.
func (p *panelController) StashSnapshot(snap panelSnapshot) { p.stash = &snap }

// Trim drops each history entry whose depth is not below depth, the depth of
// the navigator.
func (p *panelController) Trim(depth int) {
	n := 0
	for _, e := range p.history {
		if e.depth < depth {
			p.history[n] = e
			n++
		}
	}
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

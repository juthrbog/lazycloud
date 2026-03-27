package ui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// tabEntry holds a single tab's data and its lazily-created viewer.
type tabEntry struct {
	title   string
	content string
	format  ContentFormat
	links   map[int]ContentLink // navigable lines
	viewer  *ContentView        // nil until first accessed
}

// TabbedPanel displays multiple content tabs with a tab bar and lazy viewer creation.
type TabbedPanel struct {
	tabs   []tabEntry
	active int
	title  string
	width  int
	height int
}

// TabInput describes the data for one tab, passed to NewTabbedPanel.
type TabInput struct {
	Title   string
	Content string
	Format  string
	Links   map[int]ContentLink // optional: navigable lines
}

// NewTabbedPanel creates a tabbed panel. The first tab's viewer is created eagerly.
func NewTabbedPanel(title string, tabs []TabInput) TabbedPanel {
	entries := make([]tabEntry, len(tabs))
	for i, t := range tabs {
		format := ContentFormat(t.Format)
		if format == "" {
			format = FormatAuto
		}
		entries[i] = tabEntry{
			title:   t.Title,
			content: t.Content,
			format:  format,
			links:   t.Links,
		}
	}

	tp := TabbedPanel{
		tabs:  entries,
		title: title,
	}

	// Eagerly create the first tab's viewer
	if len(entries) > 0 {
		tp.ensureViewer(0)
	}

	return tp
}

// ensureViewer lazily creates the ContentView for the given tab index.
func (tp *TabbedPanel) ensureViewer(idx int) {
	if idx < 0 || idx >= len(tp.tabs) {
		return
	}
	if tp.tabs[idx].viewer != nil {
		return
	}
	tab := tp.tabs[idx]
	cv := NewContentView(tab.title, tab.content, tab.format)
	cv.SetEmbedded(true)
	if len(tab.links) > 0 {
		cv.SetLinks(tab.links)
	}
	if tp.width > 0 && tp.height > 0 {
		cv.SetSize(tp.width, tp.viewerHeight())
	}
	tp.tabs[idx].viewer = &cv
}

// tabBarHeight returns the rendered height of the tab bar, accounting for
// possible line wrapping at narrow panel widths.
func (tp TabbedPanel) tabBarHeight() int {
	if len(tp.tabs) == 0 {
		return 0
	}
	bar := tp.renderTabBar()
	if tp.width > 0 {
		bar = lipgloss.NewStyle().Width(tp.width).Render(bar)
	}
	h := lipgloss.Height(bar)
	if h < 1 {
		h = 1
	}
	return h
}

// viewerHeight returns the height available for the content viewer (minus tab bar).
func (tp TabbedPanel) viewerHeight() int {
	h := tp.height - tp.tabBarHeight()
	if h < 1 {
		h = 1
	}
	return h
}

// TabCount returns the number of tabs.
func (tp TabbedPanel) TabCount() int {
	return len(tp.tabs)
}

// ActiveTab returns the 0-indexed active tab.
func (tp TabbedPanel) ActiveTab() int {
	return tp.active
}

// SetSize sets the panel dimensions and resizes the active viewer.
func (tp *TabbedPanel) SetSize(w, h int) {
	tp.width = w
	tp.height = h
	if tp.active < len(tp.tabs) && tp.tabs[tp.active].viewer != nil {
		tp.tabs[tp.active].viewer.SetSize(w, tp.viewerHeight())
	}
}

// OpenInEditorCmd delegates to the active tab's viewer.
func (tp TabbedPanel) OpenInEditorCmd() tea.Cmd {
	if tp.active < len(tp.tabs) && tp.tabs[tp.active].viewer != nil {
		return tp.tabs[tp.active].viewer.OpenInEditorCmd()
	}
	return nil
}

// Update handles input. Number keys 1-9 switch tabs; all else forwarded to active viewer.
func (tp TabbedPanel) Update(msg tea.Msg) (TabbedPanel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		key := msg.String()

		// Number keys switch tabs
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			idx, _ := strconv.Atoi(key)
			idx-- // 1-indexed → 0-indexed
			if idx < len(tp.tabs) && idx != tp.active {
				tp.active = idx
				tp.ensureViewer(idx)
				return tp, nil
			}
			return tp, nil
		}
	}

	// Forward to active viewer
	if tp.active < len(tp.tabs) && tp.tabs[tp.active].viewer != nil {
		updated, cmd := tp.tabs[tp.active].viewer.Update(msg)
		tp.tabs[tp.active].viewer = &updated
		return tp, cmd
	}
	return tp, nil
}

// View renders the tab bar and active viewer content.
func (tp TabbedPanel) View() string {
	if len(tp.tabs) == 0 {
		return ""
	}

	tabBar := tp.renderTabBar()
	if tp.width > 0 {
		tabBar = lipgloss.NewStyle().Width(tp.width).Render(tabBar)
	}

	var content string
	if tp.active < len(tp.tabs) && tp.tabs[tp.active].viewer != nil {
		content = tp.tabs[tp.active].viewer.View()
	}

	return tabBar + "\n" + content
}

// renderTabBar renders the horizontal tab strip.
func (tp TabbedPanel) renderTabBar() string {
	s := S

	var parts []string
	for i, tab := range tp.tabs {
		label := strconv.Itoa(i+1) + ":" + tab.title
		if i == tp.active {
			parts = append(parts, s.TabActive.Render(label))
		} else {
			parts = append(parts, s.TabInactive.Render(label))
		}
	}

	return strings.Join(parts, "  ")
}

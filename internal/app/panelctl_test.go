package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
)

func ctlTabs(title string) []msg.TabContent {
	return []msg.TabContent{
		{Title: "Info", Content: "info for " + title, Format: "text"},
		{Title: "Links", Content: "x", Format: "text",
			Links: []msg.TabLink{{Line: 0, ViewID: "eventlog", Params: map[string]string{"focus": "a"}}}},
	}
}

func TestPanelControllerOpenAndClose(t *testing.T) {
	var p panelController
	assert.False(t, p.IsOpen())
	assert.False(t, p.Focused())
	assert.Nil(t, p.Panel())

	p.Open("a", ctlTabs("a"))
	assert.True(t, p.IsOpen())
	assert.True(t, p.Focused(), "a new panel has the focus")
	assert.Equal(t, "a", p.Title())
	assert.Equal(t, 2, p.Panel().TabCount())

	p.SetFocus(false)
	assert.False(t, p.Focused())
	p.SetFocus(true)
	assert.True(t, p.Focused())

	p.Close()
	assert.False(t, p.IsOpen())
	assert.False(t, p.Focused())
	assert.Empty(t, p.Title())

	p.SetFocus(true)
	assert.False(t, p.Focused(), "a closed panel cannot get the focus")
}

func TestPanelControllerHideAndUnhide(t *testing.T) {
	var p panelController
	assert.False(t, p.Unhide(), "no stash")

	p.Open("a", ctlTabs("a"))
	p.Hide()
	assert.False(t, p.IsOpen())
	assert.True(t, p.HasStash())
	assert.Equal(t, "a", p.StashTitle())

	require.True(t, p.Unhide())
	assert.True(t, p.IsOpen())
	assert.Equal(t, "a", p.Title())
	assert.False(t, p.HasStash())
	links := p.Panel().TabData()[1].Links
	assert.Equal(t, "eventlog", links[0].ViewID, "the stash keeps the links")
	assert.Equal(t, "a", links[0].Params["focus"])

	p.Hide()
	p.Open("b", ctlTabs("b"))
	assert.False(t, p.Unhide(), "a panel is open")
	assert.Equal(t, "a", p.StashTitle(), "Open keeps the stash, as openTabbedPanel did")

	p.Dismiss()
	assert.False(t, p.IsOpen())
	assert.False(t, p.HasStash())
}

func TestPanelControllerHideWithoutATitle(t *testing.T) {
	var p panelController
	p.Open("", ctlTabs(""))
	p.Hide()
	assert.False(t, p.HasStash(), "a panel without a title is not restored")
	assert.False(t, p.Unhide())
}

func TestPanelControllerHistory(t *testing.T) {
	var p panelController
	p.Push(2)
	assert.False(t, p.HasHistory(), "no panel, no entry")

	p.Open("", ctlTabs(""))
	p.Push(2)
	assert.False(t, p.HasHistory(), "a panel without a title adds no entry")

	p.Open("a", ctlTabs("a"))
	p.Push(2)
	p.Open("b", ctlTabs("b"))
	p.Push(3)
	assert.Equal(t, "b", p.BackTitle())

	e, ok := p.PopHistory()
	require.True(t, ok)
	assert.Equal(t, 3, e.depth)
	assert.Equal(t, "b", e.snap.title)
	assert.Len(t, e.snap.tabs, 2)
	assert.Equal(t, "a", p.BackTitle())

	p.Open("c", ctlTabs("c"))
	p.Push(4)
	p.Trim(3)
	require.Len(t, p.history, 1)
	assert.Equal(t, "a", p.BackTitle(), "Trim keeps entries below the depth")
	p.Trim(2)
	assert.False(t, p.HasHistory())
	assert.Empty(t, p.BackTitle())

	_, ok = p.PopHistory()
	assert.False(t, ok)
}

func TestPanelControllerResetKeepsTheWidth(t *testing.T) {
	var p panelController
	p.SetWidth(50)
	p.Open("a", ctlTabs("a"))
	p.Push(2)
	p.Hide()
	p.Open("b", ctlTabs("b"))
	require.True(t, p.IsOpen())
	require.True(t, p.HasStash())

	p.Reset()

	assert.False(t, p.IsOpen())
	assert.False(t, p.HasStash())
	assert.False(t, p.HasHistory())
	assert.Equal(t, 50, p.Width(200))
}

// A view gets the width next to the panel while the panel is open, and the
// full width again when the panel opens, closes or hides.
func TestViewWidthFollowsThePanel(t *testing.T) {
	m := newTestModel(panelWide, 40)
	v := newRowView("")
	m.pushView(v)
	full := panelWide - 2
	lastW := func() int { return v.sizes[len(v.sizes)-1].Width }
	require.Equal(t, full, lastW())

	m.openPanel("a", "{}", "json")
	assert.Equal(t, panelWide-m.panelWidth()-3, lastW(), "open")

	m, _ = update(t, m, press("q"))
	require.False(t, m.panels.IsOpen())
	assert.Equal(t, full, lastW(), "q closes the panel")

	m.openPanel("a", "{}", "json")
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})
	assert.Equal(t, panelNarrow-2, lastW(), "a narrow terminal hides the panel")
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})
	assert.Equal(t, panelWide-m.panelWidth()-3, lastW(), "the panel is back")
	m.ops.CancelAll()
}

// The panel keys < and > change the width by panelResizeStep, and = goes back
// to the default width.
func TestPanelResizeKeys(t *testing.T) {
	m := panelOnEC2(t)
	def := m.panelWidth()

	m, _ = update(t, m, press("<"))
	assert.Equal(t, def+panelResizeStep, m.panelWidth(), "< grows the panel")
	m, _ = update(t, m, press(">"))
	m, _ = update(t, m, press(">"))
	assert.Equal(t, def-panelResizeStep, m.panelWidth(), "> shrinks the panel")
	m, _ = update(t, m, press("="))
	assert.Equal(t, def, m.panelWidth(), "= resets the width")
}

func TestPanelControllerWidth(t *testing.T) {
	var p panelController
	assert.Equal(t, panelMinWidth, p.Width(50), "the default has a minimum")
	assert.Equal(t, 56, p.Width(140), "the default is 40 percent")
	assert.Equal(t, panelMaxWidth, p.Width(300), "the default has a maximum")

	p.SetWidth(10)
	assert.Equal(t, panelMinWidth, p.Width(140))
	p.SetWidth(500)
	assert.Equal(t, 140-panelMinMainWidth-3, p.Width(140), "the main view keeps its minimum")
	p.SetWidth(0)
	assert.Equal(t, 56, p.Width(140))
}

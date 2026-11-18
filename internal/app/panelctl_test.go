package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func ctlTabs(title string) []msg.TabContent {
	return []msg.TabContent{
		{Title: "Info", Content: "info for " + title, Format: "text"},
		{Title: "Links", Content: "x", Format: "text",
			Links: []msg.TabLink{{Line: 0, ViewID: "eventlog", Params: map[string]string{"focus": "a"}}}},
	}
}

// ctlViews gives n distinct views, as a navigator stack from the root.
func ctlViews(n int) []nav.View {
	vs := make([]nav.View, n)
	for i := range vs {
		vs[i] = newRowView("")
	}
	return vs
}

func TestPanelControllerOpenAndClose(t *testing.T) {
	var p panelController
	owner := newRowView("")
	assert.False(t, p.IsOpen())
	assert.False(t, p.Focused())
	assert.Nil(t, p.Panel())
	assert.Nil(t, p.Owner())

	p.Open("a", ctlTabs("a"), owner, true)
	assert.True(t, p.IsOpen())
	assert.True(t, p.Focused(), "a new panel has the focus")
	assert.Equal(t, "a", p.Title())
	assert.Equal(t, owner, p.Owner())
	assert.Equal(t, 2, p.Panel().TabCount())

	p.SetFocus(false)
	assert.False(t, p.Focused())
	p.SetFocus(true)
	assert.True(t, p.Focused())

	p.Close()
	assert.False(t, p.IsOpen())
	assert.False(t, p.Focused())
	assert.Empty(t, p.Title())
	assert.Nil(t, p.Owner())

	p.SetFocus(true)
	assert.False(t, p.Focused(), "a closed panel cannot get the focus")
}

func TestPanelControllerHideAndUnhide(t *testing.T) {
	var p panelController
	vs := ctlViews(2)
	assert.False(t, p.Unhide(vs[1], true), "no stash")

	p.Open("a", ctlTabs("a"), vs[1], true)
	p.Update(press("2"))
	p.SetFocus(false)
	panel := p.Panel()
	p.Hide()
	assert.False(t, p.IsOpen())
	assert.True(t, p.HasStash())
	assert.Equal(t, "a", p.StashTitle())

	require.True(t, p.Unhide(vs[1], true))
	assert.Same(t, panel, p.Panel(), "the stash keeps the panel itself")
	assert.Equal(t, 1, p.Panel().ActiveTab())
	assert.False(t, p.Focused(), "the stash keeps the focus")
	assert.Equal(t, vs[1], p.Owner())
	assert.False(t, p.HasStash())

	p.Hide()
	assert.False(t, p.Unhide(vs[0], true), "the stash opens only in its owner")
	assert.False(t, p.HasStash(), "a stash of another view is dropped")
	assert.False(t, p.IsOpen())

	p.Open("a", ctlTabs("a"), vs[1], true)
	p.Hide()
	assert.False(t, p.Unhide(nil, true), "no top view")
	assert.False(t, p.HasStash())

	p.StashState(panelState{title: "x"})
	assert.False(t, p.Unhide(nil, true), "a stash without an owner never opens")

	p.Open("a", ctlTabs("a"), vs[1], true)
	p.Hide()
	p.Open("b", ctlTabs("b"), vs[1], true)
	assert.False(t, p.HasStash(), "Open drops the stash of an older panel")
	p.StashState(panelState{title: "old", owner: vs[1]})
	assert.False(t, p.Unhide(vs[1], true), "a panel is open")
	assert.Equal(t, "b", p.Title(), "the open panel stays")

	p.Hide()
	p.Dismiss()
	assert.False(t, p.IsOpen())
	assert.False(t, p.HasStash())
}

func TestPanelControllerHideWithoutATitle(t *testing.T) {
	var p panelController
	owner := newRowView("")
	p.Open("", ctlTabs(""), owner, true)
	p.Hide()
	assert.False(t, p.HasStash(), "a panel without a title is not restored")
	assert.False(t, p.Unhide(owner, true))
}

func TestPanelControllerHistory(t *testing.T) {
	var p panelController
	vs := ctlViews(4)
	p.Push(2)
	assert.False(t, p.HasHistory(), "no panel, no entry")

	p.Open("", ctlTabs(""), vs[1], true)
	p.Push(2)
	assert.False(t, p.HasHistory(), "a panel without a title adds no entry")

	p.Open("a", ctlTabs("a"), vs[1], true)
	p.Push(2)
	p.Open("b", ctlTabs("b"), vs[2], true)
	p.Update(press("2"))
	b := p.Panel()
	p.Push(3)
	assert.Equal(t, "b", p.BackTitle())

	e, ok := p.Back(vs)
	require.True(t, ok)
	assert.Equal(t, 3, e.depth)
	assert.Equal(t, "b", e.state.title)
	assert.Same(t, b, e.state.panel, "the entry keeps the panel itself")
	assert.Equal(t, 1, e.state.panel.ActiveTab())
	assert.Equal(t, "a", p.BackTitle())

	p.Open("c", ctlTabs("c"), vs[3], true)
	p.Push(4)
	p.Trim(vs[:3])
	require.Len(t, p.history, 1)
	assert.Equal(t, "a", p.BackTitle(), "Trim keeps entries below the top whose owner is at its depth")
	p.Trim(vs[:2])
	assert.False(t, p.HasHistory(), "Trim drops the entry of the view on top")
	assert.Empty(t, p.BackTitle())

	_, ok = p.Back(vs)
	assert.False(t, ok)
}

// An entry whose owner is gone from its depth is skipped and dropped, by Back
// and by Trim.
func TestPanelControllerHistoryChecksTheOwner(t *testing.T) {
	var p panelController
	vs := ctlViews(3)
	p.Open("a", ctlTabs("a"), vs[1], true)
	p.Push(2)
	p.Open("b", ctlTabs("b"), vs[2], true)
	p.Push(3)

	other := []nav.View{vs[0], vs[1], newRowView("")}
	e, ok := p.Back(other)
	require.True(t, ok)
	assert.Equal(t, "a", e.state.title, "the entry of the replaced view is skipped")
	assert.False(t, p.HasHistory())

	p.Open("a", ctlTabs("a"), vs[1], true)
	p.Push(2)
	_, ok = p.Back([]nav.View{vs[0], newRowView("")})
	assert.False(t, ok, "another view at the depth")
	assert.False(t, p.HasHistory())

	p.Open("a", ctlTabs("a"), vs[1], true)
	p.Push(2)
	p.Trim([]nav.View{vs[0], newRowView(""), newRowView("")})
	assert.False(t, p.HasHistory(), "Trim drops an entry whose owner was replaced")

	p.Open("a", ctlTabs("a"), nil, true)
	p.Push(1)
	_, ok = p.Back(vs)
	assert.False(t, ok, "an entry without an owner never opens")
}

func TestPanelControllerRestoreAndStashState(t *testing.T) {
	var p panelController
	vs := ctlViews(2)
	p.Open("a", ctlTabs("a"), vs[1], true)
	p.Push(2)
	e, ok := p.Back(vs)
	require.True(t, ok)
	p.Close()

	p.StashState(e.state)
	assert.Equal(t, "a", p.StashTitle())
	require.True(t, p.Unhide(vs[1], true))
	assert.Same(t, e.state.panel, p.Panel())

	p.Hide()
	p.Restore(e.state, true)
	assert.True(t, p.IsOpen())
	assert.False(t, p.HasStash(), "Restore drops the stash")
}

func TestPanelControllerResetKeepsTheWidth(t *testing.T) {
	var p panelController
	vs := ctlViews(2)
	p.SetWidth(50)
	p.Open("a", ctlTabs("a"), vs[1], true)
	p.Push(2)
	p.Hide()
	p.StashState(panelState{title: "old", owner: vs[1]})
	p.Open("b", ctlTabs("b"), vs[1], true)
	p.StashState(panelState{title: "old", owner: vs[1]})
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

// A panel that opens or comes back while the view takes input has no focus
// (H2). A saved panel without the focus stays without it.
func TestPanelControllerFocusArgument(t *testing.T) {
	var p panelController
	vs := ctlViews(2)
	p.Open("a", ctlTabs("a"), vs[1], false)
	assert.True(t, p.IsOpen())
	assert.False(t, p.Focused(), "Open without the focus")

	p.SetFocus(true)
	p.Hide()
	require.True(t, p.Unhide(vs[1], false))
	assert.False(t, p.Focused(), "Unhide without the focus")
	p.SetFocus(true)
	p.Hide()
	require.True(t, p.Unhide(vs[1], true))
	assert.True(t, p.Focused(), "Unhide keeps the focus")

	p.Push(2)
	e, ok := p.Back(vs)
	require.True(t, ok)
	p.Restore(e.state, false)
	assert.False(t, p.Focused(), "Restore without the focus")
	p.Restore(e.state, true)
	assert.True(t, p.Focused(), "Restore keeps the focus")

	p.SetFocus(false)
	p.Push(2)
	e, _ = p.Back(vs)
	p.Restore(e.state, true)
	assert.False(t, p.Focused(), "a saved panel without the focus stays without it")
}

// Each panel gets a new number. A saved panel keeps its number, and the
// results of the commands of the panel carry it (H4).
func TestPanelControllerGen(t *testing.T) {
	var p panelController
	vs := ctlViews(2)
	assert.False(t, p.IsCurrent(0), "no panel")
	assert.Nil(t, p.EditorCmd())
	assert.Nil(t, p.Update(press("j")))

	p.Open("a", ctlTabs("a"), vs[1], true)
	a := p.open.gen
	assert.True(t, p.IsCurrent(a))
	p.Open("b", ctlTabs("b"), vs[1], true)
	b := p.open.gen
	assert.NotEqual(t, a, b)
	assert.False(t, p.IsCurrent(a), "an older panel")
	assert.True(t, p.IsCurrent(b))

	p.Hide()
	assert.False(t, p.IsCurrent(b), "a hidden panel")
	require.True(t, p.Unhide(vs[1], true))
	assert.True(t, p.IsCurrent(b), "the stash keeps the number")

	// Line 0 of the second tab holds a link. enter follows it.
	p.Update(press("2"))
	cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	got := cmd()
	r, ok := got.(panelRouted)
	require.True(t, ok, "got %T", got)
	assert.Equal(t, b, r.gen)
	assert.IsType(t, ui.ContentLinkActivatedMsg{}, r.msg)

	// A yank gives a batch: the clipboard write goes to the runtime, and
	// the YankedMsg carries the number.
	p.Update(press("V"))
	cmd = p.Update(press("y"))
	require.NotNil(t, cmd)
	batch, ok := cmd().(tea.BatchMsg)
	require.True(t, ok)
	var routed int
	for _, c := range batch {
		if r, ok := c().(panelRouted); ok {
			routed++
			assert.Equal(t, b, r.gen)
			assert.IsType(t, ui.YankedMsg{}, r.msg)
		}
	}
	assert.Equal(t, 1, routed)

	// The editor command fails without a temp directory. Its error also
	// carries the number.
	t.Setenv("TMPDIR", t.TempDir()+"/missing")
	cmd = p.EditorCmd()
	require.NotNil(t, cmd)
	r, ok = cmd().(panelRouted)
	require.True(t, ok)
	assert.Equal(t, b, r.gen)
	assert.IsType(t, ui.EditorFinishedMsg{}, r.msg)

	inner := panelRouted{gen: a, msg: "x"}
	assert.Equal(t, inner, p.tag(func() tea.Msg { return inner })(), "a routed result keeps its number")
}

// The editor key of a focused panel returns a command whose result carries
// the number of the panel.
func TestPanelEditorKeyIsRouted(t *testing.T) {
	m := panelOnEC2(t)
	require.True(t, m.panels.Focused())
	t.Setenv("TMPDIR", t.TempDir()+"/missing")

	_, cmd := update(t, m, press("e"))

	require.NotNil(t, cmd)
	r, ok := cmd().(panelRouted)
	require.True(t, ok)
	assert.True(t, m.panels.IsCurrent(r.gen))
	assert.IsType(t, ui.EditorFinishedMsg{}, r.msg)
}

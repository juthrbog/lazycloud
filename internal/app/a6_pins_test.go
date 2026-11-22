package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/aws/awstest"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// The tests in this file pin the side panel through A6
// (docs/design/a6-panel-controller.md). PR 0 pinned each hazard as a test
// named TestA6Hazard_.... The PR that fixed a hazard flipped its test and
// renamed it TestA6_<hazard>_<new behavior>. A6 fixed all of them. A test
// named TestA6Keep_... pins behavior that A6 keeps.
//
// The helpers below are the only code here that reads the panel controller
// (m.panels). A later PR that changes the controller changes only these
// helpers.

func panelShown(m Model) bool     { return m.panels.IsOpen() }
func panelTitleOf(m Model) string { return m.panels.Title() }
func panelHasFocus(m Model) bool  { return m.panels.Focused() }
func panelTabOf(m Model) int      { return m.panels.Panel().ActiveTab() }
func stashTitleOf(m Model) string { return m.panels.StashTitle() }
func historyLenOf(m Model) int    { return len(m.panels.history) }
func panelViewOf(m Model) string  { return m.panels.Panel().View() }
func fromPanel(m Model, inner tea.Msg) tea.Msg {
	return panelRouted{gen: m.panels.open.gen, msg: inner}
}
func openA6Panel(m *Model, title string) { m.openTabbedPanel(title, a6Tabs(title)) }

// a6Tabs gives two tabs. The second tab has a link to the event log on its
// first line.
func a6Tabs(title string) []msg.TabContent {
	return []msg.TabContent{
		{Title: "Info", Content: "info for " + title + "\nline 2", Format: "text"},
		{Title: "Links", Content: "event log\nline 2", Format: "text",
			Links: []msg.TabLink{{Line: 0, ViewID: "eventlog"}}},
	}
}

// followLink sends a link as the panel on screen sends it. The link
// navigates at once.
func followLink(t *testing.T, m Model, viewID string, params map[string]string) Model {
	t.Helper()
	require.True(t, panelShown(m), "a link comes from an open panel")
	m, _ = update(t, m, fromPanel(m, ui.ContentLinkActivatedMsg{ViewID: viewID, Params: params}))
	return m
}

func a6MaxWidth(s string) int {
	w := 0
	for l := range strings.SplitSeq(s, "\n") {
		w = max(w, lipgloss.Width(l))
	}
	return w
}

// sqsWithDLQPanel gives the SQS queues view with a panel whose second tab
// links to a row of the same view, as the "DLQ Source Queues" tab does.
func sqsWithDLQPanel(t *testing.T) Model {
	t.Helper()
	m := newTestModel(panelWide, 40)
	m.svc.SQS = new(awstest.MockSQSService)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "sqs_queues"})
	m.openTabbedPanel("orders-dlq", []msg.TabContent{
		{Title: "Info", Content: "a\n", Format: "text"},
		{Title: "DLQ", Content: "orders\n", Format: "text",
			Links: []msg.TabLink{{Line: 0, ViewID: "sqs_queues", Params: map[string]string{"focus": "orders"}}}},
	})
	return m
}

func sameViewLink(t *testing.T, m Model) Model {
	t.Helper()
	return followLink(t, m, "sqs_queues", map[string]string{"focus": "orders"})
}

func openA6Form(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.RequestFormMsg{
		ID: "a6", Title: "A6", Fields: []msg.FormField{{Key: "n", Title: "N"}},
	}})
	require.Contains(t, m.nav.Current().ID(), "form:")
	return m
}

// --- Hazards ---

// H1. Fixed in PR 3: after a narrow and then wide resize, the panel comes
// back on the same tab and with the same focus.
func TestA6_H1_StashKeepsTabAndFocus(t *testing.T) {
	m := panelOnEC2(t)
	openA6Panel(&m, "i-1")
	m, _ = update(t, m, press("2"))
	m, _ = update(t, m, keyTab)
	require.Equal(t, 1, panelTabOf(m))
	require.False(t, panelHasFocus(m))

	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})

	require.True(t, panelShown(m))
	assert.Equal(t, 1, panelTabOf(m), "the stash keeps the tab")
	assert.False(t, panelHasFocus(m), "the stash keeps the focus")
}

// H1. Fixed in PR 3: back traversal opens the panel on the tab with the link,
// with the focus that it had.
func TestA6_H1_BackKeepsTheTabOfTheLink(t *testing.T) {
	m := panelOnEC2(t)
	openA6Panel(&m, "i-1")
	m, _ = update(t, m, press("2"))
	require.Equal(t, 1, panelTabOf(m))
	require.True(t, panelHasFocus(m))
	m = followLink(t, m, "eventlog", nil)
	require.Equal(t, "eventlog", m.nav.Current().ID())

	m, _ = update(t, m, keyBackspace)

	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	require.True(t, panelShown(m))
	assert.Equal(t, "i-1", panelTitleOf(m))
	assert.Equal(t, 1, panelTabOf(m), "back keeps the tab")
	assert.True(t, panelHasFocus(m), "back keeps the focus")
}

// a6LongPanel opens a panel with 100 rows and scrolls it to the bottom.
func a6LongPanel(t *testing.T, m Model) Model {
	t.Helper()
	var rows []string
	for i := range 100 {
		rows = append(rows, "row "+string(rune('A'+i/26))+string(rune('a'+i%26)))
	}
	m.openTabbedPanel("long", []msg.TabContent{{Title: "Info", Content: strings.Join(rows, "\n"), Format: "text"}})
	m, _ = update(t, m, press("G"))
	require.NotContains(t, panelViewOf(m), "row Aa")
	require.Contains(t, panelViewOf(m), "row Dv")
	return m
}

// H1. Fixed in PR 3: after a narrow and then wide resize, and after back
// traversal, the panel keeps its scroll position.
func TestA6_H1_RestoreKeepsTheScrollPosition(t *testing.T) {
	m := a6LongPanel(t, panelOnEC2(t))
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})
	require.True(t, panelShown(m))
	assert.NotContains(t, panelViewOf(m), "row Aa", "the stash keeps the scroll position")
	assert.Contains(t, panelViewOf(m), "row Dv")

	m = a6LongPanel(t, panelOnEC2(t))
	m = followLink(t, m, "eventlog", nil)
	m, _ = update(t, m, keyBackspace)
	require.True(t, panelShown(m))
	assert.NotContains(t, panelViewOf(m), "row Aa", "back keeps the scroll position")
	assert.Contains(t, panelViewOf(m), "row Dv")
}

// H2. Fixed in PR 4: a panel that comes back while the filter takes the keys
// has no focus. tab gives it the focus after the filter closes.
func TestA6_H2_RestoreDuringTheFilterHasNoFocus(t *testing.T) {
	m := panelOnEC2(t)
	require.True(t, panelHasFocus(m))
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})
	m, _ = update(t, m, press("/"))
	require.True(t, m.capturingInput())

	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})

	require.True(t, panelShown(m))
	assert.False(t, panelHasFocus(m), "the filter gets the keys, so the panel has no focus")

	m, _ = update(t, m, keyEsc)
	require.False(t, m.capturingInput())
	m, _ = update(t, m, keyTab)
	assert.True(t, panelHasFocus(m), "tab moves the focus after the filter closes")
}

// H2. Fixed in PR 4: a detail result that arrives while the filter is open
// opens the panel without the focus.
func TestA6_H2_DetailDuringTheFilterHasNoFocus(t *testing.T) {
	m := panelOnEC2(t)
	m, _ = update(t, m, press("q"))
	require.False(t, panelShown(m))
	m, _ = update(t, m, press("/"))
	require.True(t, m.capturingInput())

	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.TabbedContentMsg{
		PanelTitle: "i-2", Tabs: a6Tabs("i-2"),
	}})
	require.True(t, panelShown(m))
	assert.False(t, panelHasFocus(m))

	m, _ = update(t, m, keyTab)
	assert.False(t, panelHasFocus(m), "tab goes to the filter")

	m, _ = update(t, m, keyEsc)
	m, _ = update(t, m, keyTab)
	assert.True(t, panelHasFocus(m), "tab moves the focus after the filter closes")
}

// H2. Fixed in PR 4: back traversal keeps the focus of the saved panel.
func TestA6_H2_BackKeepsTheFocusWithoutAFilter(t *testing.T) {
	m := panelOnEC2(t)
	m = followLink(t, m, "eventlog", nil)
	m, _ = update(t, m, keyBackspace)
	require.True(t, panelShown(m))
	assert.True(t, panelHasFocus(m))
}

// H3. Fixed in PR 3: a link to a row of the view on top adds no history
// entry (decision D3), so back does not open the old panel next to the new
// row.
func TestA6_H3_SameViewLinkAddsNoEntry(t *testing.T) {
	m := sameViewLink(t, sqsWithDLQPanel(t))

	assert.Equal(t, 2, m.nav.Depth(), "the link only moves the cursor")
	assert.Equal(t, 0, historyLenOf(m))

	m, _ = update(t, m, keyBackspace)
	assert.Equal(t, 2, m.nav.Depth())
	assert.False(t, panelShown(m), "back has nothing to open")
}

// H3. Fixed in PR 3: no entry exists after a same-view link, so it does not
// matter how a child view closes.
func TestA6_H3_ChildViewsLeaveNoEntry(t *testing.T) {
	m := sameViewLink(t, sqsWithDLQPanel(t))
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	m, _ = update(t, m, press("q"))
	assert.Equal(t, "sqs_queues", m.nav.Current().ID())
	assert.Equal(t, 0, historyLenOf(m), "after q")

	m = sameViewLink(t, sqsWithDLQPanel(t))
	m = openA6Form(t, m)
	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.FormResultMsg{}})
	assert.Equal(t, "sqs_queues", m.nav.Current().ID())
	assert.Equal(t, 0, historyLenOf(m), "after a form")
}

// H3. Fixed in PR 3: the detail of the new row opens as usual, and back has
// no old panel to put in its place.
func TestA6_H3_DetailOfTheNewRowStays(t *testing.T) {
	m := sameViewLink(t, sqsWithDLQPanel(t))
	top := m.nav.Current()
	m, _ = update(t, m, nav.Routed{Origin: top, Msg: msg.TabbedContentMsg{
		PanelTitle: "orders", Tabs: a6Tabs("orders"),
	}})
	require.Equal(t, "orders", panelTitleOf(m))

	m, _ = update(t, m, keyBackspace)

	assert.True(t, panelShown(m))
	assert.Equal(t, "orders", panelTitleOf(m))
	assert.Equal(t, 0, historyLenOf(m))
}

// D3. A link to another view adds an entry. Since jump PR 2, a link to the
// view on top with no focus row pushes no copy and adds no entry
// (docs/design/jump.md, H10).
func TestA6Keep_LinksToOtherViewsAddAnEntry(t *testing.T) {
	m := followLink(t, sqsWithDLQPanel(t), "eventlog", nil)
	assert.Equal(t, 1, historyLenOf(m), "another view")

	m = followLink(t, sqsWithDLQPanel(t), "sqs_queues", nil)
	assert.Equal(t, 0, historyLenOf(m), "the view on top without a focus row")
	assert.Equal(t, 2, m.nav.Depth(), "no second copy")
	assert.False(t, panelShown(m), "the link still closes the panel")
}

// I3. Back skips an entry whose view is gone from its depth, also when no
// trim removed it.
func TestA6Keep_BackSkipsAnEntryOfAReplacedView(t *testing.T) {
	m := panelOnEC2(t)
	m = followLink(t, m, "eventlog", nil)
	require.Equal(t, 1, historyLenOf(m))
	m.nav.PopTo(1)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "sg_list"})
	require.Equal(t, 3, m.nav.Depth())
	require.Equal(t, 1, historyLenOf(m), "the stale entry is still there")

	m, _ = update(t, m, keyBackspace)

	assert.Equal(t, 3, m.nav.Depth(), "back does not pop to a view that is not the owner")
	assert.False(t, panelShown(m))
	assert.Equal(t, 0, historyLenOf(m))
}

// H4. Fixed in PR 4: a link from a panel that closed, or from an older
// panel, does nothing.
func TestA6_H4_LinkOfAClosedPanelDoesNothing(t *testing.T) {
	m := panelOnEC2(t)
	openA6Panel(&m, "i-1")
	m, _ = update(t, m, press("2"))
	m, cmd := update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	m, _ = update(t, m, press("q")) // closes the panel
	m, _ = update(t, m, press("q")) // pops the EC2 list
	require.Equal(t, "home", m.nav.Current().ID())

	link := cmd()
	require.IsType(t, panelRouted{}, link)
	require.IsType(t, ui.ContentLinkActivatedMsg{}, link.(panelRouted).msg)
	m, _ = update(t, m, link)

	assert.Equal(t, "home", m.nav.Current().ID(), "the link of a closed panel does nothing")
	assert.Equal(t, 1, m.nav.Depth())

	m = panelOnEC2(t)
	openA6Panel(&m, "i-1")
	m, _ = update(t, m, press("2"))
	m, cmd = update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	openA6Panel(&m, "i-2")
	m, _ = update(t, m, cmd())
	assert.Equal(t, "ec2_list", m.nav.Current().ID(), "the link of an older panel does nothing")
	assert.Equal(t, "i-2", panelTitleOf(m))
	assert.Equal(t, 0, historyLenOf(m))
}

// H4. Fixed in PR 4: the link of the panel on screen navigates at once, so
// no key can come between the link and the navigation.
func TestA6_H4_LinkNavigatesAtOnce(t *testing.T) {
	m := panelOnEC2(t)
	openA6Panel(&m, "i-1")
	m, _ = update(t, m, press("2"))
	m, cmd := update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	m, _ = update(t, m, cmd())

	assert.Equal(t, "eventlog", m.nav.Current().ID())
	assert.False(t, panelShown(m))
	assert.Equal(t, 1, historyLenOf(m))

	m, _ = update(t, m, keyBackspace)
	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	require.True(t, panelShown(m), "back restores the panel")
	assert.Equal(t, "i-1", panelTitleOf(m))
	assert.Equal(t, 0, historyLenOf(m))
}

// H4. Fixed in PR 4: the narrow fallback of TabbedContentMsg pushes its first
// tab at once, onto the view that asked.
func TestA6_H4_NarrowFallbackNavigatesAtOnce(t *testing.T) {
	m := newTestModel(panelNarrow, 40)
	m.svc.EC2 = new(awstest.MockEC2Service)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.TabbedContentMsg{
		PanelTitle: "i-1", Tabs: a6Tabs("i-1"),
	}})

	assert.True(t, strings.HasPrefix(m.nav.Current().ID(), "content:"))
	assert.Equal(t, 3, m.nav.Depth())

	m, _ = update(t, m, press("q"))
	assert.Equal(t, "ec2_list", m.nav.Current().ID())
}

// H5. Fixed in PR 4: help drops the panel keys when a resize hides the panel.
func TestA6_H5_HelpDropsPanelKeysAfterAStash(t *testing.T) {
	m := panelOnEC2(t)
	m, _ = update(t, m, press("q"))
	m, _ = update(t, m, press("?"))
	require.NotContains(t, ansi.Strip(m.help.View()), "Panel", "help without a panel")

	m = panelOnEC2(t)
	m, _ = update(t, m, press("?"))
	require.Contains(t, ansi.Strip(m.help.View()), "Panel")

	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})

	require.False(t, panelShown(m))
	assert.NotContains(t, ansi.Strip(m.help.View()), "Panel")
}

// H5. Fixed in PR 4: help gets the panel keys when a resize shows the stashed
// panel again, or when a detail result opens a panel.
func TestA6_H5_HelpGetsPanelKeysAfterARestore(t *testing.T) {
	m := stashedOnEC2(t)
	m, _ = update(t, m, press("?"))
	require.NotContains(t, ansi.Strip(m.help.View()), "Panel")

	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})

	require.True(t, panelShown(m))
	assert.Contains(t, ansi.Strip(m.help.View()), "Panel")

	m = panelOnEC2(t)
	m, _ = update(t, m, press("q"))
	m, _ = update(t, m, press("?"))
	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.TabbedContentMsg{
		PanelTitle: "i-2", Tabs: a6Tabs("i-2"),
	}})
	assert.Contains(t, ansi.Strip(m.help.View()), "Panel", "a detail result")
}

// H6. Fixed in PR 4: the header hides the back label while a form is on top,
// because the form takes backspace. The label comes back after the form.
func TestA6_H6_NoBackLabelOverAForm(t *testing.T) {
	m := newTestModel(panelWide, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	openA6Panel(&m, "dlq")
	m = followLink(t, m, "sg_list", nil)
	require.Equal(t, 1, historyLenOf(m))
	require.Contains(t, ansi.Strip(m.View().Content), "← bksp dlq")
	m = openA6Form(t, m)

	assert.NotContains(t, ansi.Strip(m.View().Content), "← bksp")
	for _, h := range m.currentKeyHints() {
		assert.NotEqual(t, "cross-nav back", h.Help().Desc, "the status bar hides the hint")
	}

	depth := m.nav.Depth()
	m, _ = update(t, m, keyBackspace)
	assert.Equal(t, depth, m.nav.Depth(), "the form took backspace")
	assert.Equal(t, 1, historyLenOf(m))

	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.FormResultMsg{}})
	assert.Contains(t, ansi.Strip(m.View().Content), "← bksp dlq", "the label comes back")
}

// H6. Fixed in PR 4: the header also hides the back label while the filter
// of the view on top takes the keys.
func TestA6_H6_NoBackLabelDuringTheFilter(t *testing.T) {
	m := panelOnEC2(t)
	openA6Panel(&m, "dlq")
	m.svc.SQS = new(awstest.MockSQSService)
	m = followLink(t, m, "sqs_queues", nil)
	require.Equal(t, 1, historyLenOf(m))
	require.Contains(t, ansi.Strip(m.View().Content), "← bksp dlq")

	m, _ = update(t, m, press("/"))
	require.True(t, m.capturingInput())
	assert.NotContains(t, ansi.Strip(m.View().Content), "← bksp")

	m, _ = update(t, m, keyEsc)
	require.False(t, m.capturingInput())
	assert.Contains(t, ansi.Strip(m.View().Content), "← bksp dlq")
}

// H2. Fixed in PR 4: back traversal to a view whose filter is open restores
// the panel without the focus, also when the panel had the focus.
func TestA6_H2_BackToAFilterHasNoFocus(t *testing.T) {
	m := panelOnEC2(t)
	require.True(t, panelHasFocus(m))
	list := m.nav.Current()
	m = followLink(t, m, "eventlog", nil)
	require.Equal(t, "eventlog", m.nav.Current().ID())
	m.nav.UpdateView(list, press("/")) // the filter of the list below the top

	m, _ = update(t, m, keyBackspace)

	require.Equal(t, "ec2_list", m.nav.Current().ID())
	require.True(t, m.capturingInput())
	require.True(t, panelShown(m))
	assert.False(t, panelHasFocus(m))
}

// H5. Fixed in PR 4: a link that arrives after help opened closes the panel,
// and help drops the panel keys.
func TestA6_H5_HelpDropsPanelKeysAfterALateLink(t *testing.T) {
	m := panelOnEC2(t)
	openA6Panel(&m, "i-1")
	m, _ = update(t, m, press("2"))
	m, cmd := update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	m, _ = update(t, m, press("?"))
	require.Contains(t, ansi.Strip(m.help.View()), "Panel")

	m, _ = update(t, m, cmd())

	require.Equal(t, "eventlog", m.nav.Current().ID())
	assert.NotContains(t, ansi.Strip(m.help.View()), "Panel")
	assert.Contains(t, ansi.Strip(m.help.View()), "auto-scroll", "help lists the keys of the new view")
}

// H5. Fixed in PR 4: a view that sends a navigation while help is open
// closes the panel, and help drops the panel keys.
func TestA6_H5_HelpDropsPanelKeysAfterANavigation(t *testing.T) {
	m := panelOnEC2(t)
	m, _ = update(t, m, press("?"))
	require.Contains(t, ansi.Strip(m.help.View()), "Panel")

	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.NavigateMsg{ViewID: "eventlog"}})

	require.False(t, panelShown(m))
	assert.NotContains(t, ansi.Strip(m.help.View()), "Panel")
	assert.Contains(t, ansi.Strip(m.help.View()), "auto-scroll", "help lists the keys of the new view")
}

// H5. Fixed in PR 4: the narrow fallback of a detail result pushes a view
// while help is open, and help lists the keys of that view.
func TestA6_H5_HelpFollowsTheNarrowFallback(t *testing.T) {
	m := newTestModel(panelNarrow, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	m, _ = update(t, m, press("?"))
	require.Contains(t, ansi.Strip(m.help.View()), "auto-scroll")

	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.TabbedContentMsg{
		PanelTitle: "i-1", Tabs: a6Tabs("i-1"),
	}})

	require.True(t, strings.HasPrefix(m.nav.Current().ID(), "content:"))
	assert.NotContains(t, ansi.Strip(m.help.View()), "auto-scroll", "help drops the keys of the event log")
}

// H5. Fixed in PR 4: a late link to a row of the same view, or a focus
// navigation of the view, closes the panel without a push, and help drops
// the panel keys.
func TestA6_H5_HelpDropsPanelKeysWithoutAPush(t *testing.T) {
	m := sqsWithDLQPanel(t)
	m, _ = update(t, m, press("2"))
	m, cmd := update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	m, _ = update(t, m, press("?"))
	require.Contains(t, ansi.Strip(m.help.View()), "Panel")
	m, _ = update(t, m, cmd())
	require.Equal(t, 2, m.nav.Depth(), "the link only moves the cursor")
	require.False(t, panelShown(m))
	assert.NotContains(t, ansi.Strip(m.help.View()), "Panel", "same-view link")

	m = sqsWithDLQPanel(t)
	m, _ = update(t, m, press("?"))
	require.Contains(t, ansi.Strip(m.help.View()), "Panel")
	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.NavigateMsg{
		ViewID: "sqs_queues", Params: map[string]string{"focus": "orders"},
	}})
	require.Equal(t, 2, m.nav.Depth())
	require.False(t, panelShown(m))
	assert.NotContains(t, ansi.Strip(m.help.View()), "Panel", "focus navigation")
}

// H5. Fixed in PR 4: a back from the view on top while help is open drops
// the history entry, and help drops the back key.
func TestA6_H5_HelpDropsTheBackKeyAfterATrim(t *testing.T) {
	m := panelOnEC2(t)
	m = followLink(t, m, "eventlog", nil)
	require.Equal(t, 1, historyLenOf(m))
	m, _ = update(t, m, press("?"))
	require.Contains(t, ansi.Strip(m.help.View()), "cross-nav back")

	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.NavigateBackMsg{}})

	require.Equal(t, "ec2_list", m.nav.Current().ID())
	require.Equal(t, 0, historyLenOf(m))
	assert.NotContains(t, ansi.Strip(m.help.View()), "cross-nav back")
}

// H7 (L7). Fixed in PR 1: a tab that was shown before gets the new width when
// the panel width changes or the terminal is resized.
func TestA6_H7_EveryTabHasThePanelWidth(t *testing.T) {
	long := strings.Repeat("abcdefghij", 30)
	tabs := []msg.TabContent{
		{Title: "One", Content: long, Format: "text"},
		{Title: "Two", Content: long, Format: "text"},
	}
	for name, change := range map[string]func(t *testing.T, m Model) Model{
		"panel width keys": func(t *testing.T, m Model) Model {
			for range 4 {
				m, _ = update(t, m, press(">"))
			}
			return m
		},
		"terminal resize": func(t *testing.T, m Model) Model {
			m, _ = update(t, m, tea.WindowSizeMsg{Width: 160, Height: 30})
			return m
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := newTestModel(200, 40)
			m, _ = update(t, m, msg.TabbedContentMsg{PanelTitle: "p", Tabs: tabs})
			require.True(t, panelShown(m))
			m, _ = update(t, m, press("2"))
			m, _ = update(t, m, press("1"))
			before := a6MaxWidth(panelViewOf(m))
			m = change(t, m)
			tab1 := panelViewOf(m)
			require.NotEqual(t, before, a6MaxWidth(tab1), "the panel width changed")

			m, _ = update(t, m, press("2"))

			assert.Equal(t, a6MaxWidth(tab1), a6MaxWidth(panelViewOf(m)), "tab 2 has the width of tab 1")
			// Only the terminal resize changes the height. The ui test
			// TestTabbedPanelSetSizeResizesHiddenTabs checks the height of
			// each viewer.
			assert.Equal(t, strings.Count(tab1, "\n"), strings.Count(panelViewOf(m), "\n"), "tab 2 has the height of tab 1")
			assert.LessOrEqual(t, a6MaxWidth(ansi.Strip(m.View().Content)), m.width, "the screen fits the terminal")
		})
	}
}

// --- Behavior that A6 keeps ---

// I1, I3. A chain of two links goes back one link at a time, each to the
// view and the panel that held the link.
func TestA6Keep_ChainOfTwoLinks(t *testing.T) {
	m := panelOnEC2(t)
	m = followLink(t, m, "eventlog", nil)
	require.Equal(t, 3, m.nav.Depth())
	openA6Panel(&m, "second")
	m = followLink(t, m, "sg_list", nil)
	require.Equal(t, 4, m.nav.Depth())
	require.Equal(t, 2, historyLenOf(m))

	m, _ = update(t, m, keyBackspace)
	assert.Equal(t, 3, m.nav.Depth())
	assert.Equal(t, "second", panelTitleOf(m))

	m, _ = update(t, m, keyBackspace)
	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	assert.Equal(t, "web-server-1", panelTitleOf(m))
	assert.Equal(t, 0, historyLenOf(m))
}

// I3. Back pops to the depth of the entry, also when the user opened more
// views after the link.
func TestA6Keep_BackPopsToTheDepthOfTheEntry(t *testing.T) {
	m := panelOnEC2(t)
	m = followLink(t, m, "eventlog", nil)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "sg_list"})
	require.Equal(t, 4, m.nav.Depth())

	m, _ = update(t, m, keyBackspace)

	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	assert.True(t, panelShown(m))
	assert.Equal(t, "web-server-1", panelTitleOf(m))
}

// I3. An entry stays when a view above the link target closes, because its
// owner is still on the stack.
func TestA6Keep_EntryStaysWhenAViewAboveCloses(t *testing.T) {
	m := panelOnEC2(t)
	m = followLink(t, m, "eventlog", nil)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "sg_list"})
	m, _ = update(t, m, press("q"))
	require.Equal(t, 3, m.nav.Depth())
	require.Equal(t, 1, historyLenOf(m))

	m, _ = update(t, m, keyBackspace)

	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	assert.Equal(t, "web-server-1", panelTitleOf(m))
}

// I2, I3. Back in a narrow terminal goes back without the panel. The panel
// opens when the terminal is wide again.
func TestA6Keep_BackInANarrowTerminal(t *testing.T) {
	m := panelOnEC2(t)
	m = followLink(t, m, "eventlog", nil)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})

	m, _ = update(t, m, keyBackspace)
	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	assert.False(t, panelShown(m))
	assert.Equal(t, "web-server-1", stashTitleOf(m))

	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})
	assert.True(t, panelShown(m))
	assert.Equal(t, "web-server-1", panelTitleOf(m))
}

// I3. The history holds at most historyMax entries. Back still works
// for the newest ones.
func TestA6Keep_TheDepthCap(t *testing.T) {
	m := panelOnEC2(t)
	for i := range historyMax + 2 {
		openA6Panel(&m, "p"+string(rune('a'+i)))
		// Two targets in turn: a link to the view on top pushes nothing.
		m = followLink(t, m, []string{"eventlog", "sg_list"}[i%2], nil)
	}
	require.Equal(t, historyMax, historyLenOf(m))

	m, _ = update(t, m, keyBackspace)
	assert.Equal(t, "p"+string(rune('a'+historyMax+1)), panelTitleOf(m))
	assert.Equal(t, historyMax-1, historyLenOf(m))
}

// D7. q in the view that a link opened goes back without the panel and drops
// the entry.
func TestA6Keep_QAfterALinkDropsTheEntry(t *testing.T) {
	m := panelOnEC2(t)
	m = followLink(t, m, "vpc_list", map[string]string{"focus": "vpc-1"})
	require.Equal(t, 3, m.nav.Depth())

	m, _ = update(t, m, press("q"))

	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	assert.False(t, panelShown(m))
	assert.Equal(t, 0, historyLenOf(m))
}

// D7. esc in the view that a link opened also goes back without the panel and
// drops the entry.
func TestA6Keep_EscAfterALinkDropsTheEntry(t *testing.T) {
	m := panelOnEC2(t)
	m = followLink(t, m, "eventlog", nil)
	require.Equal(t, 3, m.nav.Depth())

	m, cmd := update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	back := findOf[msg.NavigateBackMsg](collectMsgs(cmd))
	require.NotNil(t, back)
	m, _ = update(t, m, back)

	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	assert.False(t, panelShown(m))
	assert.Equal(t, 0, historyLenOf(m))
}

// R2. A panel that comes back after a resize has the width of a new panel at
// the current size, from the stash and from the history.
func TestA6Keep_RestoredPanelHasTheCurrentWidth(t *testing.T) {
	fresh := newTestModel(200, 40)
	fresh.openTabbedPanel("web-server-1", []msg.TabContent{{Title: "web-server-1", Content: "{}", Format: "json"}})
	want := a6MaxWidth(panelViewOf(fresh))
	require.NotEqual(t, want, a6MaxWidth(panelViewOf(panelOnEC2(t))), "the panel is wider at 200 columns")

	m := stashedOnEC2(t)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 200, Height: 40})
	require.True(t, panelShown(m))
	assert.Equal(t, want, a6MaxWidth(panelViewOf(m)), "after the stash")

	m = panelOnEC2(t)
	m = followLink(t, m, "eventlog", nil)
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 200, Height: 40})
	m, _ = update(t, m, keyBackspace)
	require.True(t, panelShown(m))
	assert.Equal(t, want, a6MaxWidth(panelViewOf(m)), "after back")
}

// D6, I6. With the panel open and not focused, q closes the panel and esc goes
// to the view, which goes back.
func TestA6Keep_QAndEscWithAnUnfocusedPanel(t *testing.T) {
	m := panelOnEC2(t)
	m, _ = update(t, m, keyTab)
	require.False(t, panelHasFocus(m))

	mq, _ := update(t, m, press("q"))
	assert.Equal(t, "ec2_list", mq.nav.Current().ID())
	assert.False(t, panelShown(mq))

	me, cmd := update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	back := findOf[msg.NavigateBackMsg](collectMsgs(cmd))
	require.NotNil(t, back)
	me, _ = update(t, me, back)
	assert.Equal(t, "home", me.nav.Current().ID())
	assert.False(t, panelShown(me))
}

// I4. resetNav drops the panel, the stash and the history.
func TestA6Keep_ResetDropsEverything(t *testing.T) {
	m := panelOnEC2(t)
	m = followLink(t, m, "eventlog", nil)
	openA6Panel(&m, "second")
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})
	require.Equal(t, "second", stashTitleOf(m))
	require.Equal(t, 1, historyLenOf(m))

	m.resetNav()
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})

	assert.Equal(t, "home", m.nav.Current().ID())
	assert.False(t, panelShown(m))
	assert.Empty(t, stashTitleOf(m))
	assert.Equal(t, 0, historyLenOf(m))
}

// I7. The width override stays when the panel closes and opens again.
func TestA6Keep_WidthOverrideStays(t *testing.T) {
	m := panelOnEC2(t)
	before := m.panelWidth()
	m, _ = update(t, m, press(">"))
	m, _ = update(t, m, press(">"))
	changed := m.panelWidth()
	require.NotEqual(t, before, changed)

	m, _ = update(t, m, press("q"))
	openA6Panel(&m, "again")

	assert.Equal(t, changed, m.panelWidth())
}

// I8. In a narrow terminal, a detail result shows its first tab as a full
// screen view.
func TestA6Keep_NarrowFallbackShowsTheFirstTab(t *testing.T) {
	m := newTestModel(panelNarrow, 40)
	m.svc.EC2 = new(awstest.MockEC2Service)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.TabbedContentMsg{
		PanelTitle: "i-1", Tabs: a6Tabs("i-1"),
	}})

	assert.True(t, strings.HasPrefix(m.nav.Current().ID(), "content:"))
	assert.Contains(t, ansi.Strip(m.View().Content), "info for i-1")
	assert.False(t, panelShown(m))
}

// I6. With the panel focused, the panel gets the keys, and tab moves the
// focus to the view and back.
func TestA6Keep_FocusedPanelGetsTheKeys(t *testing.T) {
	m := panelOnEC2(t)
	openA6Panel(&m, "i-1")
	require.True(t, panelHasFocus(m))
	m, _ = update(t, m, press("2"))
	assert.Equal(t, 1, panelTabOf(m))

	m, _ = update(t, m, keyTab)
	assert.False(t, panelHasFocus(m))
	m, _ = update(t, m, keyTab)
	assert.True(t, panelHasFocus(m))
}

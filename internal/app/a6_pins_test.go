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

// The tests in this file pin the side panel before A6
// (docs/design/a6-panel-controller.md). A test named TestA6Hazard_... pins a
// hazard as it is today. Its comment names the PR that changes it, and that PR
// flips the test. A test named TestA6Keep_... pins behavior that A6 keeps.
//
// The helpers below are the only code here that reads the panel fields of
// Model. PR 2 moves the fields into panelController and changes only these
// helpers.

func panelShown(m Model) bool            { return m.panelOpen }
func panelTitleOf(m Model) string        { return m.panelTitle }
func panelHasFocus(m Model) bool         { return m.panelFocused }
func panelTabOf(m Model) int             { return m.panel.ActiveTab() }
func stashTitleOf(m Model) string        { return m.stashedPanelTitle }
func historyLenOf(m Model) int           { return len(m.crossNavHistory) }
func historyDepthOf(m Model) int         { return m.crossNavHistory[len(m.crossNavHistory)-1].navDepth }
func panelViewOf(m Model) string         { return m.panel.View() }
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

// followLink sends a panel link as the panel sends it today, and then the
// NavigateMsg that the handler returns.
func followLink(t *testing.T, m Model, viewID string, params map[string]string) Model {
	t.Helper()
	m, cmd := update(t, m, ui.ContentLinkActivatedMsg{ViewID: viewID, Params: params})
	require.NotNil(t, cmd)
	m, _ = update(t, m, cmd())
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

// H1. A narrow and then wide resize opens the panel on tab 1, with the focus.
// PR 3 flips this: the tab and the focus come back.
func TestA6Hazard_H1_StashLosesTabAndFocus(t *testing.T) {
	m := panelOnEC2(t)
	openA6Panel(&m, "i-1")
	m, _ = update(t, m, press("2"))
	m, _ = update(t, m, keyTab)
	require.Equal(t, 1, panelTabOf(m))
	require.False(t, panelHasFocus(m))

	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})

	require.True(t, panelShown(m))
	assert.Equal(t, 0, panelTabOf(m), "the stash lost the tab")
	assert.True(t, panelHasFocus(m), "the stash lost the focus")
}

// H1. Back traversal opens the panel on tab 1, not on the tab with the link.
// PR 3 flips this.
func TestA6Hazard_H1_BackLosesTheTabOfTheLink(t *testing.T) {
	m := panelOnEC2(t)
	openA6Panel(&m, "i-1")
	m, _ = update(t, m, press("2"))
	require.Equal(t, 1, panelTabOf(m))
	m = followLink(t, m, "eventlog", nil)
	require.Equal(t, "eventlog", m.nav.Current().ID())

	m, _ = update(t, m, keyBackspace)

	assert.Equal(t, "ec2_list", m.nav.Current().ID())
	require.True(t, panelShown(m))
	assert.Equal(t, "i-1", panelTitleOf(m))
	assert.Equal(t, 0, panelTabOf(m), "back lost the tab")
}

// H1. A narrow and then wide resize opens the panel at the top of the text.
// PR 3 flips this: the scroll position comes back.
func TestA6Hazard_H1_StashLosesTheScrollPosition(t *testing.T) {
	var rows []string
	for i := range 100 {
		rows = append(rows, "row "+string(rune('A'+i/26))+string(rune('a'+i%26)))
	}
	m := panelOnEC2(t)
	m.openTabbedPanel("long", []msg.TabContent{{Title: "Info", Content: strings.Join(rows, "\n"), Format: "text"}})
	m, _ = update(t, m, press("G"))
	require.NotContains(t, panelViewOf(m), "row Aa")
	require.Contains(t, panelViewOf(m), "row Dv")

	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})

	require.True(t, panelShown(m))
	assert.Contains(t, panelViewOf(m), "row Aa", "the stash lost the scroll position")
}

// H2. A panel that had the focus when the stash took it gets the focus back
// while the filter takes the keys. PR 4 flips this: a panel that opens or
// comes back while the view takes input has no focus.
func TestA6Hazard_H2_RestoreFocusesThePanelDuringTheFilter(t *testing.T) {
	m := panelOnEC2(t)
	require.True(t, panelHasFocus(m))
	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})
	m, _ = update(t, m, press("/"))
	require.True(t, m.capturingInput())

	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})

	require.True(t, panelShown(m))
	assert.True(t, panelHasFocus(m) && m.capturingInput(), "the panel looks focused, but the filter gets the keys")
}

// H2. A detail result that arrives while the filter is open gives the panel
// the focus, and tab cannot move it. PR 4 flips this.
func TestA6Hazard_H2_DetailDuringTheFilterLooksFocused(t *testing.T) {
	m := panelOnEC2(t)
	m, _ = update(t, m, press("q"))
	require.False(t, panelShown(m))
	m, _ = update(t, m, press("/"))
	require.True(t, m.capturingInput())

	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.TabbedContentMsg{
		PanelTitle: "i-2", Tabs: a6Tabs("i-2"),
	}})
	require.True(t, panelShown(m))
	assert.True(t, panelHasFocus(m))

	m, _ = update(t, m, keyTab)
	assert.True(t, panelHasFocus(m), "tab goes to the filter")
}

// H3. A link to a row of the view on top adds a history entry at the current
// depth. PR 3 flips this: the link adds no entry.
func TestA6Hazard_H3_SameViewLinkAddsAnEntry(t *testing.T) {
	m := sameViewLink(t, sqsWithDLQPanel(t))

	assert.Equal(t, 2, m.nav.Depth(), "the link only moves the cursor")
	require.Equal(t, 1, historyLenOf(m))
	assert.Equal(t, 2, historyDepthOf(m))

	m, _ = update(t, m, keyBackspace)
	assert.Equal(t, 2, m.nav.Depth())
	assert.True(t, panelShown(m))
	assert.Equal(t, "orders-dlq", panelTitleOf(m), "back opens the old panel next to the new row")
}

// H3. A child view that closes with q drops the entry of a same-view link, but
// a form that closes keeps it. PR 3 flips this: no entry exists.
func TestA6Hazard_H3_SameViewEntryDependsOnHowTheChildCloses(t *testing.T) {
	m := sameViewLink(t, sqsWithDLQPanel(t))
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	m, _ = update(t, m, press("q"))
	assert.Equal(t, "sqs_queues", m.nav.Current().ID())
	assert.Equal(t, 0, historyLenOf(m), "q drops the entry")

	m = sameViewLink(t, sqsWithDLQPanel(t))
	m = openA6Form(t, m)
	m, _ = update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.FormResultMsg{}})
	assert.Equal(t, "sqs_queues", m.nav.Current().ID())
	assert.Equal(t, 1, historyLenOf(m), "a form keeps the entry")
}

// H3. A detail result of the new row that arrives after back replaces the
// restored panel, and the history is empty. PR 3 flips this: back has no
// entry to restore, so the late result is the only panel.
func TestA6Hazard_H3_LateDetailReplacesTheRestoredPanel(t *testing.T) {
	m := sameViewLink(t, sqsWithDLQPanel(t))
	top := m.nav.Current()
	m, _ = update(t, m, keyBackspace)
	require.Equal(t, "orders-dlq", panelTitleOf(m))
	require.Equal(t, 0, historyLenOf(m))

	m, _ = update(t, m, nav.Routed{Origin: top, Msg: msg.TabbedContentMsg{
		PanelTitle: "orders", Tabs: a6Tabs("orders"),
	}})

	assert.Equal(t, "orders", panelTitleOf(m))
	assert.Equal(t, 0, historyLenOf(m), "the restored panel is gone")
}

// H4. A link travels as a plain message. A key in between changes the stack,
// and the link then pushes its view onto the wrong view. PR 4 flips this: a
// link from a panel that closed does nothing.
func TestA6Hazard_H4_LinkAfterQPushesOntoTheWrongView(t *testing.T) {
	m := panelOnEC2(t)
	openA6Panel(&m, "i-1")
	m, _ = update(t, m, press("2"))
	m, cmd := update(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	m, _ = update(t, m, press("q")) // closes the panel
	m, _ = update(t, m, press("q")) // pops the EC2 list
	require.Equal(t, "home", m.nav.Current().ID())

	link := cmd()
	require.IsType(t, ui.ContentLinkActivatedMsg{}, link)
	m, cmd = update(t, m, link)
	m, _ = update(t, m, cmd())

	assert.Equal(t, "eventlog", m.nav.Current().ID(), "the link of a closed panel pushed its view")
	assert.Equal(t, 2, m.nav.Depth())
}

// H4. The NavigateMsg of a link comes in a second command. A back in between
// restores the panel, and the NavigateMsg then drops it and the entry. PR 4
// flips this: the link navigates at once.
func TestA6Hazard_H4_BackBeforeTheNavigateLosesTheEntry(t *testing.T) {
	m := panelOnEC2(t)
	m, cmd := update(t, m, ui.ContentLinkActivatedMsg{ViewID: "eventlog"})
	require.NotNil(t, cmd)
	require.Equal(t, 1, historyLenOf(m))

	m, _ = update(t, m, keyBackspace)
	require.True(t, panelShown(m))
	m, _ = update(t, m, cmd())

	assert.Equal(t, "eventlog", m.nav.Current().ID())
	assert.False(t, panelShown(m))
	assert.Equal(t, 0, historyLenOf(m), "the panel and the entry are gone")
}

// H4. The narrow fallback of TabbedContentMsg sends its NavigateMsg in a
// second command. A q in between pops the view, and the content then opens on
// top of home. PR 4 flips this: the fallback navigates at once.
func TestA6Hazard_H4_NarrowFallbackAfterQ(t *testing.T) {
	m := newTestModel(panelNarrow, 40)
	m.svc.EC2 = new(awstest.MockEC2Service)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "ec2_list"})
	m, cmd := update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.TabbedContentMsg{
		PanelTitle: "i-1", Tabs: a6Tabs("i-1"),
	}})
	require.NotNil(t, cmd)
	m, _ = update(t, m, press("q"))
	require.Equal(t, "home", m.nav.Current().ID())

	m, _ = update(t, m, cmd())

	assert.True(t, strings.HasPrefix(m.nav.Current().ID(), "content:"), "the detail of the EC2 list opened over home")
	assert.Equal(t, 2, m.nav.Depth())
}

// H5. Help keeps the panel keys after a resize hides the panel. PR 4 flips
// this.
func TestA6Hazard_H5_HelpKeepsPanelKeysAfterAStash(t *testing.T) {
	m := panelOnEC2(t)
	m, _ = update(t, m, press("q"))
	m, _ = update(t, m, press("?"))
	require.NotContains(t, ansi.Strip(m.help.View()), "Panel", "help without a panel")

	m = panelOnEC2(t)
	m, _ = update(t, m, press("?"))
	require.Contains(t, ansi.Strip(m.help.View()), "Panel")

	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelNarrow, Height: 40})

	require.False(t, panelShown(m))
	assert.Contains(t, ansi.Strip(m.help.View()), "Panel", "help still lists the panel keys")
}

// H5. Help does not list the panel keys after a resize shows the stashed
// panel again. PR 4 flips this.
func TestA6Hazard_H5_HelpMissesPanelKeysAfterARestore(t *testing.T) {
	m := stashedOnEC2(t)
	m, _ = update(t, m, press("?"))
	require.NotContains(t, ansi.Strip(m.help.View()), "Panel")

	m, _ = update(t, m, tea.WindowSizeMsg{Width: panelWide, Height: 40})

	require.True(t, panelShown(m))
	assert.NotContains(t, ansi.Strip(m.help.View()), "Panel", "help does not list the panel keys")
}

// H6. The header shows the back label while a form is on top, but the form
// takes backspace. PR 4 flips this: the header hides the label.
func TestA6Hazard_H6_BackLabelOverAForm(t *testing.T) {
	m := newTestModel(panelWide, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	openA6Panel(&m, "dlq")
	m = followLink(t, m, "eventlog", nil)
	require.Equal(t, 1, historyLenOf(m))
	m = openA6Form(t, m)

	assert.Contains(t, ansi.Strip(m.View().Content), "← bksp dlq")
	for _, h := range m.currentKeyHints() {
		assert.NotEqual(t, "cross-nav back", h.Help().Desc, "the status bar hides the hint")
	}

	depth := m.nav.Depth()
	m, _ = update(t, m, keyBackspace)
	assert.Equal(t, depth, m.nav.Depth(), "the form took backspace")
	assert.Equal(t, 1, historyLenOf(m))
}

// H7 (L7). A tab that was shown before keeps its old width after the panel
// gets narrower. PR 1 flips this: every tab gets the width of the panel.
func TestA6Hazard_H7_HiddenTabKeepsItsOldWidth(t *testing.T) {
	long := strings.Repeat("abcdefghij", 30)
	tabs := []msg.TabContent{
		{Title: "One", Content: long, Format: "text"},
		{Title: "Two", Content: long, Format: "text"},
	}
	m := newTestModel(200, 40)
	m, _ = update(t, m, msg.TabbedContentMsg{PanelTitle: "p", Tabs: tabs})
	require.True(t, panelShown(m))
	m, _ = update(t, m, press("2"))
	m, _ = update(t, m, press("1"))
	for range 4 {
		m, _ = update(t, m, press(">"))
	}
	m, _ = update(t, m, press("2"))

	assert.Greater(t, a6MaxWidth(panelViewOf(m)), m.panelWidth(), "tab 2 is wider than the panel")
	assert.Greater(t, a6MaxWidth(ansi.Strip(m.View().Content)), m.width, "the screen is wider than the terminal")
}

// --- Behavior that A6 keeps ---

// I1, I3. A chain of two links goes back one link at a time, each to the
// view and the panel that held the link.
func TestA6Keep_ChainOfTwoLinks(t *testing.T) {
	m := panelOnEC2(t)
	m = followLink(t, m, "eventlog", nil)
	require.Equal(t, 3, m.nav.Depth())
	openA6Panel(&m, "second")
	m = followLink(t, m, "eventlog", nil)
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
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
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
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
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

// I3. The history holds at most crossNavHistoryMax entries. Back still works
// for the newest ones.
func TestA6Keep_TheDepthCap(t *testing.T) {
	m := panelOnEC2(t)
	for i := range crossNavHistoryMax + 2 {
		openA6Panel(&m, "p"+string(rune('a'+i)))
		m = followLink(t, m, "eventlog", nil)
	}
	require.Equal(t, crossNavHistoryMax, historyLenOf(m))

	m, _ = update(t, m, keyBackspace)
	assert.Equal(t, "p"+string(rune('a'+crossNavHistoryMax+1)), panelTitleOf(m))
	assert.Equal(t, crossNavHistoryMax-1, historyLenOf(m))
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
	m, cmd := update(t, m, nav.Routed{Origin: m.nav.Current(), Msg: msg.TabbedContentMsg{
		PanelTitle: "i-1", Tabs: a6Tabs("i-1"),
	}})
	require.NotNil(t, cmd)
	m, _ = update(t, m, cmd())

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

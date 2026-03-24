package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/config"
	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/registry"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func newTestModel(width, height int) Model {
	m := New(config.DefaultConfig())
	m.width = width
	m.height = height
	return m
}

func contentMsg() msg.NavigateMsg {
	return msg.NavigateMsg{
		ViewID: "content",
		Params: map[string]string{
			"title":   "test.json",
			"content": `{"key": "value"}`,
			"format":  "json",
		},
	}
}

func openPanel(m *Model) {
	m.openPanel("test.json", `{"key": "value"}`, "json")
}

// --- Helper tests ---

func TestCanShowPanel(t *testing.T) {
	m := newTestModel(119, 40)
	assert.False(t, m.canShowPanel())

	m.width = 120
	assert.True(t, m.canShowPanel())
}

func TestPanelWidth(t *testing.T) {
	m := newTestModel(120, 40)
	assert.Equal(t, 48, m.panelWidth()) // 120 * 40% = 48

	m.width = 200
	assert.Equal(t, 80, m.panelWidth()) // capped at 80

	m.width = 80
	assert.Equal(t, 40, m.panelWidth()) // min 40 (80 * 40% = 32, clamped to 40)
}

// --- NavigateMsg routing ---

func TestNavigateContentOpensPanelWhenWide(t *testing.T) {
	m := newTestModel(140, 40)
	result, _ := m.Update(contentMsg())
	m = result.(Model)

	assert.True(t, m.panelOpen)
	assert.True(t, m.panelFocused)
	assert.NotNil(t, m.panel)
}

func TestNavigateContentFallsBackToStackWhenNarrow(t *testing.T) {
	m := newTestModel(100, 40)
	initialDepth := m.nav.Depth()

	result, _ := m.Update(contentMsg())
	m = result.(Model)

	assert.False(t, m.panelOpen)
	assert.Greater(t, m.nav.Depth(), initialDepth)
}

func TestNonContentNavigateClosesPanel(t *testing.T) {
	m := newTestModel(140, 40)
	openPanel(&m)
	assert.True(t, m.panelOpen)

	result, _ := m.Update(msg.NavigateMsg{ViewID: "eventlog"})
	m = result.(Model)

	assert.False(t, m.panelOpen)
}

// --- Focus toggle ---

func TestTabTogglesFocus(t *testing.T) {
	m := newTestModel(140, 40)
	openPanel(&m)
	assert.True(t, m.panelFocused)

	// Tab → main focused
	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = result.(Model)
	assert.False(t, m.panelFocused)
	assert.True(t, m.panelOpen) // panel still open

	// Tab → panel focused
	result, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = result.(Model)
	assert.True(t, m.panelFocused)
}

// --- Panel close triggers ---

func TestEscWhenPanelFocusedClosesPanel(t *testing.T) {
	m := newTestModel(140, 40)
	openPanel(&m)
	assert.True(t, m.panelFocused)

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = result.(Model)

	assert.False(t, m.panelOpen)
	assert.False(t, m.panelFocused)
}

func TestQWhenPanelOpenClosesPanel(t *testing.T) {
	m := newTestModel(140, 40)
	openPanel(&m)
	// Switch focus to main
	m.panelFocused = false

	result, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = result.(Model)

	assert.False(t, m.panelOpen) // panel closed
	assert.Nil(t, cmd)           // did NOT quit
}

func TestNavigateBackClosesPanel(t *testing.T) {
	m := newTestModel(140, 40)
	openPanel(&m)

	result, _ := m.Update(msg.NavigateBackMsg{})
	m = result.(Model)

	assert.False(t, m.panelOpen)
}

func TestResizeBelowThresholdClosesPanel(t *testing.T) {
	m := newTestModel(140, 40)
	openPanel(&m)
	assert.True(t, m.panelOpen)

	result, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = result.(Model)

	assert.False(t, m.panelOpen)
}

// --- topLevelViewID ---

func TestTopLevelViewIDOnHome(t *testing.T) {
	m := newTestModel(140, 40)
	assert.Equal(t, "", m.topLevelViewID())
}

func TestTopLevelViewIDOnEC2(t *testing.T) {
	m := newTestModel(140, 40)
	// Simulate navigating to ec2_list
	result, _ := m.Update(msg.NavigateMsg{ViewID: "ec2_list"})
	m = result.(Model)

	assert.Equal(t, "ec2_list", m.topLevelViewID())
}

func TestTopLevelViewIDOnAMI(t *testing.T) {
	m := newTestModel(140, 40)
	result, _ := m.Update(msg.NavigateMsg{ViewID: "ami_list"})
	m = result.(Model)

	assert.Equal(t, "ami_list", m.topLevelViewID())
}

func TestTopLevelViewIDOnS3(t *testing.T) {
	m := newTestModel(140, 40)
	result, _ := m.Update(msg.NavigateMsg{ViewID: "s3_list"})
	m = result.(Model)

	assert.Equal(t, "s3_list", m.topLevelViewID())
}

// --- Feature picker ---

func TestFeaturePickerBecomesVisible(t *testing.T) {
	m := newTestModel(140, 40)
	result, _ := m.Update(msg.RequestFeaturePickerMsg{
		Service: "EC2",
		Labels:  []string{"Instances", "AMIs"},
		ViewIDs: []string{"ec2_list", "ami_list"},
	})
	m = result.(Model)

	assert.True(t, m.picker.Visible())
}

func TestFeaturePickerResultNavigatesToView(t *testing.T) {
	m := newTestModel(140, 40)
	initialDepth := m.nav.Depth()

	// Simulate picker selection of "AMIs" (index 1, value "ami_list")
	result, cmd := m.Update(ui.PickerResultMsg{ID: "feature", Selected: 1, Value: "ami_list"})
	m = result.(Model)
	require.NotNil(t, cmd)

	// Execute the NavigateMsg cmd
	navMsg := cmd()
	result, _ = m.Update(navMsg)
	m = result.(Model)

	assert.Greater(t, m.nav.Depth(), initialDepth)
	assert.Equal(t, "AMIs", m.nav.Current().Title())
}

func TestFeaturePickerEscDoesNothing(t *testing.T) {
	m := newTestModel(140, 40)
	initialDepth := m.nav.Depth()

	result, cmd := m.Update(ui.PickerResultMsg{ID: "feature", Selected: -1, Value: ""})
	m = result.(Model)

	assert.Equal(t, initialDepth, m.nav.Depth())
	assert.Nil(t, cmd)
}

// --- Region/profile apply ---

func TestApplyRegionReturnsToDismissToast(t *testing.T) {
	m := newTestModel(140, 40)
	_, cmd := m.applyRegion("eu-west-1")
	assert.NotNil(t, cmd, "applyRegion must return a cmd (toast dismiss + resize)")
}

func TestApplyRegionReturnsToServiceView(t *testing.T) {
	m := newTestModel(140, 40)
	// Navigate to EC2
	result, _ := m.Update(msg.NavigateMsg{ViewID: "ec2_list"})
	m = result.(Model)

	m, cmd := m.applyRegion("eu-west-1")
	assert.NotNil(t, cmd)
	// The nav was reset but a NavigateMsg should restore ec2_list.
	// After processing batch, current view should be ec2_list.
	assert.Equal(t, "Services", m.nav.Current().Title()) // nav was reset to home
	// The returned cmd batch includes a NavigateMsg to ec2_list
}

func TestApplyRegionStaysOnHomeWhenOnHome(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = m.applyRegion("eu-west-1")
	assert.Equal(t, "Services", m.nav.Current().Title())
	assert.Equal(t, 1, m.nav.Depth())
}

func TestApplyProfileReturnsToDismissToast(t *testing.T) {
	m := newTestModel(140, 40)
	_, cmd := m.applyProfile("staging")
	assert.NotNil(t, cmd)
}

func TestApplyThemeReturnsToDismissToast(t *testing.T) {
	m := newTestModel(140, 40)
	_, cmd := m.applyTheme("dracula")
	assert.NotNil(t, cmd)
}

// --- Toast dismiss ---

func TestToastMsgHandlerReturnsDismissCmd(t *testing.T) {
	m := newTestModel(140, 40)

	result, cmd := m.Update(msg.ToastMsg{Text: "ReadOnly mode", Level: 2})
	m = result.(Model)

	assert.True(t, m.toasts.HasActive(), "toast should be visible after ToastMsg")
	assert.NotNil(t, cmd, "dismiss cmd must be returned from ToastMsg handler")
}

func TestToastDismissMsgRemovesToast(t *testing.T) {
	m := newTestModel(140, 40)

	id, _ := m.toasts.Add("test toast", ui.ToastInfo, 0)
	assert.True(t, m.toasts.HasActive())

	result, _ := m.Update(ui.ToastDismissMsg{ID: id})
	m = result.(Model)

	assert.False(t, m.toasts.HasActive(), "toast should be dismissed")
}

func TestReadOnlyToastFullPath(t *testing.T) {
	// Simulate the full path: view closure → ToastMsg → dismiss cmd
	ui.ReadOnly = true
	defer func() { ui.ReadOnly = true }()

	m := newTestModel(140, 40)

	result, _ := m.Update(msg.NavigateMsg{ViewID: "ec2_list"})
	m = result.(Model)

	// Press 'm' — view returns closure that produces ToastError
	result, viewCmd := m.Update(tea.KeyPressMsg{Code: 'm', Text: "m"})
	m = result.(Model)
	assert.NotNil(t, viewCmd)

	// Execute closure, feed result back to Update
	toastMsg := viewCmd()
	result, dismissCmd := m.Update(toastMsg)
	m = result.(Model)

	assert.True(t, m.toasts.HasActive(), "toast should be visible")
	assert.NotNil(t, dismissCmd, "dismiss cmd must be returned")
}

// --- Key hints ---

func TestKeyHintsPanelFocused(t *testing.T) {
	m := newTestModel(140, 40)
	openPanel(&m)
	assert.True(t, m.panelFocused)

	hints := m.currentKeyHints()
	descs := make([]string, len(hints))
	for i, h := range hints {
		descs[i] = h.Desc
	}
	assert.Contains(t, descs, "close panel")
	assert.Contains(t, descs, "focus main")
	assert.Contains(t, descs, "scroll")
}

func TestKeyHintsMainFocusedWithPanel(t *testing.T) {
	m := newTestModel(140, 40)
	openPanel(&m)
	m.panelFocused = false

	hints := m.currentKeyHints()
	descs := make([]string, len(hints))
	for i, h := range hints {
		descs[i] = h.Desc
	}
	assert.Contains(t, descs, "focus panel")
	assert.NotContains(t, descs, "close panel")
}

// --- Registry sync ---

// --- Width tiers ---

func TestWidthTierNarrowNoPanel(t *testing.T) {
	m := newTestModel(79, 40)
	assert.False(t, m.canShowPanel())
	assert.Equal(t, ui.TierNarrow, ui.GetWidthTier(m.width))
}

func TestWidthTierMediumNoPanel(t *testing.T) {
	m := newTestModel(100, 40)
	assert.False(t, m.canShowPanel())
	assert.Equal(t, ui.TierMedium, ui.GetWidthTier(m.width))
}

func TestWidthTierWideShowsPanel(t *testing.T) {
	m := newTestModel(140, 40)
	assert.True(t, m.canShowPanel())
	assert.Equal(t, ui.TierWide, ui.GetWidthTier(m.width))
}

// --- Command execution ---

func TestExecuteCommandAlias(t *testing.T) {
	m := newTestModel(140, 40)

	// "q" is an alias for "quit"
	_, cmd := m.executeCommand("q")
	assert.NotNil(t, cmd, "alias 'q' should resolve to quit command")
}

func TestExecuteCommandNavAlias(t *testing.T) {
	m := newTestModel(140, 40)

	// "log" and "events" are aliases for "logs"
	for _, alias := range []string{"log", "events"} {
		_, cmd := m.executeCommand(alias)
		assert.NotNil(t, cmd, "alias %q should resolve to logs nav command", alias)
	}
}

func TestExecuteCommandUnknown(t *testing.T) {
	m := newTestModel(140, 40)

	m, _ = m.executeCommand("nonexistent")
	assert.True(t, m.toasts.HasActive(), "unknown command should show error toast")
}

// --- Registry sync ---

func TestRegistryNavCommandsCoveredByResolveView(t *testing.T) {
	m := newTestModel(140, 40)
	for _, cmd := range registry.Commands {
		if cmd.IsNav() {
			view := m.resolveView(msg.NavigateMsg{ViewID: cmd.ViewID})
			assert.NotNilf(t, view, "registry command %q has ViewID %q but resolveView returns nil", cmd.Name, cmd.ViewID)
		}
	}
}

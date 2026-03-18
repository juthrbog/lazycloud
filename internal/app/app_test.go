package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/juthrbog/lazycloud/internal/config"
	"github.com/juthrbog/lazycloud/internal/msg"
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

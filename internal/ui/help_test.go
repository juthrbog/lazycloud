package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
)

func init() {
	RebuildStyles()
}

func testHints() []KeyHint {
	return []KeyHint{
		{Key: "enter", Desc: "select"},
		{Key: "m", Desc: "manage", Mode: ModeReadWrite},
		{Key: "/", Desc: "filter"},
		{Key: "W", Desc: "mode", Category: "Global"},
		{Key: "q", Desc: "quit", Category: "Global"},
		{Key: "j/k", Desc: "scroll", Category: "Panel"},
	}
}

func TestHelpOverlayShowAndHide(t *testing.T) {
	h := NewHelpOverlay()
	assert.False(t, h.Visible())

	h.Show(testHints(), 120, 40)
	assert.True(t, h.Visible())

	h.Hide()
	assert.False(t, h.Visible())
}

func TestHelpOverlayDismissWithQuestionMark(t *testing.T) {
	h := NewHelpOverlay()
	h.Show(testHints(), 120, 40)

	h, _ = h.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	assert.False(t, h.Visible())
}

func TestHelpOverlayDismissWithEsc(t *testing.T) {
	h := NewHelpOverlay()
	h.Show(testHints(), 120, 40)

	h, _ = h.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, h.Visible())
}

func TestHelpOverlayFilter(t *testing.T) {
	h := NewHelpOverlay()
	h.Show(testHints(), 120, 40)

	// Type "quit" to filter
	h, _ = h.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	assert.Equal(t, "q", h.filter)

	content := h.renderContent()
	assert.Contains(t, content, "quit")
	assert.NotContains(t, content, "select")
}

func TestHelpOverlayFilterBackspace(t *testing.T) {
	h := NewHelpOverlay()
	h.Show(testHints(), 120, 40)

	h, _ = h.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	assert.Equal(t, "q", h.filter)

	h, _ = h.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	assert.Equal(t, "", h.filter)
}

func TestHelpOverlayRenderContentGroupsCategories(t *testing.T) {
	h := NewHelpOverlay()
	h.Show(testHints(), 120, 40)

	content := h.renderContent()
	assert.Contains(t, content, "Current View")
	assert.Contains(t, content, "Global")
	assert.Contains(t, content, "Panel")
}

func TestHelpOverlayRenderContentShowsRWBadge(t *testing.T) {
	h := NewHelpOverlay()
	h.Show(testHints(), 120, 40)

	content := h.renderContent()
	assert.Contains(t, content, "[RW]")
}

func TestHelpOverlayViewEmptyWhenHidden(t *testing.T) {
	h := NewHelpOverlay()
	assert.Equal(t, "", h.View())
}

func TestHelpOverlayNoMatchesFilter(t *testing.T) {
	h := NewHelpOverlay()
	h.Show(testHints(), 120, 40)

	h, _ = h.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	h, _ = h.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	h, _ = h.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})

	content := h.renderContent()
	assert.Contains(t, content, "no matches")
}

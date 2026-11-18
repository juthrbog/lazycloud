package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	RebuildStyles()
}

func testHints() []HintBinding {
	return []HintBinding{
		NewHintBinding([]string{"enter"}, "enter", "select"),
		NewHintBinding([]string{"m"}, "m", "manage").WithMode(ModeReadWrite),
		NewHintBinding([]string{"/"}, "/", "filter"),
		NewHintBinding([]string{"W"}, "W", "mode").WithCategory("Global"),
		NewHintBinding([]string{"q"}, "q", "quit").WithCategory("Global"),
		NewHintBinding([]string{"j", "k"}, "j/k", "scroll").WithCategory("Panel"),
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
	assert.Equal(t, "q", h.filter.String())

	content := h.renderContent()
	assert.Contains(t, content, "quit")
	assert.NotContains(t, content, "select")
}

func TestHelpOverlayFilterBackspace(t *testing.T) {
	h := NewHelpOverlay()
	h.Show(testHints(), 120, 40)

	h, _ = h.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	assert.Equal(t, "q", h.filter.String())

	h, _ = h.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	assert.Empty(t, h.filter.String())
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
	assert.Empty(t, h.View())
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

// When help gets wider while it is scrolled to the right, lines that now
// fit start at column 0 again, so no key name is cut.
func TestHelpOverlaySetSizeResetsTheSideScroll(t *testing.T) {
	hints := []HintBinding{
		NewHintBinding([]string{"ctrl+x"}, "ctrl+x", "cancel an operation"),
		NewHintBinding([]string{"tab"}, "tab", "toggle panel focus"),
	}
	var h HelpOverlay
	h.Show(hints, 54, 40)
	h, _ = h.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Positive(t, h.viewport.XOffset(), "the narrow help scrolled to the right")

	h.SetSize(120, 40)
	assert.Zero(t, h.viewport.XOffset())
	assert.Contains(t, ansi.Strip(h.View()), "ctrl+x")
}

// SetHints replaces the keys of an open help overlay and keeps the filter.
func TestHelpOverlaySetHints(t *testing.T) {
	h := NewHelpOverlay()
	h.SetHints(testHints())
	assert.False(t, h.Visible(), "SetHints does not open help")

	h.Show(testHints(), 120, 40)
	h, _ = h.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.Contains(t, ansi.Strip(h.View()), "scroll")

	h.SetHints(testHints()[:5])

	assert.Equal(t, "s", h.filter.String(), "the filter stays")
	assert.NotContains(t, ansi.Strip(h.View()), "scroll")
	assert.Contains(t, ansi.Strip(h.View()), "select")
}

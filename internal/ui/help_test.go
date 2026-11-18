package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	RebuildStyles()
}

var slashKey = tea.KeyPressMsg{Code: '/', Text: "/"}

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
	h, _ = h.Update(slashKey)

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
	h, _ = h.Update(slashKey)

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
	h, _ = h.Update(slashKey)

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
	h, _ = h.Update(slashKey)
	h, _ = h.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	require.Contains(t, ansi.Strip(h.viewport.View()), "scroll")

	h.SetHints(testHints()[:5])

	assert.Equal(t, "s", h.filter.String(), "the filter stays")
	assert.NotContains(t, ansi.Strip(h.viewport.View()), "scroll")
	assert.Contains(t, ansi.Strip(h.viewport.View()), "select")
}

// L9: without the filter, j and k scroll help, and q closes it.
func TestHelpOverlayScrollKeys(t *testing.T) {
	var hints []HintBinding
	for i := range 40 {
		hints = append(hints, NewHintBinding([]string{"x"}, fmt.Sprintf("key%02d", i), "desc"))
	}
	h := NewHelpOverlay()
	h.Show(hints, 120, 30)

	h, _ = h.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	h, _ = h.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	assert.Equal(t, 2, h.viewport.YOffset())
	assert.Empty(t, h.filter.String(), "j does not type")

	h, _ = h.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	assert.Equal(t, 1, h.viewport.YOffset())

	h, _ = h.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	assert.False(t, h.Visible())
}

// L9: / starts the filter. Then j, k, q and ? type. enter keeps the text
// and gives the keys back to scrolling. esc clears the text.
func TestHelpOverlayFilterMode(t *testing.T) {
	h := NewHelpOverlay()
	h.Show(testHints(), 120, 40)
	h, _ = h.Update(slashKey)
	require.Contains(t, ansi.Strip(h.View()), "esc clear filter")

	for _, r := range "jq?" {
		h, _ = h.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	assert.True(t, h.Visible())
	assert.Equal(t, "jq?", h.filter.String())

	h, _ = h.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	h, _ = h.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	h, _ = h.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	h, _ = h.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	h, _ = h.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, "q", h.filter.String(), "enter keeps the text")
	assert.Contains(t, ansi.Strip(h.View()), "/ q")
	assert.Contains(t, ansi.Strip(h.View()), "/ filter", "the keys scroll again")
	assert.NotContains(t, h.renderContent(), "select")

	h, _ = h.Update(slashKey)
	h, _ = h.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.True(t, h.Visible(), "esc in the filter does not close help")
	assert.Empty(t, h.filter.String())
	assert.Contains(t, ansi.Strip(h.viewport.View()), "select", "the list shows all keys again")

	h, _ = h.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, h.Visible())
}

// L9: Show starts without the filter.
func TestHelpOverlayShowEndsTheFilter(t *testing.T) {
	h := NewHelpOverlay()
	h.Show(testHints(), 120, 40)
	h, _ = h.Update(slashKey)
	h.Show(testHints(), 120, 40)

	h, _ = h.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	assert.False(t, h.Visible())
}

// L9: categories that help does not know come after the known ones, in the
// order of their names, so the order does not change between renders.
func TestHelpOverlayUnknownCategoriesHaveAFixedOrder(t *testing.T) {
	hints := []HintBinding{
		NewHintBinding([]string{"z"}, "z", "zulu").WithCategory("Zulu"),
		NewHintBinding([]string{"g"}, "g", "global").WithCategory("Global"),
		NewHintBinding([]string{"a"}, "a", "alpha").WithCategory("Alpha"),
		NewHintBinding([]string{"v"}, "v", "view"),
		NewHintBinding([]string{"n"}, "n", "nav").WithCategory("Navigation"),
	}
	want := []string{"Current View", "Navigation", "Global", "Alpha", "Zulu"}
	for range 20 {
		h := NewHelpOverlay()
		h.Show(hints, 120, 40)
		content := ansi.Strip(h.renderContent())
		last := -1
		for _, name := range want {
			i := strings.Index(content, name)
			require.Greaterf(t, i, last, "%q comes after the groups before it", name)
			last = i
		}
	}
}

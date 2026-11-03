package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pickerOptions makes n options named "opt-00", "opt-01", and so on.
func pickerOptions(n int) []PickerOption {
	opts := make([]PickerOption, n)
	for i := range opts {
		name := fmt.Sprintf("opt-%02d", i)
		opts[i] = PickerOption{Label: name, Value: name}
	}
	return opts
}

func pressKey(p Picker, code rune) Picker {
	p, _ = p.Update(tea.KeyPressMsg{Code: code})
	return p
}

func typeText(p Picker, text string) Picker {
	for _, r := range text {
		p, _ = p.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return p
}

// cursorLine returns the option label on the cursor row of the view.
func cursorLine(t *testing.T, view string) string {
	t.Helper()
	for line := range strings.SplitSeq(ansi.Strip(view), "\n") {
		if _, after, ok := strings.Cut(line, "▸ "); ok {
			return strings.Fields(after)[0]
		}
	}
	t.Fatalf("no cursor row in view:\n%s", view)
	return ""
}

// The 23 regions made a dialog of 30 rows, which a 24-row terminal cut off.
func TestPickerViewFitsMaxHeight(t *testing.T) {
	for _, h := range []int{10, 12, 24, 40} {
		t.Run(fmt.Sprint(h), func(t *testing.T) {
			p := NewPicker()
			p.SetMaxHeight(h)
			p.Show("region", "Select Region", pickerOptions(23), 0)
			assert.LessOrEqual(t, lipgloss.Height(p.View()), h)
		})
	}
}

func TestPickerShowsAllOptionsWhenTheyFit(t *testing.T) {
	p := NewPicker()
	p.SetMaxHeight(40)
	p.Show("region", "Select Region", pickerOptions(23), 0)
	view := p.View()
	assert.Contains(t, view, "opt-00")
	assert.Contains(t, view, "opt-22")
	assert.NotContains(t, view, "more")
	assert.Equal(t, 23+pickerFullChrome, lipgloss.Height(view))
}

// Short lists, such as the mode picker, keep the full layout.
func TestPickerFewOptionsDoNotScroll(t *testing.T) {
	p := NewPicker()
	p.SetMaxHeight(12)
	p.Show("mode", "Access Mode", pickerOptions(3), 2)
	view := p.View()
	assert.Equal(t, "opt-02", cursorLine(t, view))
	assert.NotContains(t, view, "more")
	assert.Equal(t, 3+pickerFullChrome, lipgloss.Height(view))
}

func TestPickerNoLimitShowsAllOptions(t *testing.T) {
	p := NewPicker()
	p.Show("region", "Select Region", pickerOptions(23), 0)
	assert.Equal(t, 23+pickerFullChrome, lipgloss.Height(p.View()))
}

func TestPickerShowScrollsToInitialIndex(t *testing.T) {
	p := NewPicker()
	p.SetMaxHeight(24)
	p.Show("region", "Select Region", pickerOptions(23), 20)
	view := p.View()
	assert.Equal(t, "opt-20", cursorLine(t, view))
	assert.Regexp(t, `↑ \d+ more`, view)
	assert.NotContains(t, view, "opt-00")
}

func TestPickerCursorStaysVisibleWhenMoving(t *testing.T) {
	p := NewPicker()
	p.SetMaxHeight(24)
	p.Show("region", "Select Region", pickerOptions(23), 0)

	view := p.View()
	assert.Contains(t, view, "↓ 8 more") // 24 - 7 chrome - 2 more lines = 15 rows
	assert.NotRegexp(t, `↑ \d+ more`, view)

	for i := 1; i < 23; i++ {
		p = pressKey(p, tea.KeyDown)
		view = p.View()
		require.Equal(t, fmt.Sprintf("opt-%02d", i), cursorLine(t, view), "after %d downs", i)
		require.LessOrEqual(t, lipgloss.Height(view), 24)
	}
	assert.Contains(t, view, "↑ 8 more")
	assert.NotRegexp(t, `↓ \d+ more`, view)

	// Going back up keeps the window still until the cursor reaches its top.
	p = pressKey(p, tea.KeyUp)
	assert.Contains(t, p.View(), "↑ 8 more")
	for i := 21; i >= 0; i-- {
		require.Equal(t, fmt.Sprintf("opt-%02d", i), cursorLine(t, p.View()))
		p = pressKey(p, tea.KeyUp)
	}
	assert.NotRegexp(t, `↑ \d+ more`, p.View())
}

func TestPickerFilterResetsWindow(t *testing.T) {
	p := NewPicker()
	p.SetMaxHeight(24)
	p.Show("region", "Select Region", pickerOptions(23), 22)
	p = typeText(p, "opt-1")
	view := p.View()
	assert.Equal(t, "opt-10", cursorLine(t, view))
	assert.NotContains(t, view, "more")

	// Backspace widens the list again, and the cursor is back on top.
	p = pressKey(p, tea.KeyBackspace)
	view = p.View()
	assert.Equal(t, "opt-00", cursorLine(t, view))
	assert.NotRegexp(t, `↑ \d+ more`, view)
}

func TestPickerPageAndEndKeys(t *testing.T) {
	p := NewPicker()
	p.SetMaxHeight(24)
	p.Show("region", "Select Region", pickerOptions(23), 0)

	p = pressKey(p, tea.KeyPgDown)
	assert.Equal(t, "opt-15", cursorLine(t, p.View()))
	p = pressKey(p, tea.KeyEnd)
	assert.Equal(t, "opt-22", cursorLine(t, p.View()))
	p = pressKey(p, tea.KeyPgUp)
	assert.Equal(t, "opt-07", cursorLine(t, p.View()))
	p = pressKey(p, tea.KeyHome)
	assert.Equal(t, "opt-00", cursorLine(t, p.View()))
}

func TestPickerShrinkKeepsCursorVisible(t *testing.T) {
	p := NewPicker()
	p.SetMaxHeight(40)
	p.Show("region", "Select Region", pickerOptions(23), 22)
	p.SetMaxHeight(14)
	view := p.View()
	assert.Equal(t, "opt-22", cursorLine(t, view))
	assert.LessOrEqual(t, lipgloss.Height(view), 14)
}

func TestPickerTinyHeight(t *testing.T) {
	for _, h := range []int{1, 3, 5, 6, 7, 8, 9} {
		t.Run(fmt.Sprint(h), func(t *testing.T) {
			p := NewPicker()
			p.SetMaxHeight(h)
			p.Show("region", "Select Region", pickerOptions(23), 12)
			view := p.View()
			assert.Equal(t, "opt-12", cursorLine(t, view))
			// The title, filter line, and one option row always show.
			assert.LessOrEqual(t, lipgloss.Height(view), max(h, pickerCompactChrome+1))
		})
	}
}

func TestPickerNoMatchesFits(t *testing.T) {
	p := NewPicker()
	p.SetMaxHeight(10)
	p.Show("region", "Select Region", pickerOptions(23), 0)
	p = typeText(p, "zzz")
	view := p.View()
	assert.Contains(t, view, "no matches")
	assert.LessOrEqual(t, lipgloss.Height(view), 10)

	// Enter with no matches does nothing.
	p, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, cmd)
	assert.True(t, p.Visible())
}

func TestPickerSelectAfterScroll(t *testing.T) {
	p := NewPicker()
	p.SetMaxHeight(12)
	p.Show("region", "Select Region", pickerOptions(23), 0)
	for range 18 {
		p = pressKey(p, tea.KeyDown)
	}
	_, cmd := p.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	res, ok := cmd().(PickerResultMsg)
	require.True(t, ok)
	assert.Equal(t, 18, res.Selected)
	assert.Equal(t, "opt-18", res.Value)
}

// A long label must not make the box change width while the list scrolls.
// The app centers the box, so a width change makes the whole dialog move.
func TestPickerWidthStableWhileScrolling(t *testing.T) {
	opts := pickerOptions(23)
	opts[0].Label = strings.Repeat("x", 70)
	p := NewPicker()
	p.SetMaxHeight(15)
	p.Show("id", "Pick", opts, 0)
	want := lipgloss.Width(p.View())
	for range 22 {
		p = pressKey(p, tea.KeyDown)
		require.Equal(t, want, lipgloss.Width(p.View()), "cursor %d", p.cursor)
	}
	assert.NotContains(t, ansi.Strip(p.View()), strings.Repeat("x", 70), "long label scrolled off")
}

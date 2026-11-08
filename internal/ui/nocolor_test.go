package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

// withNoColor runs the test with colors off and turns them on again after.
func withNoColor(t *testing.T) {
	t.Helper()
	SetNoColor(true)
	t.Cleanup(func() { SetNoColor(false) })
}

var reverseSGR = ansi.NewStyle().Reverse(true).String()

// M20: without colors, a background shows nothing. The selected table row
// uses reverse video, so it still stands out.
func TestNoColorTableCursorIsReverse(t *testing.T) {
	withNoColor(t)
	assert.True(t, tableStyles(ActiveTheme).Selected.GetReverse())
}

// With colors, the cursor keeps the background of the theme.
func TestColorTableCursorIsNotReverse(t *testing.T) {
	assert.False(t, tableStyles(ActiveTheme).Selected.GetReverse())
}

// M20: the content cursor uses reverse video without colors.
func TestNoColorContentCursorIsReverse(t *testing.T) {
	withNoColor(t)
	cv := NewContentView("notes.txt", "one\ntwo", FormatText)
	cv.SetSize(40, 10)

	assert.Contains(t, cv.View(), reverseSGR)
}

func TestColorContentCursorIsNotReverse(t *testing.T) {
	cv := NewContentView("notes.txt", "one\ntwo", FormatText)
	cv.SetSize(40, 10)

	assert.NotContains(t, cv.View(), reverseSGR)
}

// M20: the selected picker option uses reverse video without colors.
func TestNoColorPickerSelectionIsReverse(t *testing.T) {
	withNoColor(t)
	assert.True(t, S.PickerOptionSelected.GetReverse())
	assert.True(t, S.TableSelected.GetReverse())
}

func TestColorPickerSelectionIsNotReverse(t *testing.T) {
	assert.False(t, S.PickerOptionSelected.GetReverse())
}

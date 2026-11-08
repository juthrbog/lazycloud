package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const longKey = "Downloaded s3://media-archive-prod/exports/2026/11/customer-invoices/region=eu-west-1/batch-000123/invoice-2026-11-08T03-30-25Z-0001.json.gz to ~/Downloads/invoice-2026-11-08T03-30-25Z-0001.json.gz"

func toastView(t *testing.T, text string, width int) string {
	t.Helper()
	tm := NewToastManager()
	tm.Add(text, ToastSuccess, 0)
	out := tm.View(width)
	require.NotEmpty(t, out)
	return out
}

// plainText returns the toast text without the border, the icon, and the
// line breaks, so a test can compare it with the text it added.
func plainText(view string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(ansi.Strip(view), "\n") {
		line = strings.Trim(line, "│╭╮╰╯─ ")
		line = strings.TrimPrefix(line, "✓ ")
		b.WriteString(strings.TrimSpace(line))
	}
	return b.String()
}

// L27, L11: the app puts the toast box at the right edge, two columns in
// from it. A long toast must fit in the width, or it runs past the screen.
func TestLongToastFitsTheWidth(t *testing.T) {
	for _, width := range []int{150, 100, 80, 60, 40} {
		out := toastView(t, longKey, width)
		assert.LessOrEqual(t, lipgloss.Width(out), width-2, "width %d", width)
	}
}

// The end of a long key or path is often the part that matters, so wrapping
// keeps all of the text when it fits in a few lines.
func TestLongToastKeepsAllText(t *testing.T) {
	out := toastView(t, longKey, 100)

	assert.Equal(t, strings.ReplaceAll(longKey, " ", ""), strings.ReplaceAll(plainText(out), " ", ""))
	assert.Greater(t, lipgloss.Height(out), 3, "the text wraps onto more lines")
}

// Wrapped lines start under the text, not under the icon.
func TestWrappedToastLinesAlignWithText(t *testing.T) {
	lines := strings.Split(ansi.Strip(toastView(t, longKey, 80)), "\n")
	require.Greater(t, len(lines), 3)

	first := textColumn(strings.TrimPrefix(lines[1], "│"), "✓ ")
	require.Positive(t, first)
	for _, line := range lines[2 : len(lines)-1] {
		assert.Equal(t, first, textColumn(strings.TrimPrefix(line, "│"), ""), "line %q", line)
	}
}

// textColumn returns the screen column where the text of a box line starts,
// after skip and the spaces before it.
func textColumn(line, skip string) int {
	i := strings.IndexFunc(line, func(r rune) bool { return r != ' ' })
	if skip != "" && strings.HasPrefix(line[i:], skip) {
		i += len(skip)
	}
	return ansi.StringWidth(line[:i])
}

// On a narrow terminal, one toast must not cover the screen. It shows at
// most maxToastLines lines and ends with an ellipsis.
func TestToastLinesAreLimited(t *testing.T) {
	out := toastView(t, strings.Repeat(longKey+" ", 4), 40)

	assert.LessOrEqual(t, lipgloss.Height(out), maxToastLines+2, "lines plus the border")
	assert.Contains(t, ansi.Strip(out), "…")
}

// A short toast stays on one line.
func TestShortToastIsOneLine(t *testing.T) {
	out := toastView(t, "Theme: Nord", 150)

	assert.Equal(t, 3, lipgloss.Height(out))
	assert.Contains(t, ansi.Strip(out), "✓ Theme: Nord")
}

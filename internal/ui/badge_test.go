package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
)

func plainHeader(d HeaderData) string { return ansi.Strip(RenderHeader(d)) }

// useFonts sets UseNerdFonts for one test.
func useFonts(t *testing.T, on bool) {
	t.Helper()
	before := UseNerdFonts
	UseNerdFonts = on
	t.Cleanup(func() { UseNerdFonts = before })
}

func TestModeBadge(t *testing.T) {
	useFonts(t, false)
	assert.Equal(t, "  RO  ", ansi.Strip(ModeBadge("RO", false)), "HeaderStyle adds its own padding")
	assert.Equal(t, "  RO*  ", ansi.Strip(ModeBadge("RO", true)))
	assert.Equal(t, " RW ", ansi.Strip(ModeBadge("RW", false)))
	assert.Equal(t, " RW ", ansi.Strip(ModeBadge("RW", true)), "RW cannot be locked")

	useFonts(t, true)
	assert.Equal(t, "  RO \U000f033e  ", ansi.Strip(ModeBadge("RO", true)))
}

func TestLabelBadge(t *testing.T) {
	assert.Empty(t, LabelBadge("", true))
	assert.Equal(t, " PROD ", ansi.Strip(LabelBadge("PROD", true)))
	assert.NotContains(t, ansi.Strip(LabelBadge("a\x1b[31mb", false)), "\x1b", "the label is sanitized")
	assert.NotEqual(t, LabelBadge("PROD", true), LabelBadge("PROD", false), "a locking label has its own color")
}

// The mode is on screen at every width, and the label comes before the
// profile.
func TestHeaderShowsTheModeAndTheLabel(t *testing.T) {
	useFonts(t, false)
	for _, w := range []int{40, 50, 70, 100, 130} {
		got := plainHeader(HeaderData{Profile: "prod-admin", Region: "eu-west-1", Mode: "RO", Locked: true, Label: "PROD", LabelLocks: true, Width: w})
		assert.Contains(t, got, " RO* ", "width %d", w)
		assert.Contains(t, got, " PROD ", "width %d", w)
	}
	got := plainHeader(HeaderData{Profile: "prod-admin", Region: "eu-west-1", Mode: "RO", Label: "PROD", Width: 100})
	assert.Less(t, strings.Index(got, "PROD"), strings.Index(got, "prod-admin"))
	assert.Contains(t, got, "eu-west-1")
	got = plainHeader(HeaderData{Profile: "prod-admin", Region: "eu-west-1", Mode: "RO", Label: "PROD", Width: 70})
	assert.Less(t, strings.Index(got, "PROD"), strings.Index(got, "prod-admin"))

	narrow := plainHeader(HeaderData{Profile: "p", Mode: "RW", Breadcrumbs: []string{"S3", "bucket"}, Width: 50})
	assert.Contains(t, narrow, " RW ")
	assert.Contains(t, narrow, "bucket")
	assert.NotContains(t, narrow, "PROD")
	assert.LessOrEqual(t, ansi.StringWidth(strings.Split(RenderHeader(HeaderData{Mode: "RO", Label: "PROD", Breadcrumbs: []string{"a-very-long-breadcrumb", "another-long-breadcrumb"}, Width: 50}), "\n")[0]), 50)
}

// The status bar starts with the session badge, also with an error, and
// keeps its width.
func TestStatusBarSessionBadge(t *testing.T) {
	useFonts(t, false)
	badge := LabelBadge("PROD", true) + " " + ModeBadge("RO", true)
	keys := []HintBinding{NewHintBinding([]string{"q"}, "q", "quit")}

	got := RenderStatusBar(StatusBarData{Keys: keys, Width: 60, Session: badge})
	assert.Equal(t, 60, ansi.StringWidth(got))
	assert.True(t, strings.HasPrefix(ansi.Strip(got), " PROD    RO*   q quit"), ansi.Strip(got))

	got = RenderStatusBar(StatusBarData{Error: "boom", Width: 60, Session: badge})
	assert.Equal(t, 60, ansi.StringWidth(got))
	assert.True(t, strings.HasPrefix(ansi.Strip(got), " PROD    RO*    boom "), ansi.Strip(got))

	got = RenderStatusBar(StatusBarData{Keys: keys, Width: 60})
	assert.Equal(t, 60, ansi.StringWidth(got))
	assert.NotContains(t, ansi.Strip(got), "RO")
}

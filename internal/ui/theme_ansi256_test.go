package ui

import (
	"image/color"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"
)

// isGray reports whether a color has no hue.
func isGray(c color.Color) bool {
	r, g, b, _ := c.RGBA()
	return r == g && g == b
}

// A terminal without truecolor gets the nearest of 256 colors. That color
// can be a gray: the official red of Catppuccin Latte, #d20f39, becomes 241
// (#626262), so errors and stopped instances looked gray. Each status color
// must keep a hue.
func TestThemeColorsKeepTheirHueIn256Colors(t *testing.T) {
	for name, theme := range Themes {
		for role, c := range map[string]color.Color{
			"Primary":      theme.Primary,
			"Accent":       theme.Accent,
			"Error":        theme.Error,
			"Warning":      theme.Warning,
			"Success":      theme.Success,
			"Info":         theme.Info,
			"StateRunning": theme.StateRunning,
			"StateStopped": theme.StateStopped,
			"StatePending": theme.StatePending,
		} {
			got := colorprofile.ANSI256.Convert(c)
			assert.False(t, isGray(got), "%s %s becomes the gray %v with 256 colors", name, role, got)
		}
	}
}

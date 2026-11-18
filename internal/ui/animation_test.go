package ui

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// P3: halfBlocks writes a color sequence only when the colors change, and
// resets the colors at the end of each line.
func TestHalfBlocks(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}
	// 3 x 2 cells, 3 x 4 pixels. The first two cells of row 0 are the same.
	pixels := [][]color.Color{
		{red, red, blue},
		{blue, blue, blue},
		{red, red, red},
		{red, red, red},
	}

	got := halfBlocks(3, 2, func(x, y int) color.Color { return pixels[y][x] })

	want := "\x1b[38;2;255;0;0;48;2;0;0;255m▀▀\x1b[38;2;0;0;255;48;2;0;0;255m▀\x1b[m\n" +
		"\x1b[38;2;255;0;0;48;2;255;0;0m▀▀▀\x1b[m"
	assert.Equal(t, want, got)
}

func animations() map[string]func() Animation {
	return map[string]func() Animation{
		"cloud": func() Animation { return NewCloudAnimation(7) },
		"storm": func() Animation { return NewStormAnimation(7) },
	}
}

// P3: View draws the frame and moves nothing. Two calls give the same text.
// Update moves the frame on.
func TestAnimationMovesOnlyInUpdate(t *testing.T) {
	for name, build := range animations() {
		t.Run(name, func(t *testing.T) {
			a := build()
			a.Update(80, 20)
			first := a.View(80, 20)
			for range 5 {
				require.Equal(t, first, a.View(80, 20), "View does not move the frame")
			}

			changed := false
			for range 40 {
				a.Update(80, 20)
				if a.View(80, 20) != first {
					changed = true
					break
				}
			}
			assert.True(t, changed, "Update moves the frame")
		})
	}
}

// The motion does not depend on how often View runs: the same frames come
// out with one View per frame and with many.
func TestAnimationSpeedDoesNotDependOnRedraws(t *testing.T) {
	for name, build := range animations() {
		t.Run(name, func(t *testing.T) {
			once, often := build(), build()
			for range 30 {
				once.Update(80, 20)
				often.Update(80, 20)
				for range 3 {
					often.View(80, 20)
				}
			}
			assert.Equal(t, once.View(80, 20), often.View(80, 20))
		})
	}
}

// Each frame fills the area: height lines of width cells.
func TestAnimationFillsTheArea(t *testing.T) {
	for name, build := range animations() {
		t.Run(name, func(t *testing.T) {
			a := build()
			a.Update(50, 12)
			lines := strings.Split(a.View(50, 12), "\n")
			require.Len(t, lines, 12)
			for _, l := range lines {
				assert.Equal(t, 50, lipgloss.Width(l))
				assert.Equal(t, strings.Repeat("▀", 50), ansi.Strip(l))
			}
			assert.Empty(t, a.View(0, 12))
		})
	}
}

// A theme switch draws the frame again with the new colors, also without a
// new frame.
func TestAnimationRedrawsAfterAThemeSwitch(t *testing.T) {
	for name, build := range animations() {
		t.Run(name, func(t *testing.T) {
			useTheme(t, Catppuccin)
			a := build()
			a.Update(80, 20)
			require.Contains(t, a.View(80, 20), fg(Catppuccin.Base)[3:], "the sky")

			useTheme(t, Nord)

			assert.Contains(t, a.View(80, 20), fg(Nord.Base)[3:])
			assert.NotContains(t, a.View(80, 20), fg(Catppuccin.Base)[3:])
		})
	}
}

// The speed label shows when the speed is not the default.
func TestAnimationShowsTheSpeed(t *testing.T) {
	for name, build := range animations() {
		t.Run(name, func(t *testing.T) {
			a := build()
			a.Update(80, 20)
			require.NotContains(t, ansi.Strip(a.View(80, 20)), "x ")

			a.SpeedUp()

			assert.Contains(t, ansi.Strip(a.View(80, 20)), " 1.2x ")
		})
	}
}

package ui

import (
	"image/color"
	"math"
	"testing"

	"github.com/alecthomas/chroma/v2/styles"
	"github.com/stretchr/testify/assert"
)

// luminance returns the relative luminance of a color (WCAG 2).
func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	f := func(v uint32) float64 {
		x := float64(v) / 0xffff
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*f(r) + 0.7152*f(g) + 0.0722*f(b)
}

// contrast returns the WCAG contrast ratio of two colors, from 1 to 21.
func contrast(a, b color.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// M20: the text of a light theme must be readable on its light background
// and on its badges. The official yellow of Catppuccin Latte has 2.3:1 on
// its base, and its green 3.0:1, so the theme uses darker shades of both.
func TestLightThemesHaveReadableText(t *testing.T) {
	const minContrast = 3.0
	pairs := map[string]func(Theme) (fg, bg color.Color){
		"text on base":        func(t Theme) (color.Color, color.Color) { return t.Text, t.Base },
		"subtext on base":     func(t Theme) (color.Color, color.Color) { return t.SubText, t.Base },
		"muted on base":       func(t Theme) (color.Color, color.Color) { return t.Muted, t.Base },
		"bright on base":      func(t Theme) (color.Color, color.Color) { return t.BrightText, t.Base },
		"primary on base":     func(t Theme) (color.Color, color.Color) { return t.Primary, t.Base },
		"accent on base":      func(t Theme) (color.Color, color.Color) { return t.Accent, t.Base },
		"error on base":       func(t Theme) (color.Color, color.Color) { return t.Error, t.Base },
		"warning on base":     func(t Theme) (color.Color, color.Color) { return t.Warning, t.Base },
		"info on base":        func(t Theme) (color.Color, color.Color) { return t.Info, t.Base },
		"pending on base":     func(t Theme) (color.Color, color.Color) { return t.StatePending, t.Base },
		"header badge":        func(t Theme) (color.Color, color.Color) { return t.BrightText, t.Primary },
		"region badge":        func(t Theme) (color.Color, color.Color) { return t.Base, t.Accent },
		"read-write badge":    func(t Theme) (color.Color, color.Color) { return t.Base, t.Warning },
		"status bar":          func(t Theme) (color.Color, color.Color) { return t.SubText, t.Surface },
		"cursor row":          func(t Theme) (color.Color, color.Color) { return t.BrightText, t.Overlay },
		"text on cursor row":  func(t Theme) (color.Color, color.Color) { return t.Text, t.Overlay },
		"form button":         func(t Theme) (color.Color, color.Color) { return t.BrightText, t.Accent },
		"breadcrumb on bar":   func(t Theme) (color.Color, color.Color) { return t.BrightText, t.Surface },
		"selection in visual": func(t Theme) (color.Color, color.Color) { return t.BrightText, t.Secondary },
	}
	for name, theme := range Themes {
		if !theme.Light {
			continue
		}
		for pair, colors := range pairs {
			fg, bg := colors(theme)
			assert.GreaterOrEqual(t, contrast(fg, bg), minContrast, "%s: %s", name, pair)
		}
	}
}

// Each theme names a chroma style that exists. Chroma falls back to another
// style for an unknown name, so a typo would give dark code on a light theme.
func TestThemesUseKnownChromaStyles(t *testing.T) {
	for name, theme := range Themes {
		assert.Contains(t, styles.Registry, theme.ChromaStyle, name)
	}
}

func TestEveryThemeIsInTheCycleOrder(t *testing.T) {
	assert.Len(t, ThemeOrder, len(Themes))
	for _, name := range ThemeOrder {
		assert.Contains(t, Themes, name)
	}
}

func TestAutoThemeNames(t *testing.T) {
	assert.False(t, Themes[AutoThemeName(true)].Light)
	assert.True(t, Themes[AutoThemeName(false)].Light)
}

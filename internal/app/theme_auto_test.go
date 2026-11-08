package app

import (
	"image/color"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/juthrbog/lazycloud/internal/ui"
)

var (
	lightBackground = tea.BackgroundColorMsg{Color: color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}}
	darkBackground  = tea.BackgroundColorMsg{Color: color.RGBA{R: 0x1e, G: 0x1e, B: 0x2e, A: 0xff}}
)

// autoModel returns a model with the given theme setting, and puts the
// default theme back after the test.
func autoModel(t *testing.T, setting string) Model {
	t.Helper()
	t.Cleanup(func() { ui.ActiveTheme = ui.DefaultTheme; ui.RebuildStyles() })
	m := newTestModel(140, 40)
	m.config.Display.Theme = setting
	m.autoTheme = setting == ui.AutoTheme
	return m
}

// M20: with the auto setting, a light terminal gets the light theme.
func TestAutoThemeOnLightTerminal(t *testing.T) {
	m := autoModel(t, ui.AutoTheme)

	m, _ = update(t, m, lightBackground)

	assert.Equal(t, ui.CatppuccinLatte.Name, ui.ActiveTheme.Name)
	assert.False(t, m.isDark)
}

func TestAutoThemeOnDarkTerminal(t *testing.T) {
	m := autoModel(t, ui.AutoTheme)

	update(t, m, darkBackground)

	assert.Equal(t, ui.Catppuccin.Name, ui.ActiveTheme.Name)
}

// The app picks the theme once. A later report does not switch the theme,
// because that would rebuild the views that the user opened.
func TestAutoThemePicksOnce(t *testing.T) {
	m := autoModel(t, ui.AutoTheme)

	m, _ = update(t, m, lightBackground)
	update(t, m, darkBackground)

	assert.Equal(t, ui.CatppuccinLatte.Name, ui.ActiveTheme.Name)
}

// A theme that the user set wins over the background.
func TestSetThemeIgnoresBackground(t *testing.T) {
	m := autoModel(t, "nord")
	ui.ActiveTheme = ui.Nord

	update(t, m, lightBackground)

	assert.Equal(t, ui.Nord.Name, ui.ActiveTheme.Name)
}

func TestNewModelReadsAutoSetting(t *testing.T) {
	assert.True(t, newTestModel(140, 40).autoTheme, "the default setting is auto")
}

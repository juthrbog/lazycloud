package views

import (
	"errors"
	"fmt"
	"image/color"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// fgSGR returns the truecolor foreground sequence of a color, as lipgloss
// writes it.
func fgSGR(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
}

// L10: the error text of a view uses the red of the active theme, also after
// a theme switch. The old ErrorStyle kept the red of the theme at startup.
func TestViewErrorUsesActiveThemeRed(t *testing.T) {
	t.Cleanup(func() { ui.ActiveTheme = ui.DefaultTheme; ui.RebuildStyles() })
	ui.ActiveTheme = ui.CatppuccinLatte
	ui.RebuildStyles()

	view, _ := newTestEC2List()
	view.Update(msg.ErrorMsg{Err: errors.New("access denied"), Context: "EC2"})
	out := view.View().Content

	assert.Contains(t, out, "access denied")
	assert.Contains(t, out, fgSGR(ui.CatppuccinLatte.Error))
	assert.NotContains(t, out, fgSGR(ui.Catppuccin.Error))
}

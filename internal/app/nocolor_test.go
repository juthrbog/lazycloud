package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/stretchr/testify/assert"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// M20: NO_COLOR gives the ASCII profile. The app then turns on the styles
// that show without colors.
func TestColorProfileSetsNoColor(t *testing.T) {
	t.Cleanup(func() { ui.SetNoColor(false) })
	for profile, want := range map[colorprofile.Profile]bool{
		colorprofile.ASCII:     true,
		colorprofile.NoTTY:     true,
		colorprofile.ANSI:      false,
		colorprofile.ANSI256:   false,
		colorprofile.TrueColor: false,
	} {
		m := newTestModel(140, 40)
		update(t, m, tea.ColorProfileMsg{Profile: profile})
		assert.Equal(t, want, ui.NoColor(), profile.String())
	}
}

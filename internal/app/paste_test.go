package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/views"
)

// pasteView records the pastes that reach it.
type pasteView struct {
	*views.EventLog
	pastes []string
}

func (v *pasteView) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	if p, ok := m.(tea.PasteMsg); ok {
		v.pastes = append(v.pastes, p.Content)
	}
	return v, nil
}

// A9: a paste goes to the overlay on top, like a key press. Before, it went
// to the view under the overlay, for example into a list filter.
func TestPasteGoesToTheOverlayOnTop(t *testing.T) {
	m := newTestModel(140, 40)
	v := &pasteView{EventLog: views.NewEventLog()}
	m.pushView(v)

	m, _ = update(t, m, tea.PasteMsg{Content: "first"})
	require.Equal(t, []string{"first"}, v.pastes, "with no overlay, the view gets the paste")

	m.showRegionPicker()
	require.True(t, m.picker.Visible())
	m, _ = update(t, m, tea.PasteMsg{Content: "eu-west-2\n"})
	assert.Equal(t, []string{"first"}, v.pastes, "the view under the picker gets nothing")
	screen := ansi.Strip(rendered(m))
	assert.Contains(t, screen, "/ eu-west-2")
	assert.NotContains(t, screen, "us-east-2", "the picker filtered its options")

	// A confirm has no text input: it drops the paste.
	m, _ = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	require.False(t, m.picker.Visible())
	m.askQuick("Continue?", nil, nil)
	require.True(t, m.confirm.Visible())
	m, _ = update(t, m, tea.PasteMsg{Content: "y"})
	assert.True(t, m.confirm.Visible())
	assert.Equal(t, []string{"first"}, v.pastes)
}

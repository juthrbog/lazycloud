package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// dcs is a DECRQSS query, which a terminal answers with input. csi8 is the
// C1 form of CSI.
const (
	dcs  = "\x1bP$qm\x1b\\"
	csi8 = "\u009b7m"
)

// rawView is a view that shows AWS text without ui.Sanitize, as a view with
// a bug would.
type rawView struct{}

func (rawView) Init() tea.Cmd                         { return nil }
func (v rawView) Update(tea.Msg) (tea.Model, tea.Cmd) { return v, nil }
func (rawView) View() tea.View                        { return tea.NewView("body [" + dcs + "] [" + csi8 + "]") }
func (rawView) ID() string                            { return "raw" }
func (rawView) Title() string                         { return "title-" + dcs }
func (rawView) KeyMap() []ui.HintBinding              { return nil }
func (rawView) Footer() string                        { return "footer-" + dcs + csi8 }

// M23: the screen never passes a string sequence or a C1 control to the
// terminal, also when a view shows AWS text without ui.Sanitize.
func TestViewStripsUnsafeSequences(t *testing.T) {
	m := newTestModel(120, 30)
	m.nav.Push(rawView{})

	out := m.View().Content
	assert.Equal(t, ui.StripUnsafe(out), out)
	assert.NotContains(t, out, "\x1bP")
	assert.NotContains(t, out, "\u009b")

	plain := ansi.Strip(out)
	assert.Contains(t, plain, "body []")
	assert.Contains(t, plain, "title-␛P$qm␛\\", "the header shows the title through Sanitize")
	assert.Contains(t, plain, "footer-")
}

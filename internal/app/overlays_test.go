package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

func kindsOf(s overlayStack) []overlayKind {
	var out []overlayKind
	for _, e := range s {
		out = append(out, e.kind)
	}
	return out
}

// An overlay that opens again moves to the top, and push never changes the
// stack that another copy of Model holds.
func TestOverlayStackPush(t *testing.T) {
	a := overlayStack{}.push(ovHelp).push(ovConfirm)
	assert.Equal(t, []overlayKind{ovHelp, ovConfirm}, kindsOf(a))

	b := a.push(ovHelp)
	assert.Equal(t, []overlayKind{ovConfirm, ovHelp}, kindsOf(b))
	assert.Equal(t, []overlayKind{ovHelp, ovConfirm}, kindsOf(a), "push makes a new slice")

	top, ok := b.top()
	require.True(t, ok)
	assert.Equal(t, ovHelp, top.kind)
	_, ok = overlayStack{}.top()
	assert.False(t, ok)
}

// A picker that a view asks for while its confirm is open goes on top and
// gets the keys, as the old chain chose the picker first. When it closes,
// the confirm gets the keys again.
func TestPickerOverConfirmGetsTheKeys(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestConfirmMsg{Message: "Start it?", Action: "start", Quick: true}})
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestActionPickerMsg{Title: "Actions", Options: []string{"Stop"}}})
	require.True(t, m.confirm.Visible())
	require.True(t, m.picker.Visible())
	assert.Equal(t, []overlayKind{ovConfirm, ovPicker}, kindsOf(m.overlays))

	m = answer(t, m, keyEnter)
	assert.False(t, m.picker.Visible())
	assert.True(t, m.confirm.Visible(), "enter went to the picker")
	assert.Equal(t, []overlayKind{ovConfirm}, kindsOf(m.overlays))

	m = answer(t, m, press("y"))
	assert.False(t, m.confirm.Visible())
	assert.Empty(t, m.overlays)
	assert.Equal(t, []ui.ConfirmResultMsg{{Confirmed: true, Action: "start"}}, v.confirms)
}

// After a confirm over help closes, help gets the keys again.
func TestHelpGetsTheKeysAgainAfterAConfirm(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m, _ = update(t, m, press("?"))
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestConfirmMsg{Message: "Start it?", Action: "start", Quick: true}})
	m = answer(t, m, press("n"))
	require.True(t, m.help.Visible())

	m, _ = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.False(t, m.help.Visible())
	assert.Equal(t, 2, m.nav.Depth(), "esc closed help and did not go back")
	assert.Empty(t, m.overlays)
}

// The app draws the overlays in stack order. A confirm that opens over the
// command bar draws over its suggestions, so the user can read the
// question that gets the keys. Before PR 2, the suggestions drew over it.
func TestConfirmDrawsOverCommandBarSuggestions(t *testing.T) {
	m := newTestModel(100, 16)
	v := newReplyView()
	m.pushView(v)
	m, _ = update(t, m, press(":"))
	require.NotEmpty(t, m.commandBar.ViewSuggestions())

	question := strings.Repeat("Q", 60)
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestConfirmMsg{Message: question, Action: "start", Quick: true}})
	require.True(t, m.confirm.Visible())
	assert.Contains(t, rendered(m), question)
}

// The picker gets the new size on resize, open or closed. The pin
// TestOverlayPin_HelpGetsTheNewSizeOnResize covers open help. Closed help
// and the command bar get their size again from Show, and the confirm
// dialog fits its text.
func TestResizeReachesThePicker(t *testing.T) {
	m := newTestModel(140, 40)
	m.showRegionPicker()
	m, _ = update(t, m, tea.WindowSizeMsg{Width: 140, Height: 12})
	assert.LessOrEqual(t, lipgloss.Height(m.picker.View()), 12, "the open picker fits the new height")
	m.picker.Hide()

	m, _ = update(t, m, tea.WindowSizeMsg{Width: 100, Height: 14})
	m.showRegionPicker()
	assert.LessOrEqual(t, lipgloss.Height(m.picker.View()), 14, "a picker that opens later fits too")
}

// pruneOverlays makes a new slice, so a copy of Model keeps its stack when
// an overlay under the top one closes.
func TestPruneOverlaysKeepsACopy(t *testing.T) {
	m := newTestModel(140, 40)
	m.help.Show(nil, 140, 40)
	m.openOverlay(ovHelp)
	m.confirm.ShowQuick("Start it?", "start")
	m.openOverlay(ovConfirm)
	saved := m

	m.help.Hide()
	m.pruneOverlays()
	assert.Equal(t, []overlayKind{ovConfirm}, kindsOf(m.overlays))
	assert.Equal(t, []overlayKind{ovHelp, ovConfirm}, kindsOf(saved.overlays))
}

// A copy of Model keeps its own stack when the other copy closes an overlay.
func TestOverlayStackSurvivesACopy(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, press("?"))
	saved := m

	m, _ = update(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Empty(t, m.overlays)
	assert.Equal(t, []overlayKind{ovHelp}, kindsOf(saved.overlays))
	assert.True(t, saved.help.Visible())
}

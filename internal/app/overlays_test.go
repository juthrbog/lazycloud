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
	a := overlayStack{}.push(ovHelp, replyRoute{}).push(ovConfirm, replyRoute{})
	assert.Equal(t, []overlayKind{ovHelp, ovConfirm}, kindsOf(a))

	b := a.push(ovHelp, replyRoute{})
	assert.Equal(t, []overlayKind{ovConfirm, ovHelp}, kindsOf(b))
	assert.Equal(t, []overlayKind{ovHelp, ovConfirm}, kindsOf(a), "push makes a new slice")

	top, ok := b.top()
	require.True(t, ok)
	assert.Equal(t, ovHelp, top.kind)
	_, ok = overlayStack{}.top()
	assert.False(t, ok)
}

// A picker that a view asks for while its confirm is open does not open
// (D3, PR 7). Before PR 7, it opened over the confirm. The view gets a
// decline, and the confirm keeps the keys.
func TestPickerRequestWhileAConfirmIsOpenIsRefused(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestConfirmMsg{Message: "Start it?", Action: "start", Quick: true}})
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestActionPickerMsg{Title: "Actions", Options: []string{"Stop"}}})
	require.True(t, m.confirm.Visible())
	assert.False(t, m.picker.Visible())
	assert.Equal(t, []overlayKind{ovConfirm}, kindsOf(m.overlays))
	assert.True(t, m.toasts.HasActive(), "the user learns about the skipped prompt")
	assert.Equal(t, []ui.PickerResultMsg{{ID: "action", Outcome: ui.PickCancelled, Selected: -1}}, v.picks)

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

// A confirm reply goes to the view that asked, a yes and a no alike, also
// when another view is on top by then.
func TestConfirmReplyGoesToTheViewThatAsked(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want bool
	}{{"y", true}, {"n", false}} {
		t.Run(tc.key, func(t *testing.T) {
			m := newTestModel(140, 40)
			asker := newReplyView()
			m.pushView(asker)
			m, _ = update(t, m, nav.Routed{Origin: asker, Msg: msg.RequestConfirmMsg{Message: "Start it?", Action: "start", Quick: true}})
			other := newReplyView()
			m.pushView(other)

			m = answer(t, m, press(tc.key))
			assert.False(t, m.confirm.Visible())
			assert.Equal(t, []ui.ConfirmResultMsg{{Confirmed: tc.want, Action: "start"}}, asker.confirms)
			assert.Empty(t, other.confirms)
		})
	}
}

// When the view that asked is gone, the reply is dropped. The view that is
// on top now never gets it.
func TestReplyToAViewThatIsGoneIsDropped(t *testing.T) {
	m := newTestModel(140, 40)
	parent := newReplyView()
	m.pushView(parent)
	asker := newReplyView()
	m.pushView(asker)
	m, _ = update(t, m, nav.Routed{Origin: asker, Msg: msg.RequestActionPickerMsg{Title: "Actions", Options: []string{"Stop"}}})
	m.nav.Pop()
	require.True(t, m.nav.IsCurrent(parent))

	m = answer(t, m, keyEnter)
	assert.False(t, m.picker.Visible())
	assert.Empty(t, parent.picks)
	assert.Empty(t, asker.picks)
}

// A request without an origin has no view to reply to, so its reply goes
// the old way until PR 8: esc stays with the app, and a pick goes to the
// view on top.
func TestRequestWithoutOriginRepliesTheOldWay(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	req := msg.RequestActionPickerMsg{Title: "Actions", Options: []string{"Stop"}}

	m, _ = update(t, m, req)
	m = answer(t, m, press("esc"))
	assert.Empty(t, v.picks, "esc stays with the app")

	m, _ = update(t, m, req)
	m = answer(t, m, keyEnter)
	assert.Equal(t, []ui.PickerResultMsg{{ID: "action", Selected: 0, Value: "Stop"}}, v.picks)
}

// A sort pick and a sort clear go to the view that asked, also when another
// view is on top by then.
func TestSortReplyGoesToTheViewThatAsked(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyPressMsg
		want ui.PickerResultMsg
	}{
		{"pick", keyEnter, ui.PickerResultMsg{ID: "sort", Selected: 1, Value: "Size"}},
		{"clear", keyCtrlX, ui.PickerResultMsg{ID: "sort", Outcome: ui.PickCleared, Selected: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(140, 40)
			asker := newReplyView()
			m.pushView(asker)
			m, _ = update(t, m, nav.Routed{Origin: asker, Msg: msg.RequestSortPickerMsg{Columns: []string{"Name", "Size"}, CurrentCol: 1}})
			other := newReplyView()
			m.pushView(other)

			m = answer(t, m, tc.key)
			assert.Equal(t, []ui.PickerResultMsg{tc.want}, asker.picks)
			assert.Empty(t, other.picks)
		})
	}
}

// A picker request from a view that is not on top never opens a picker, so
// no key can answer for a view that the user cannot see.
func TestPickerRequestFromAViewBelowIsDropped(t *testing.T) {
	for _, req := range []tea.Msg{
		msg.RequestActionPickerMsg{Title: "Actions", Options: []string{"Stop"}},
		msg.RequestSortPickerMsg{Columns: []string{"Name"}},
	} {
		m := newTestModel(140, 40)
		below := newReplyView()
		m.pushView(below)
		m.pushView(newReplyView())

		m, _ = update(t, m, nav.Routed{Origin: below, Msg: req})
		assert.False(t, m.picker.Visible(), "%T", req)
		assert.Empty(t, m.overlays)
	}
}

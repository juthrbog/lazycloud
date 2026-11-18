package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
	"github.com/juthrbog/lazycloud/internal/views"
)

// These tests pin how the overlays behave before A7 (see
// docs/design/a7-overlays.md, PR 0). A7 must keep this behavior, except
// where a test says that a later PR changes it on purpose.

// replyView records the prompt replies that reach it.
type replyView struct {
	*views.EventLog
	picks    []msg.PickerResultMsg
	confirms []msg.ConfirmResultMsg
	forms    []msg.FormResultMsg
	lastCmd  tea.Cmd // set by tests that keep the command of an update
}

func newReplyView() *replyView { return &replyView{EventLog: views.NewEventLog()} }

func (v *replyView) Update(m tea.Msg) (tea.Model, tea.Cmd) {
	switch m := m.(type) {
	case msg.PickerResultMsg:
		v.picks = append(v.picks, m)
	case msg.ConfirmResultMsg:
		v.confirms = append(v.confirms, m)
	case msg.FormResultMsg:
		v.forms = append(v.forms, m)
	}
	return v, nil
}

// answer presses k and delivers the messages that the key produces.
func answer(t *testing.T, m Model, k tea.KeyPressMsg) Model {
	t.Helper()
	m, cmd := update(t, m, k)
	return pump(t, m, cmd)
}

// A confirm request that comes while help or the command bar is open
// shows over it and takes the keys first.
func TestOverlayPin_ConfirmOverHelpAndCommandBar(t *testing.T) {
	for _, tc := range []struct {
		name  string
		open  string
		shown func(Model) bool
	}{
		{"help", "?", func(m Model) bool { return m.help.Visible() }},
		{"command bar", ":", func(m Model) bool { return m.commandBar.Visible() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(140, 40)
			v := newReplyView()
			m.pushView(v)
			m, _ = update(t, m, press(tc.open))
			require.True(t, tc.shown(m))

			m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestConfirmMsg{Message: "Start it?", Quick: true}})
			require.True(t, m.confirm.Visible(), "the confirm shows over the %s", tc.name)

			m = answer(t, m, press("y"))
			assert.False(t, m.confirm.Visible())
			assert.True(t, tc.shown(m), "the %s stays open under the confirm", tc.name)
			assert.Equal(t, []msg.ConfirmResultMsg{{Confirmed: true}}, v.confirms)
		})
	}
}

// While a picker is open, ctrl+x goes to the picker. It does not cancel a
// running operation.
func TestOverlayPin_CtrlXOnAnOpenPickerKeepsTheOperation(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m = startTestOp(t, m)
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestSortPickerMsg{Columns: []string{"Name", "Size"}, CurrentCol: -1}})
	require.True(t, m.picker.Visible())

	m = answer(t, m, keyCtrlX)
	assert.False(t, m.picker.Visible(), "ctrl+x closes the picker")
	assert.Equal(t, 1, m.ops.Count(), "the operation goes on")
	assert.False(t, m.picker.Visible(), "no cancel picker opens")
}

// esc on a sort or an action picker reaches the view as a cancel (D2, from
// PR 3). Before PR 3, the app dropped it. ctrl+x on the sort picker reaches
// the view as a clear. The action picker cannot be cleared, so ctrl+x
// cancels it like esc (D4, PR 1).
func TestOverlayPin_EscAndClearOnViewPickers(t *testing.T) {
	for _, tc := range []struct {
		name  string
		req   tea.Msg
		clear msg.PickOutcome // what ctrl+x sends to the view
		hint  bool            // the hint names ctrl+x
	}{
		{"sort", msg.RequestSortPickerMsg{Columns: []string{"Name", "Size"}, CurrentCol: 0}, msg.PickCleared, true},
		{"action", msg.RequestActionPickerMsg{Title: "Actions", Options: []string{"Stop", "Start"}}, msg.PickCancelled, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(140, 40)
			v := newReplyView()
			m.pushView(v)

			m, _ = update(t, m, nav.Routed{Origin: v, Msg: tc.req})
			assert.Equal(t, tc.hint, strings.Contains(ansi.Strip(m.picker.View()), "ctrl+x clear"), "the clear hint")
			m = answer(t, m, press("esc"))
			assert.False(t, m.picker.Visible())
			assert.Equal(t, []msg.PickerResultMsg{{Outcome: msg.PickCancelled}}, v.picks, "esc reaches the view")

			m, _ = update(t, m, nav.Routed{Origin: v, Msg: tc.req})
			m = answer(t, m, keyCtrlX)
			assert.False(t, m.picker.Visible())
			assert.Equal(t, msg.PickerResultMsg{Outcome: tc.clear}, v.picks[len(v.picks)-1])
			assert.Len(t, v.picks, 2)
		})
	}
}

// A8: below the home view, the theme picker switches the theme at once and
// keeps the views.
func TestOverlayPin_ThemeSwitchBelowHomeAppliesAtOnce(t *testing.T) {
	a8Theme(t)
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	before := ui.ActiveTheme.Name

	m, _ = update(t, m, press("T"))
	require.True(t, m.picker.Visible())
	m, _ = update(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = answer(t, m, keyEnter)

	assert.False(t, m.confirm.Visible(), "no question")
	assert.NotEqual(t, before, ui.ActiveTheme.Name)
	assert.Same(t, v, m.nav.Current(), "the views stay")
	assert.Empty(t, v.picks, "the theme pick does not reach the view")
}

// The discard confirm of a form shows over the form and takes the keys
// first. A yes closes the form as aborted.
func TestOverlayPin_FormDiscardConfirm(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m, form := askDiscardOfAnEditedForm(t, m, v)
	m, cmd := update(t, m, press("n"))
	assert.False(t, m.confirm.Visible())
	assert.Same(t, form, m.nav.Current(), "a no keeps the form")
	m = pump(t, m, cmd)
	assert.Same(t, form, m.nav.Current())

	// esc asks again. This is a new prompt, so it gets its own reply.
	m = answer(t, m, press("esc"))
	require.True(t, m.confirm.Visible(), "the form asks again")
	m = answer(t, m, press("y"))
	assert.Same(t, v, m.nav.Current(), "a yes closes the form")
	assert.Empty(t, v.confirms, "the discard confirm does not reach the view")
	assert.Equal(t, []msg.FormResultMsg{{ID: "f", Aborted: true}}, v.forms, "the view hears that the form closed")
}

// Help gets the new size when the window changes while it is open. Before
// PR 2, it kept the size that it had when it opened.
func TestOverlayPin_HelpGetsTheNewSizeOnResize(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, press("?"))
	require.True(t, m.help.Visible())
	before := lipgloss.Width(m.help.View())

	m, _ = update(t, m, tea.WindowSizeMsg{Width: 60, Height: 20})
	after := m.help.View()
	assert.Less(t, lipgloss.Width(after), before)
	assert.LessOrEqual(t, lipgloss.Width(after), 60)
	assert.LessOrEqual(t, lipgloss.Height(after), 20)
}

// L9: q and j go to open help. q closes help and does not leave the view.
func TestHelpTakesQAndJ(t *testing.T) {
	m := newTestModel(140, 40)
	m, _ = update(t, m, msg.NavigateMsg{ViewID: "eventlog"})
	m, _ = update(t, m, press("?"))
	require.True(t, m.help.Visible())

	m, _ = update(t, m, press("j"))
	require.True(t, m.help.Visible())
	m, _ = update(t, m, press("q"))

	assert.False(t, m.help.Visible())
	assert.Equal(t, "eventlog", m.nav.Current().ID())
}

// A reply goes to the view that asked, also when another view is on top
// when the reply comes. Before PR 3, it went to the view on top.
func TestOverlayPin_ReplyGoesToTheViewThatAsked(t *testing.T) {
	m := newTestModel(140, 40)
	asker := newReplyView()
	m.pushView(asker)
	m, _ = update(t, m, nav.Routed{Origin: asker, Msg: msg.RequestActionPickerMsg{Title: "Actions", Options: []string{"Stop"}}})
	require.True(t, m.picker.Visible())

	// A load of the asker ends with a navigation while the picker is open.
	other := newReplyView()
	m.pushView(other)
	m = answer(t, m, keyEnter)
	assert.Equal(t, []msg.PickerResultMsg{{Selected: 0, Value: "Stop"}}, asker.picks)
	assert.Empty(t, other.picks, "the view on top gets no reply")
}

// appState is the state that a picker of the app can change.
type appState struct {
	depth    int
	top      nav.View
	theme    string
	readOnly bool
	region   string
	profile  string
	ops      int
	confirm  bool
}

func stateOf(m Model) appState {
	return appState{
		depth: m.nav.Depth(), top: m.nav.Current(), theme: ui.ActiveTheme.Name, readOnly: ui.IsReadOnly(),
		region: m.activeRegion(), profile: m.config.AWS.Profile, ops: m.ops.Count(), confirm: m.confirm.Visible(),
	}
}

// esc and ctrl+x close each picker of the app and change nothing. A view
// below the home view gets no reply.
func TestOverlayPin_EscAndClearOnAppPickers(t *testing.T) {
	defer func() { ui.ActiveTheme = ui.DefaultTheme; ui.RebuildStyles() }()
	defer ui.SetReadOnly(true)
	for _, tc := range []struct {
		name string
		home bool // the service picker opens only on the home view
		open func(t *testing.T, m Model) Model
	}{
		{"theme", false, func(t *testing.T, m Model) Model { m, _ = update(t, m, press("T")); return m }},
		{"region", false, func(t *testing.T, m Model) Model { m, _ = update(t, m, press("R")); return m }},
		{"profile", false, func(t *testing.T, m Model) Model {
			awsConfigEnv(t, "dev", "prod")
			m, _ = update(t, m, press("P"))
			return m
		}},
		{"mode", false, func(t *testing.T, m Model) Model { m, _ = update(t, m, press("W")); return m }},
		{"service", true, func(t *testing.T, m Model) Model { m, _ = update(t, m, press("s")); return m }},
		{"feature", true, func(t *testing.T, m Model) Model {
			m, _ = update(t, m, press("s"))
			return answer(t, m, keyEnter) // EC2 has several features
		}},
		{"cancel", false, func(t *testing.T, m Model) Model {
			m = startTestOp(t, m)
			m, _ = update(t, m, keyCtrlX)
			return m
		}},
	} {
		for _, k := range []struct {
			name string
			key  tea.KeyPressMsg
		}{{"esc", press("esc")}, {"ctrl+x", keyCtrlX}} {
			t.Run(tc.name+"/"+k.name, func(t *testing.T) {
				m := newTestModel(140, 40)
				var v *replyView
				if !tc.home {
					v = newReplyView()
					m.pushView(v)
				}
				m = tc.open(t, m)
				defer m.ops.CancelAll()
				require.True(t, m.picker.Visible(), "the %s picker opens", tc.name)
				assert.NotContains(t, ansi.Strip(m.picker.View()), "ctrl+x clear", "only the sort picker can be cleared (D4)")
				before := stateOf(m)

				m = answer(t, m, k.key)
				assert.False(t, m.picker.Visible(), "%s closes the picker", k.name)
				assert.Equal(t, before, stateOf(m), "%s changes nothing", k.name)
				for _, r := range m.ops.Running() {
					require.NoError(t, r.Ctx.Err(), "the operation goes on")
				}
				if v != nil {
					assert.Empty(t, v.picks, "the view gets no reply")
				}
			})
		}
	}
}

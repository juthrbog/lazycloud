package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/nav"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// These tests pin the replies that reach a view before A12
// (docs/design/a12-view-tags.md). PR 1 adds a tag to each request here and
// checks that every reply carries it. Today a reply carries the Action of
// the confirm or the ID of the picker.

// Every way that a prompt of a view closes sends exactly one reply to the
// view.
func TestA12Pin_EveryReplyPathReachesTheView(t *testing.T) {
	typed := msg.RequestConfirmMsg{Message: "Delete?", Action: "go"}
	quick := msg.RequestConfirmMsg{Message: "Start?", Action: "go", Quick: true}
	action := msg.RequestActionPickerMsg{Title: "Actions", Options: []string{"Stop", "Start"}}
	sort := msg.RequestSortPickerMsg{Columns: []string{"Name", "Size"}, CurrentCol: -1}
	yes := msg.ConfirmResultMsg{Confirmed: true, Action: "go"}
	no := msg.ConfirmResultMsg{Confirmed: false, Action: "go"}

	for _, c := range []struct {
		name string
		req  tea.Msg
		keys []tea.KeyPressMsg
		want tea.Msg
	}{
		{"typed enter", typed, typedConfirm(), yes},
		{"typed esc", typed, []tea.KeyPressMsg{press("esc")}, no},
		{"quick y", quick, []tea.KeyPressMsg{press("y")}, yes},
		{"quick n", quick, []tea.KeyPressMsg{press("n")}, no},
		{"quick enter", quick, []tea.KeyPressMsg{keyEnter}, no},
		{"quick esc", quick, []tea.KeyPressMsg{press("esc")}, no},
		{"action pick", action, []tea.KeyPressMsg{keyDown, keyEnter},
			msg.PickerResultMsg{ID: "action", Selected: 1, Value: "Start"}},
		{"action esc", action, []tea.KeyPressMsg{press("esc")},
			msg.PickerResultMsg{ID: "action", Outcome: msg.PickCancelled}},
		{"action ctrl+x", action, []tea.KeyPressMsg{keyCtrlX},
			msg.PickerResultMsg{ID: "action", Outcome: msg.PickCancelled}},
		{"sort pick", sort, []tea.KeyPressMsg{keyDown, keyEnter},
			msg.PickerResultMsg{ID: "sort", Selected: 1, Value: "Size"}},
		{"sort esc", sort, []tea.KeyPressMsg{press("esc")},
			msg.PickerResultMsg{ID: "sort", Outcome: msg.PickCancelled}},
		{"sort ctrl+x", sort, []tea.KeyPressMsg{keyCtrlX},
			msg.PickerResultMsg{ID: "sort", Outcome: msg.PickCleared}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := newTestModel(140, 40)
			v := newReplyView()
			m.pushView(v)
			m, _ = update(t, m, nav.Routed{Origin: v, Msg: c.req})
			require.True(t, m.promptOpen())
			m, _ = reply(t, m, c.keys...)
			assert.False(t, m.promptOpen())
			assert.Equal(t, []tea.Msg{c.want}, repliesOf(v))
		})
	}
}

var keyDown = tea.KeyPressMsg{Code: tea.KeyDown}

// repliesOf returns the replies that v got, in a single list.
func repliesOf(v *replyView) []tea.Msg {
	var out []tea.Msg
	for _, c := range v.confirms {
		out = append(out, c)
	}
	for _, p := range v.picks {
		out = append(out, p)
	}
	return out
}

// The action picker reports the index of the option in the list of the
// request, also after a filter hides options before it.
func TestA12Pin_ActionPickAfterAFilterHasTheOriginalIndex(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestActionPickerMsg{
		Title: "Actions", Options: []string{"Stop", "Start", "Reboot"},
	}})
	m = typeInto(t, m, "reb")
	m, _ = reply(t, m, keyEnter)
	assert.Equal(t, []msg.PickerResultMsg{{ID: "action", Selected: 2, Value: "Reboot"}}, v.picks)
}

// A theme switch resets the views. It declines each kind of view prompt
// exactly once (I6). The decline then finds no view and is dropped, and no
// prompt stays open.
func TestA12Pin_ThemeSwitchWithEachViewPromptOpen(t *testing.T) {
	for name, req := range map[string]tea.Msg{
		"confirm":       msg.RequestConfirmMsg{Message: "Go?", Action: "go"},
		"quick confirm": msg.RequestConfirmMsg{Message: "Go?", Action: "go", Quick: true},
		"action picker": msg.RequestActionPickerMsg{Title: "Actions", Options: []string{"Stop"}},
		"sort picker":   msg.RequestSortPickerMsg{Columns: []string{"Name"}},
	} {
		t.Run(name, func(t *testing.T) {
			m := newTestModel(140, 40)
			v := newReplyView()
			m.pushView(v)
			m, _ = update(t, m, nav.Routed{Origin: v, Msg: req})
			require.True(t, m.promptOpen())

			m, cmd := m.setTheme(ui.ThemeOrder[0])
			assert.Len(t, declinesOf(cmd), 1, "one decline")
			m = pump(t, m, cmd)
			assert.False(t, m.promptOpen())
			assert.Empty(t, m.overlays)
			assert.Empty(t, repliesOf(v), "the view is gone, so it gets nothing")
		})
	}
	t.Run("form discard confirm", func(t *testing.T) {
		m := newTestModel(140, 40)
		v := newReplyView()
		m.pushView(v)
		m, _ = askDiscardOfAnEditedForm(t, m, v)

		m, cmd := m.setTheme(ui.ThemeOrder[0])
		assert.Len(t, declinesOf(cmd), 1, "one decline")
		m = pump(t, m, cmd)
		assert.False(t, m.promptOpen())
		assert.Empty(t, m.overlays)
		assert.Empty(t, v.forms, "the view is gone, so it gets nothing")
	})
}

// declinesOf returns the prompt replies that cmd sends.
func declinesOf(cmd tea.Cmd) []overlayReplyMsg {
	var out []overlayReplyMsg
	for _, m := range collectMsgs(cmd) {
		if r, ok := m.(overlayReplyMsg); ok {
			out = append(out, r)
		}
	}
	return out
}

// askDiscardOfAnEditedForm opens a form for v, types into it and presses
// esc, so the form asks whether to discard the change. It returns the form.
func askDiscardOfAnEditedForm(t *testing.T, m Model, v nav.View) (Model, nav.View) {
	t.Helper()
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestFormMsg{
		ID: "f", Title: "Form", Fields: []msg.FormField{{Key: "body", Type: "input", Title: "Body"}},
	}})
	form := m.nav.Current()
	require.IsType(t, &ui.FormView{}, form)
	m = answer(t, m, press("a"))
	m = answer(t, m, press("esc"))
	require.True(t, m.confirm.Visible(), "the edited form asks first")
	require.Same(t, form, m.nav.Current())
	return m, form
}

// The discard confirm of an edited form: a no keeps the form, and a yes
// closes it as aborted (I14).
func TestA12Pin_FormDiscardThroughEsc(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m, form := askDiscardOfAnEditedForm(t, m, v)
	m = answer(t, m, press("n"))
	assert.False(t, m.confirm.Visible())
	assert.Same(t, form, m.nav.Current(), "a no keeps the form")
	assert.Empty(t, v.forms)

	m = answer(t, m, press("esc"))
	require.True(t, m.confirm.Visible(), "the form asks again")
	m = answer(t, m, press("y"))
	assert.Same(t, v, m.nav.Current(), "a yes closes the form")
	assert.Empty(t, v.confirms, "the discard confirm does not reach the view")
	assert.Equal(t, []msg.FormResultMsg{{ID: "f", Aborted: true}}, v.forms)
}

// A discard request of an edited form while another prompt is open is
// refused. The form gets the decline and stays open (I14).
func TestA12Pin_RefusedDiscardKeepsTheForm(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m, _ = update(t, m, nav.Routed{Origin: v, Msg: msg.RequestFormMsg{
		ID: "f", Title: "Form", Fields: []msg.FormField{{Key: "body", Type: "input", Title: "Body"}},
	}})
	form := m.nav.Current()
	require.IsType(t, &ui.FormView{}, form)
	m = answer(t, m, press("a"))
	m.askQuick("Open?", func(m Model) (Model, tea.Cmd) { return m, nil }, nil)

	// The confirm on top takes the keys, so the form gets esc directly.
	_, cmd := form.Update(press("esc"))
	require.NotNil(t, cmd)
	m, cmd = update(t, m, nav.Routed{Origin: form, Msg: cmd()})
	m = pump(t, m, cmd)
	assert.True(t, m.toasts.HasActive())
	assert.Same(t, form, m.nav.Current(), "the refusal keeps the form")
	assert.Empty(t, v.forms, "the form did not close")
}

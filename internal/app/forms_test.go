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

var testForm = msg.RequestFormMsg{ID: "f", Title: "Form", Fields: []msg.FormField{{Key: "body", Title: "Body"}}}

// openTestForm lets asker ask for testForm and returns the form view.
func openTestForm(t *testing.T, m Model, asker nav.View) (Model, *ui.FormView) {
	t.Helper()
	m, _ = update(t, m, nav.Routed{Origin: asker, Msg: testForm})
	fv, ok := m.nav.Current().(*ui.FormView)
	require.True(t, ok, "the form is on top")
	return m, fv
}

// The result of a form goes to the view that asked for it, and the app
// forgets the form.
func TestFormResultGoesToTheViewThatAsked(t *testing.T) {
	m := newTestModel(140, 40)
	asker := newReplyView()
	m.pushView(asker)
	m, fv := openTestForm(t, m, asker)
	require.Len(t, m.forms, 1)
	// The asker is always right below the form today, so only the record
	// shows that the result does not just go to the view below.
	assert.Equal(t, formRoute{form: fv, asker: asker}, m.forms[0])

	res := msg.FormResultMsg{ID: "f", Values: map[string]string{"body": "hi"}}
	m = pump(t, m, func() tea.Msg { return nav.Routed{Origin: fv, Msg: res} })
	assert.Same(t, asker, m.nav.Current(), "the form is popped")
	assert.Equal(t, []msg.FormResultMsg{res}, asker.forms)
	assert.Empty(t, m.forms)
}

// A form that is not on top cannot close itself or another view. Its
// result is dropped.
func TestFormResultFromAFormBelowTheTopIsDropped(t *testing.T) {
	m := newTestModel(140, 40)
	asker := newReplyView()
	m.pushView(asker)
	m, fv := openTestForm(t, m, asker)
	top := newReplyView()
	m.pushView(top)

	m, _ = update(t, m, nav.Routed{Origin: fv, Msg: msg.FormResultMsg{ID: "f", Aborted: true}})
	assert.Same(t, top, m.nav.Current(), "nothing is popped")
	assert.True(t, m.nav.Contains(fv))
	assert.Empty(t, asker.forms)
	assert.Empty(t, top.forms)
}

// When the view that asked for a form is gone, the form still closes, but
// its result is dropped. No other view takes it for its own.
func TestFormResultForAViewThatIsGoneIsDropped(t *testing.T) {
	m := newTestModel(140, 40)
	below := newReplyView()
	m.pushView(below)
	m, fv := openTestForm(t, m, below)
	m.forms[0].asker = newReplyView() // a view that is not on the stack

	m, _ = update(t, m, nav.Routed{Origin: fv, Msg: msg.FormResultMsg{ID: "f", Aborted: true}})
	assert.Same(t, below, m.nav.Current(), "the form is popped")
	assert.Empty(t, below.forms)
	assert.Empty(t, m.forms)
}

// A form request without an origin has no view to reply to, so its result
// goes to the view below the form, as before.
func TestFormWithoutOriginRepliesTheOldWay(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m, _ = update(t, m, testForm)
	fv, ok := m.nav.Current().(*ui.FormView)
	require.True(t, ok)

	res := msg.FormResultMsg{ID: "f", Aborted: true}
	m, _ = update(t, m, nav.Routed{Origin: fv, Msg: res})
	assert.Same(t, v, m.nav.Current())
	assert.Equal(t, []msg.FormResultMsg{res}, v.forms)
}

// A session switch or a theme switch replaces every view, so the app
// forgets the open forms. A new form keeps only the routes of forms that
// are still on the stack.
func TestFormRoutesFollowTheStack(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m, _ = openTestForm(t, m, v)
	m.resetNav()
	assert.Empty(t, m.forms)

	m.pushView(v)
	m, _ = openTestForm(t, m, v)
	m.nav.Pop() // the form left the stack without a result
	m, _ = openTestForm(t, m, v)
	assert.Len(t, m.forms, 1)
}

// The discard confirm of a form replies to that form, so the form closes
// itself. The app no longer guesses the form from the top of the stack.
func TestDiscardConfirmRepliesToTheForm(t *testing.T) {
	m := newTestModel(140, 40)
	v := newReplyView()
	m.pushView(v)
	m, fv := openTestForm(t, m, v)

	m, _ = update(t, m, nav.Routed{Origin: fv, Msg: ui.RequestFormDiscardConfirmMsg{}})
	top, ok := m.overlays.top()
	require.True(t, ok)
	assert.Equal(t, ovConfirm, top.kind)
	assert.Equal(t, nav.View(fv), top.route.replyTo)
}

// A form request or a discard request from a view that is not on top
// opens nothing over the view that the user sees.
func TestFormRequestsFromAViewBelowAreDropped(t *testing.T) {
	m := newTestModel(140, 40)
	below := newReplyView()
	m.pushView(below)
	m, fv := openTestForm(t, m, below)
	top := newReplyView()
	m.pushView(top)

	m, _ = update(t, m, nav.Routed{Origin: below, Msg: testForm})
	assert.Same(t, top, m.nav.Current(), "no form opens")
	m, _ = update(t, m, nav.Routed{Origin: fv, Msg: ui.RequestFormDiscardConfirmMsg{}})
	assert.False(t, m.confirm.Visible(), "no discard confirm opens")
	assert.Len(t, m.forms, 1)
}

// The result goes to the recorded asker, not just to the view below the
// form. Today the asker is always right below, so the test puts it deeper.
func TestFormResultGoesToTheAskerNotTheViewBelow(t *testing.T) {
	m := newTestModel(140, 40)
	asker := newReplyView()
	m.pushView(asker)
	below := newReplyView()
	m.pushView(below)
	m, fv := openTestForm(t, m, below)
	m.forms[0].asker = asker

	res := msg.FormResultMsg{ID: "f", Aborted: true}
	m, _ = update(t, m, nav.Routed{Origin: fv, Msg: res})
	assert.Same(t, below, m.nav.Current())
	assert.Equal(t, []msg.FormResultMsg{res}, asker.forms)
	assert.Empty(t, below.forms)
}

package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
)

// M18: form dirty tracking was wrong in both directions.

// formOut collects what a form sends to the app.
type formOut struct {
	discard *msg.RequestConfirmMsg // the discard confirm, if the form asked
	result  *msg.FormResultMsg
}

// send delivers a message to the form. It runs the commands that the form
// returns and delivers the messages of huh, such as the move to the next
// field, back to the form, as the runtime would. It skips timers, such as
// the cursor blink.
func send(t *testing.T, f *FormView, m tea.Msg, out *formOut) {
	t.Helper()
	_, cmd := f.Update(m)
	run(t, f, cmd, out, 0)
}

func run(t *testing.T, f *FormView, cmd tea.Cmd, out *formOut, depth int) {
	t.Helper()
	if cmd == nil || depth > 10 {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	var m tea.Msg
	select {
	case m = <-ch:
	case <-time.After(50 * time.Millisecond):
		return // a timer
	}
	switch m := m.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			run(t, f, c, out, depth+1)
		}
	case msg.RequestConfirmMsg:
		out.discard = &m
	case msg.FormResultMsg:
		out.result = &m
	default:
		if strings.HasPrefix(fmt.Sprintf("%T", m), "huh.") {
			_, next := f.Update(m)
			run(t, f, next, out, depth+1)
		}
	}
}

func formType(t *testing.T, f *FormView, s string, out *formOut) {
	t.Helper()
	for _, r := range s {
		send(t, f, tea.KeyPressMsg{Code: r, Text: string(r)}, out)
	}
}

var (
	keyEsc      = tea.KeyPressMsg{Code: tea.KeyEscape}
	keyEnter    = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyShiftTab = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	keyBack     = tea.KeyPressMsg{Code: tea.KeyBackspace}
)

func newTestForm(t *testing.T, fields ...msg.FormField) *FormView {
	t.Helper()
	f := NewFormView(msg.RequestFormMsg{ID: "test", Title: "Test", Fields: fields})
	run(t, f, f.Init(), &formOut{}, 0)
	f.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	return f
}

// An untouched form with default values is not dirty, so Esc closes it
// without a discard prompt.
func TestFormUntouchedDefaultsAreClean(t *testing.T) {
	f := newTestForm(t,
		msg.FormField{Key: "name", Title: "Name", Default: "orders"},
		msg.FormField{Key: "kind", Title: "Kind", Type: "select", Options: []string{"a", "b"}, Default: "b"},
		msg.FormField{Key: "plain", Title: "Plain", Type: "select", Options: []string{"x", "y"}},
		msg.FormField{Key: "body", Title: "Body", Type: "text", Default: "hello"},
	)
	var out formOut
	send(t, f, keyEsc, &out)
	assert.Nil(t, out.discard, "no prompt for an untouched form")
	require.NotNil(t, out.result)
	assert.True(t, out.result.Aborted)
}

// Text typed in a later field is not lost when the user goes back with
// shift+tab and presses Esc.
func TestFormInputAfterShiftTabIsDirty(t *testing.T) {
	f := newTestForm(t,
		msg.FormField{Key: "a", Title: "A"},
		msg.FormField{Key: "b", Title: "B"},
	)
	var out formOut
	send(t, f, keyEnter, &out) // to field B
	formType(t, f, "typed", &out)
	send(t, f, keyShiftTab, &out) // back to field A
	require.Equal(t, "a", f.form.GetFocusedField().GetKey())

	send(t, f, keyEsc, &out)
	assert.NotNil(t, out.discard, "Esc asks before it drops the text in B")
	assert.Nil(t, out.result)
}

// A change that is undone is not a change.
func TestFormUndoneChangeIsClean(t *testing.T) {
	f := newTestForm(t, msg.FormField{Key: "name", Title: "Name", Default: "ab"})
	var out formOut
	formType(t, f, "c", &out)
	require.True(t, f.isDirty())
	send(t, f, keyBack, &out)
	assert.False(t, f.isDirty())
}

// A select that the user moves is dirty.
func TestFormSelectChangeIsDirty(t *testing.T) {
	f := newTestForm(t, msg.FormField{Key: "kind", Title: "Kind", Type: "select", Options: []string{"a", "b"}})
	var out formOut
	send(t, f, tea.KeyPressMsg{Code: tea.KeyDown}, &out)
	assert.True(t, f.isDirty())
}

// The submitted values are the values in the fields, also for a field
// that the user changed after going back with shift+tab.
func TestFormSubmitUsesFieldValues(t *testing.T) {
	f := newTestForm(t,
		msg.FormField{Key: "a", Title: "A"},
		msg.FormField{Key: "b", Title: "B", Default: "keep"},
	)
	var out formOut
	formType(t, f, "one", &out)
	send(t, f, keyEnter, &out) // to B
	send(t, f, keyShiftTab, &out)
	formType(t, f, "!", &out) // A is now "one!"
	send(t, f, keyEnter, &out)
	send(t, f, keyEnter, &out) // submit from B
	require.NotNil(t, out.result)
	assert.False(t, out.result.Aborted)
	assert.Equal(t, map[string]string{"a": "one!", "b": "keep"}, out.result.Values)
}

// The form asks with a quick confirm that carries its own tag.
func TestFormDiscardRequest(t *testing.T) {
	f := newTestForm(t, msg.FormField{Key: "name", Title: "Name"})
	var out formOut
	formType(t, f, "x", &out)
	send(t, f, keyEsc, &out)
	require.NotNil(t, out.discard)
	req := *out.discard
	require.NotNil(t, req.Tag)
	assert.Same(t, f.discard, req.Tag, "the tag of this form")
	req.Tag = nil
	assert.Equal(t, msg.RequestConfirmMsg{Message: "Discard unsaved changes?", Quick: true}, req)
}

// The app sends the reply of the discard confirm to the form. A yes closes
// the form, a no keeps it open, and a reply to another question does
// nothing.
func TestFormDiscardReply(t *testing.T) {
	// askDiscard types into f and presses Esc. It returns the discard
	// confirm of f.
	askDiscard := func(f *FormView) msg.RequestConfirmMsg {
		var out formOut
		formType(t, f, "x", &out)
		send(t, f, keyEsc, &out)
		require.NotNil(t, out.discard)
		return *out.discard
	}
	reply := func(req msg.RequestConfirmMsg, yes bool) msg.ConfirmResultMsg {
		return msg.ConfirmResultMsg{Confirmed: yes, Tag: req.Tag}
	}

	f := newTestForm(t, msg.FormField{Key: "name", Title: "Name"})
	req := askDiscard(f)
	var out formOut
	send(t, f, reply(req, false), &out)
	assert.Nil(t, out.result, "a no keeps the form")

	// Each reply below is not for the prompt of this form, so it does
	// nothing.
	req = askDiscard(f)
	send(t, f, msg.ConfirmResultMsg{Confirmed: true}, &out)
	assert.Nil(t, out.result, "an untagged reply does nothing")
	send(t, f, msg.ConfirmResultMsg{Confirmed: true, Tag: (*discardTag)(nil)}, &out)
	assert.Nil(t, out.result, "a typed-nil tag does nothing")

	send(t, f, reply(req, true), &out)
	require.NotNil(t, out.result, "a yes closes the form")
	assert.Equal(t, msg.FormResultMsg{ID: "test", Aborted: true}, *out.result)

	// A closed form sends no second result. A real prompt gets one reply
	// (I10). This checks that a late reply to the closed form is safe.
	out = formOut{}
	send(t, f, reply(req, true), &out)
	assert.Nil(t, out.result, "a closed form sends no second result")
}

// Each form has its own discard tag, so a yes for the confirm of another
// form does not close this one.
func TestFormDiscardTagIsPerForm(t *testing.T) {
	f := newTestForm(t, msg.FormField{Key: "name", Title: "Name"})
	other := newTestForm(t, msg.FormField{Key: "name", Title: "Name"})
	assert.NotSame(t, f.discard, other.discard, "two forms share a discard tag")

	var out formOut
	formType(t, f, "x", &out)
	send(t, f, keyEsc, &out)
	require.NotNil(t, out.discard)
	send(t, f, msg.ConfirmResultMsg{Confirmed: true, Tag: other.discard}, &out)
	assert.Nil(t, out.result, "the tag of another form does nothing")
}

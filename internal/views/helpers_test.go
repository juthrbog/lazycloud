package views

import (
	"context"
	"slices"
	"testing"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/juthrbog/lazycloud/internal/msg"
)

// Shared helpers for the view tests.

// ─── Prompt requests and their replies ───────────────────────────────────────
//
// A view asks with a request, and the app answers with a reply. These
// helpers build the reply that the app sends from the request that the view
// sent, so a test answers the real request. Today the reply copies the
// Action of a confirm and uses the picker ID of the app ("action" or
// "sort"). A12 (docs/design/a12-view-tags.md) changes only these helpers to
// copy the tag of the request.

// requestOf runs cmd and returns the message of type T. It fails the test
// when cmd sends none, or more than one, because a view asks for one prompt
// at a time.
func requestOf[T any](t testing.TB, cmd tea.Cmd) T {
	t.Helper()
	msgs := collectMsgs(cmd)
	var found []T
	for _, m := range msgs {
		if req, ok := m.(T); ok {
			found = append(found, req)
		}
	}
	var zero T
	require.Len(t, found, 1, "want one %T in %#v", zero, msgs)
	return found[0]
}

// answerYes sends the yes to req to v and runs the command that the yes
// gives. It fails the test when the yes runs nothing, so a lost tag cannot
// turn a yes into a silent no (R1).
func answerYes(t testing.TB, v tea.Model, req msg.RequestConfirmMsg) {
	t.Helper()
	_, cmd := v.Update(confirmReply(req, true))
	require.NotNil(t, cmd, "the yes runs nothing")
	collectMsgs(cmd)
}

// confirmReply is the reply of the confirm of req: a yes when yes is true,
// and a no otherwise. A no is also the decline that the app sends when it
// refuses req.
func confirmReply(req msg.RequestConfirmMsg, yes bool) msg.ConfirmResultMsg {
	return msg.ConfirmResultMsg{Confirmed: yes, Action: req.Action}
}

// pickLabel is the reply of the action picker of req when the user picks
// the option label. It fails the test when req has no such option.
func pickLabel(t testing.TB, req msg.RequestActionPickerMsg, label string) msg.PickerResultMsg {
	t.Helper()
	i := slices.Index(req.Options, label)
	require.GreaterOrEqual(t, i, 0, "no option %q in %q", label, req.Options)
	return msg.PickerResultMsg{ID: "action", Selected: i, Value: label}
}

// pickColumn is the reply of the sort picker of req when the user picks
// the column title. It fails the test when req has no such column.
func pickColumn(t testing.TB, req msg.RequestSortPickerMsg, title string) msg.PickerResultMsg {
	t.Helper()
	i := slices.Index(req.Columns, title)
	require.GreaterOrEqual(t, i, 0, "no column %q in %q", title, req.Columns)
	return msg.PickerResultMsg{ID: "sort", Selected: i, Value: title}
}

// cancelPick is the reply of the picker of req when the user presses esc.
// It is also the decline that the app sends when it refuses req.
func cancelPick[R msg.RequestActionPickerMsg | msg.RequestSortPickerMsg](req R) msg.PickerResultMsg {
	id := "action"
	if _, ok := any(req).(msg.RequestSortPickerMsg); ok {
		id = "sort"
	}
	return msg.PickerResultMsg{ID: id, Outcome: msg.PickCancelled}
}

// clearSort is the reply of the sort picker of req when the user presses
// ctrl+x. Today the reply needs nothing from req. A12 copies its tag.
func clearSort(_ msg.RequestSortPickerMsg) msg.PickerResultMsg {
	return msg.PickerResultMsg{ID: "sort", Outcome: msg.PickCleared}
}

// sortRequest presses s on v and returns the sort picker request.
func sortRequest(t testing.TB, v tea.Model) msg.RequestSortPickerMsg {
	t.Helper()
	_, cmd := v.Update(keyPress('s'))
	return requestOf[msg.RequestSortPickerMsg](t, cmd)
}

func keyPress(char rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: char, Text: string(char)}
}

// firstOfType returns the first message in msgs with the same type as want.
func firstOfType[T any](msgs []tea.Msg) (T, bool) {
	for _, m := range msgs {
		if v, ok := m.(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// deliverAll sends each message to the view and returns the produced messages.
func deliverAll(v tea.Model, msgs ...tea.Msg) []tea.Msg {
	var out []tea.Msg
	for _, m := range msgs {
		_, cmd := v.Update(m)
		out = append(out, collectMsgs(cmd)...)
	}
	return out
}

// collectMsgs runs a command and every command inside a tea.BatchMsg.
func collectMsgs(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	res := cmd()
	if op, ok := res.(msg.StartOpMsg); ok {
		// Run a background operation as the app would.
		return []tea.Msg{op.Run(context.Background())}
	}
	batch, ok := res.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{res}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, collectMsgs(c)...)
	}
	return out
}

func toasts(msgs []tea.Msg) []msg.ToastMsg {
	var out []msg.ToastMsg
	for _, m := range msgs {
		if t, ok := m.(msg.ToastMsg); ok {
			out = append(out, t)
		}
	}
	return out
}

// pump runs cmd, delivers each produced message to v, and repeats with the
// commands that v returns, for at most rounds rounds. It returns every
// message it saw. It does not deliver spinner ticks, so a visible spinner
// does not loop forever. It does not run commands that wait longer than
// maxWait (for example a delayed reload): those are skipped.
func pump(v tea.Model, cmd tea.Cmd, rounds int) []tea.Msg {
	var seen []tea.Msg
	cmds := []tea.Cmd{cmd}
	for range rounds {
		var next []tea.Cmd
		for _, c := range cmds {
			for _, m := range collectMsgsTimed(c, maxWait) {
				seen = append(seen, m)
				if _, tick := m.(spinner.TickMsg); tick {
					continue
				}
				_, out := v.Update(m)
				if out != nil {
					next = append(next, out)
				}
			}
		}
		if len(next) == 0 {
			break
		}
		cmds = next
	}
	return seen
}

// maxWait is how long pump waits for one command before it skips it.
const maxWait = 500 * time.Millisecond

// collectMsgsTimed is collectMsgs, but a command that takes longer than wait
// is skipped. tea.Tick commands block until their time, so this keeps a test
// from waiting for a delayed reload.
func collectMsgsTimed(cmd tea.Cmd, wait time.Duration) []tea.Msg {
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var res tea.Msg
	select {
	case res = <-done:
	case <-time.After(wait):
		return nil
	}
	if op, ok := res.(msg.StartOpMsg); ok {
		return []tea.Msg{op.Run(context.Background())}
	}
	batch, ok := res.(tea.BatchMsg)
	if !ok {
		if res == nil {
			return nil
		}
		return []tea.Msg{res}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, collectMsgsTimed(c, wait)...)
	}
	return out
}

// plain removes ANSI escape codes from s.
func plain(s string) string { return ansi.Strip(s) }

// deliverItems sends items to a list view as the result of its current
// load (gen c.loadGen), through the view's Update.
func deliverItems[T any](v tea.Model, c *listCore[T], items []T) {
	v.Update(listPageMsg[T]{gen: c.loadGen, items: items})
}

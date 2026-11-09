package views

import (
	"context"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/juthrbog/lazycloud/internal/msg"
)

// Shared helpers for the view tests.

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

package views

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/msg"
	"github.com/juthrbog/lazycloud/internal/ui"
)

// Helpers for actions on list rows that ask for a confirm first.

// pendingConfirm holds the targets of an action from the key press, through
// the picker, to the confirm reply.
type pendingConfirm struct {
	// action is the Action id of the confirm request, for example
	// "ec2_stop". It is empty until the view asks.
	action string
	// keys are the row keys of the targets, frozen at the key press.
	keys []string
}

// hold stores the targets when the user presses the action key.
func (p *pendingConfirm) hold(keys []string) {
	p.keys = keys
	p.action = ""
}

// ask records the Action of req and returns a command that sends req.
func (p *pendingConfirm) ask(req msg.RequestConfirmMsg) tea.Cmd {
	p.action = req.Action
	return func() tea.Msg { return req }
}

// clear forgets the targets.
func (p *pendingConfirm) clear() {
	p.keys = nil
	p.action = ""
}

// take returns the targets only for a confirmed reply to the recorded
// action. Every other reply clears them: a decline, the decline that the
// app sends for a skipped prompt, and a reply for another action.
func (p *pendingConfirm) take(m ui.ConfirmResultMsg) ([]string, bool) {
	keys, action := p.keys, p.action
	p.clear()
	if !m.Confirmed || action == "" || m.Action != action || len(keys) == 0 {
		return nil, false
	}
	return keys, true
}

// confirmMessage builds a confirm prompt, for example:
//
//	Stop 2 instance(s)?
//	  web (i-1)
//	  db (i-2)
//
// The names are capped and made safe by promptNameList. warning, when not
// empty, goes on the last line.
func confirmMessage(verb string, n int, noun string, names []string, warning string) string {
	text := fmt.Sprintf("%s %d %s?\n%s", verb, n, noun, promptNameList(names))
	if warning != "" {
		text += warning
	}
	return text
}

// runEach calls fn for each key in order. Each call gets its own detached
// context (see detached), so many keys do not share one time limit and
// closing the view does not stop the action. It goes on after a failure.
// names maps a key to the name for a failure, and the caller must copy it
// on the Update goroutine. A key with no name uses the key.
func runEach(ctx context.Context, keys []string, names map[string]string,
	fn func(ctx context.Context, key string) error,
) (done []string, failed []itemFailure) {
	for _, key := range keys {
		kctx, cancel := detached(ctx)
		err := fn(kctx, key)
		cancel()
		if err != nil {
			name := names[key]
			if name == "" {
				name = key
			}
			failed = append(failed, itemFailure{name: name, err: err})
			continue
		}
		done = append(done, key)
	}
	return done, failed
}

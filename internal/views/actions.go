package views

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/msg"
)

// Helpers for actions on list rows that ask for a confirm first.

// readOnlyText is the toast when ReadOnly mode is on at the reply of a
// prompt. The mode can change while the prompt is open. The app shows the
// same text when it blocks a ReadWrite key.
const readOnlyText = "ReadOnly mode — press W to switch"

// refuseReadOnly shows the ReadOnly toast in place of the action of a yes.
func refuseReadOnly() tea.Cmd {
	return func() tea.Msg { return msg.ToastError(readOnlyText) }
}

// pickedOption returns the option that r picked from opts, the options
// that the view froze in its tag. A reply that is not a pick, or an index
// out of range, returns false.
func pickedOption[T any](opts []T, r msg.PickerResultMsg) (T, bool) {
	var zero T
	if !r.Picked() || r.Selected < 0 || r.Selected >= len(opts) {
		return zero, false
	}
	return opts[r.Selected], true
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

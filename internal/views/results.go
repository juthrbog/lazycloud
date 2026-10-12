package views

import (
	"fmt"
	"strings"

	"github.com/juthrbog/lazycloud/internal/aws"
)

// itemFailure is one item that an action on several items could not change.
type itemFailure struct {
	name string
	err  error
}

// maxFailedNames is the number of failed items that a result text names.
// The event log has the full error of each one.
const maxFailedNames = 3

// failureText names the failed items with their AWS error codes, for
// example "orders (AccessDenied), billing (QueueDoesNotExist) and 2 more".
func failureText(failures []itemFailure) string {
	shown := failures[:min(len(failures), maxFailedNames)]
	parts := make([]string, len(shown))
	for i, f := range shown {
		parts[i] = fmt.Sprintf("%s (%s)", f.name, aws.ErrorCode(f.err))
	}
	text := strings.Join(parts, ", ")
	if n := len(failures) - len(shown); n > 0 {
		text += fmt.Sprintf(" and %d more", n)
	}
	return text
}

// resultText summarizes an action on total items. verb is in the past
// tense, for example "Deleted", and noun names the items, for example
// "queue(s)". ok is true when every item succeeded. Otherwise the text says
// how many items succeeded and names the failed ones.
func resultText(verb string, total int, noun string, failures []itemFailure) (text string, ok bool) {
	if len(failures) == 0 {
		return fmt.Sprintf("%s %d %s", verb, total, noun), true
	}
	return fmt.Sprintf("%s %d of %d %s. Failed: %s",
		verb, total-len(failures), total, noun, failureText(failures)), false
}

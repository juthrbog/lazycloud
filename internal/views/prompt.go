package views

import (
	"fmt"
	"strings"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// maxPromptNames is the number of names a confirm prompt lists.
const maxPromptNames = 10

// promptNameList returns the names as indented lines for a confirm prompt,
// so the user sees what an action applies to. After maxPromptNames names it
// adds one line with the number of names left. Each name stays on its own
// line: a line break in an S3 key shows as ␊, so a key cannot add a line to
// the prompt.
func promptNameList(names []string) string {
	var b strings.Builder
	for i, n := range names {
		if i == maxPromptNames {
			fmt.Fprintf(&b, "  and %d more\n", len(names)-maxPromptNames)
			break
		}
		fmt.Fprintf(&b, "  %s\n", ui.Sanitize(n))
	}
	return b.String()
}

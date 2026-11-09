package views

import (
	"fmt"
	"sort"
	"strings"

	"github.com/juthrbog/lazycloud/internal/msg"
)

// Helpers for the tabs of the detail panels.

// infoField is one line of an Info tab.
type infoField struct {
	K, V string
	// ViewID makes the line a link to that view. Empty means no link.
	ViewID string
	// Params are the parameters of the link.
	Params map[string]string
}

// buildInfo prints one "%-16s %s" line per field and skips a field with an
// empty value. A link points at the line of its field among the printed
// lines, so a skipped field does not move the links below it.
func buildInfo(fields []infoField) (string, []msg.TabLink) {
	var b strings.Builder
	var links []msg.TabLink
	line := 0
	for _, f := range fields {
		if f.V == "" {
			continue
		}
		fmt.Fprintf(&b, "%-16s %s\n", f.K, f.V)
		if f.ViewID != "" {
			links = append(links, msg.TabLink{Line: line, ViewID: f.ViewID, Params: f.Params})
		}
		line++
	}
	return b.String(), links
}

// yesNo returns "Yes" or "No".
func yesNo(v bool) string {
	if v {
		return "Yes"
	}
	return "No"
}

// buildTagsContent prints the tags sorted by key, one "%-24s %s" line each.
func buildTagsContent(tags map[string]string) string {
	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%-24s %s\n", k, tags[k])
	}
	return b.String()
}

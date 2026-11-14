package views

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/juthrbog/lazycloud/internal/ui"
)

// Helpers for the PR 0 characterization tests of the network lists
// (sg_list, vpc_list, subnet_list). They use only the public surface of a
// view, so the port PRs do not need to change them.

// netList is the public surface that the three network lists share.
type netList interface {
	tea.Model
	ID() string
	Title() string
	Footer() string
	KeyMap() []ui.HintBinding
	CapturingInput() bool
}

// netKeyMap returns one line per hint: keys, help key, help text, mode and
// category.
func netKeyMap(hints []ui.HintBinding) []string {
	out := make([]string, 0, len(hints))
	for _, h := range hints {
		out = append(out, fmt.Sprintf("%s | %s | %s | mode=%d | cat=%q",
			strings.Join(h.Keys(), ","), h.Help().Key, h.Help().Desc, h.Mode, h.Category))
	}
	return out
}

// netView returns the view text without ANSI codes.
func netView(v tea.Model) string { return plain(v.View().Content) }

// netLines returns the view lines without ANSI codes and trailing spaces.
func netLines(v tea.Model) []string {
	lines := strings.Split(netView(v), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return lines
}

var netGap = regexp.MustCompile(`\s{2,}`)

// netHeader returns the table header with " | " between the column titles.
func netHeader(v tea.Model) string {
	return netGap.ReplaceAllString(strings.TrimSpace(netLines(v)[0]), " | ")
}

// netRows returns the table body lines (after the header), each with
// " | " between the cells. Empty lines and the rule are left out.
func netRows(v tea.Model) []string {
	var out []string
	for _, l := range netLines(v)[1:] {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "─") {
			continue
		}
		out = append(out, netGap.ReplaceAllString(l, " | "))
	}
	return out
}

// netFooter returns the footer text without ANSI codes.
func netFooter(v netList) string { return plain(v.Footer()) }

// netNoTicks drops spinner ticks, so only the messages of the load remain.
func netNoTicks(msgs []tea.Msg) []tea.Msg {
	var out []tea.Msg
	for _, m := range msgs {
		if _, ok := m.(spinner.TickMsg); ok {
			continue
		}
		out = append(out, m)
	}
	return out
}

// netLoadMsg runs cmd and returns its only message that is not a spinner
// tick (the page or the error of a load), without naming its type.
func netLoadMsg(cmd tea.Cmd) tea.Msg {
	msgs := netNoTicks(collectMsgs(cmd))
	if len(msgs) != 1 {
		panic(fmt.Sprintf("want 1 load message, got %d: %#v", len(msgs), msgs))
	}
	return msgs[0]
}

// netCopied presses y and returns the toast texts.
func netCopied(v tea.Model) []string {
	_, cmd := v.Update(keyPress('y'))
	var out []string
	for _, t := range toasts(collectMsgs(cmd)) {
		out = append(out, t.Text)
	}
	return out
}

// netNoToken is the nil page token of a first page request.
var netNoToken *string

// netType sends a key press while the filter is open and returns the
// messages it produces. The text input also returns a cursor blink command
// that waits about half a second; it is skipped, so the test stays fast.
func netType(v tea.Model, k tea.KeyPressMsg) []tea.Msg {
	_, cmd := v.Update(k)
	return collectMsgsTimed(cmd, 100*time.Millisecond)
}

// netCols returns one line per column spec: title, width, weight, max width
// and whether the column has a Style.
func netCols(cols []ui.Column) []string {
	out := make([]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, fmt.Sprintf("%s w=%d weight=%d max=%d style=%t",
			c.Title, c.Width, c.Weight, c.MaxWidth, c.Style != nil))
	}
	return out
}

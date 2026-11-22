package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

// maxQueryRunes limits the length of a query. A large paste stops there. It
// is room for a long S3 object ARN, which a jump takes (docs/design/jump.md,
// H7): an S3 key has at most 1,024 bytes.
const maxQueryRunes = 2048

// queryInput is the one-line text that the picker, the command bar, and the
// help overlay type into (A9, L12). It takes the text of a key press, also
// space and letters outside ASCII, and the text of a paste. It removes
// control characters, so the text is safe to show.
type queryInput struct {
	text string
}

// String returns the text.
func (q queryInput) String() string { return q.text }

// set replaces the text.
func (q *queryInput) set(s string) {
	q.text = ""
	q.add(s)
}

// update applies a key press or a paste to the text. Backspace removes the
// last character. It returns true when the text changed. The caller handles
// its own keys, such as enter and esc, before it calls update.
func (q *queryInput) update(msg tea.Msg) bool {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "backspace" {
			return q.backspace()
		}
		// The decoder sets Text only for a key that types text. A
		// shortcut such as ctrl+a has none. AltGr, which types @ or { on
		// many keyboards, can come with ctrl and alt, so the modifiers
		// do not decide.
		return q.add(msg.Text)
	case tea.PasteMsg:
		return q.add(msg.Content)
	}
	return false
}

// add appends s without its control characters. A line break or a tab
// becomes a space, and the line break at the end of a paste goes away.
func (q *queryInput) add(s string) bool {
	s = strings.TrimRight(s, "\r\n")
	room := maxQueryRunes - utf8.RuneCountInString(q.text)
	var b strings.Builder
	for _, r := range s {
		if room <= 0 {
			break
		}
		switch {
		case r == '\n' || r == '\t':
			r = ' '
		case r == '\r':
			continue
		case r == utf8.RuneError || unicode.IsControl(r):
			continue
		}
		b.WriteRune(r)
		room--
	}
	if b.Len() == 0 {
		return false
	}
	q.text += b.String()
	return true
}

// backspace removes the last character, as the user sees it: an accent or
// an emoji that is more than one rune goes away with one key press.
func (q *queryInput) backspace() bool {
	if q.text == "" {
		return false
	}
	last := 0
	rest, state := q.text, -1
	for rest != "" {
		last = len(q.text) - len(rest)
		_, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
	}
	q.text = q.text[:last]
	return true
}

// view returns the text in at most width cells. When the text is wider, it
// shows the end, where the user types, after an ellipsis. With no room, it
// returns "".
func (q queryInput) view(width int) string {
	if width <= 0 {
		return ""
	}
	w := lipgloss.Width(q.text)
	if w <= width {
		return q.text
	}
	// TruncateLeft keeps a wide character when the cut falls on its left
	// half. Then cut one more cell.
	for n := w - width + 1; n < w; n++ {
		if out := ansi.TruncateLeft(q.text, n, "…"); lipgloss.Width(out) <= width {
			return out
		}
	}
	return "…"
}

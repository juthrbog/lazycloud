package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ReapplyAfterResets returns s with the escape sequence seq written again
// after each full SGR reset in s. It knows the two forms of the reset:
// "\x1b[0m", which chroma writes after each token, and "\x1b[m", which
// lipgloss writes.
//
// Use it to put an outer style, for example a background or faint text, on
// text that already has its own styles. Without it, the first reset in s
// also removes the outer style, so only the first token gets it.
//
// The resets stay in the text. A token that sets its own background or bold
// cannot leak into the next token, because the reset still clears it before
// seq starts again. The caller must end the text with its own reset, so
// that seq does not leak into the next line.
func ReapplyAfterResets(s, seq string) string {
	if seq == "" || !strings.Contains(s, "\x1b[") {
		return s
	}
	r := strings.NewReplacer(
		"\x1b[0m", "\x1b[0m"+seq,
		"\x1b[m", "\x1b[m"+seq,
	)
	return r.Replace(s)
}

// faintSeq is the SGR sequence that turns on faint text.
var faintSeq = ansi.NewStyle().Faint().String()

// faintStyle draws each line faint and ends each line with a reset.
var faintStyle = lipgloss.NewStyle().Faint(true)

// Dim returns s drawn faint, for the side of the screen that does not have
// focus. All the text is faint, also the text after a styled token.
func Dim(s string) string {
	return faintStyle.Render(ReapplyAfterResets(s, faintSeq))
}

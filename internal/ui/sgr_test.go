package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReapplyAfterResets(t *testing.T) {
	const bg = "\x1b[48;5;1m"
	tests := []struct {
		name string
		in   string
		seq  string
		want string
	}{
		{"no escapes", "plain", bg, "plain"},
		{"empty seq", "a\x1b[0mb", "", "a\x1b[0mb"},
		{"chroma reset", "\x1b[31ma\x1b[0mb", bg, "\x1b[31ma\x1b[0m" + bg + "b"},
		{"short reset", "\x1b[1ma\x1b[mb", bg, "\x1b[1ma\x1b[m" + bg + "b"},
		{"both forms", "a\x1b[0mb\x1b[mc", bg, "a\x1b[0m" + bg + "b\x1b[m" + bg + "c"},
		{"other SGR unchanged", "\x1b[38;5;10ma\x1b[39mb", bg, "\x1b[38;5;10ma\x1b[39mb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, ReapplyAfterResets(tt.in, tt.seq))
		})
	}
}

// assertStyleAfterEachReset checks that seq follows each reset in s, except
// the reset at the end of each line.
func assertStyleAfterEachReset(t *testing.T, s, seq string) {
	t.Helper()
	for line := range strings.SplitSeq(s, "\n") {
		// lipgloss pads a short line with spaces after the last reset.
		body := strings.TrimRight(line, " ")
		body = strings.TrimSuffix(body, "\x1b[m")
		body = strings.TrimSuffix(body, "\x1b[0m")
		for _, reset := range []string{"\x1b[0m", "\x1b[m"} {
			parts := strings.Split(body, reset)
			for _, after := range parts[1:] {
				assert.True(t, strings.HasPrefix(after, seq),
					"style is lost after a reset in line %q", line)
			}
		}
	}
}

func TestDimKeepsEveryTokenFaint(t *testing.T) {
	// Text like a table row or chroma output: each token ends with a reset.
	in := "\x1b[31mred\x1b[0m plain \x1b[1mbold\x1b[m end\n\x1b[32mgreen\x1b[0m tail"
	out := Dim(in)

	assertStyleAfterEachReset(t, out, faintSeq)
	for line := range strings.SplitSeq(out, "\n") {
		assert.True(t, strings.HasPrefix(line, faintSeq), "line %q does not start faint", line)
		assert.True(t, strings.HasSuffix(strings.TrimRight(line, " "), "\x1b[m"), "line %q does not end with a reset", line)
	}
}

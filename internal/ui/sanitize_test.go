package ui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Payloads that AWS text can carry. dcs is a DECRQSS query, which a terminal
// answers with input. csi8 is the C1 form of CSI, and "\u009b7m" turns on
// reverse video. osc52 writes to the clipboard.
const (
	dcs   = "\x1bP$qm\x1b\\"
	csi8  = "\u009b7m"
	osc52 = "\x1b]52;c;cHduZWQ=\x07"
)

// assertTerminalSafe fails when rendered output holds a part that reaches the
// terminal as a control: a string sequence, a C1 control character, a byte
// that is not valid UTF-8, or a C0 control other than ESC, line feed, and
// tab. ESC itself is allowed, because the styling uses SGR sequences.
func assertTerminalSafe(t *testing.T, out string) {
	t.Helper()
	assert.False(t, hasUnsafe(out), "output holds a string sequence, a C1 control, or invalid UTF-8: %q", out)
	for i := 0; i < len(out); i++ {
		if c := out[i]; c < 0x20 && c != 0x1b && c != '\n' && c != '\t' {
			assert.Failf(t, "C0 control in output", "byte %#x at %d in %q", c, i, out)
			return
		}
	}
	assert.NotContains(t, out, "\x1b]52", "OSC 52 reached the output")
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "logs/2026/app.log", "logs/2026/app.log"},
		{"unicode", "Grüße 日本 📄 ✓", "Grüße 日本 📄 ✓"},
		{"replacement char kept", "a�b", "a�b"},
		{"empty", "", ""},
		{"DCS query", "key-" + dcs + ".txt", "key-␛P$qm␛\\.txt"},
		{"OSC 52", osc52, "␛]52;c;cHduZWQ=␇"},
		{"line breaks and tab", "a\nb\r\nc\td", "a␊b␍␊c␉d"},
		{"NUL and US", "\x00\x1f", "␀␟"},
		{"DEL", "ab\x7f", "ab␡"},
		{"C1 CSI", "key-" + csi8 + ".txt", "key-�7m.txt"},
		{"C1 range ends", "\u0080\u009f ", "�� "},
		{"invalid UTF-8", "a\xffb\xc2", "a�b�"},
		{"raw 8-bit CSI byte", "a\x9b31mb", "a�31mb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Sanitize(tt.in))
		})
	}
}

func TestSanitizeLines(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"keeps line feed and tab", "a\n\tb\n", "a\n\tb\n"},
		{"drops CR of CRLF", "a\r\nb\r\n", "a\nb\n"},
		{"shows a lone CR", "visible\rHIDDEN", "visible␍HIDDEN"},
		{"CR at the end", "a\r", "a␍"},
		{"escape sequences", "x" + dcs + osc52 + csi8, "x␛P$qm␛\\␛]52;c;cHduZWQ=␇�7m"},
		{"BEL and BS", "a\a\bb", "a␇␈b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SanitizeLines(tt.in))
		})
	}
}

func TestSanitizeReturnsCleanTextUnchanged(t *testing.T) {
	clean := strings.Repeat("Grüße 日本 📄 ✓ │ logs/app.log ", 20)
	cleanLines := clean + "\n\tnext line"
	allocs := testing.AllocsPerRun(100, func() {
		_ = Sanitize(clean)
		_ = SanitizeLines(cleanLines)
	})
	assert.Zero(t, allocs)
}

func TestSanitizedTextIsTerminalSafe(t *testing.T) {
	in := "a" + dcs + osc52 + csi8 + "\r\a\b\x7f\xff\u0090z"
	assertTerminalSafe(t, Sanitize(in))
	assertTerminalSafe(t, SanitizeLines(in))
	assert.Equal(t, Sanitize(in), Sanitize(Sanitize(in)), "Sanitize must be idempotent")
}

func TestStripUnsafe(t *testing.T) {
	const sgr = "\x1b[1;38;2;1;2;3m"
	tests := []struct {
		name, in, want string
	}{
		{"plain screen", sgr + "│ name │\n╰──╯\x1b[m", sgr + "│ name │\n╰──╯\x1b[m"},
		{"hyperlink kept", "\x1b]8;;https://x\x1b\\x\x1b]8;;\x1b\\", "\x1b]8;;https://x\x1b\\x\x1b]8;;\x1b\\"},
		{"DCS with ST", "a" + dcs + "b", "ab"},
		{"APC, SOS, and PM", "a\x1b_Gx\x1b\\b\x1bXs\x1b\\c\x1b^p\x1b\\d", "abcd"},
		{"DCS ends at the next ESC", "a\x1bPdata" + sgr + "b", "a" + sgr + "b"},
		{"DCS without an end", "a\x1bPdata", "a"},
		{"C1 CSI dropped", "a" + csi8 + "b", "a7mb"},
		{"invalid UTF-8", "a\xffb", "a�b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, StripUnsafe(tt.in))
		})
	}
}

func TestStripUnsafeReturnsSafeScreenUnchanged(t *testing.T) {
	screen := strings.Repeat("\x1b[38;5;12m│ 📄 Grüße 日本 ✓\x1b[m\n", 40)
	allocs := testing.AllocsPerRun(100, func() { _ = StripUnsafe(screen) })
	assert.Zero(t, allocs)
}

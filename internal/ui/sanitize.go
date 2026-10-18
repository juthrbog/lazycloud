package ui

import (
	"strings"
	"unicode/utf8"
)

// Text from AWS can hold control characters: an S3 key, an object preview, a
// tag, a message body. A terminal acts on them. It can move the cursor, show
// text in reverse, or answer a query with input to the application. The
// Bubble Tea renderer also passes the string sequences DCS, SOS, PM, and APC
// on to the terminal unchanged. For these reasons, LazyCloud shows AWS text
// only after Sanitize or SanitizeLines.

// Sanitize returns s in a form that is safe to show on one line. It shows
// each C0 control character, line breaks and tabs included, as its Unicode
// control picture, for example ESC as ␛ and a line feed as ␊. It shows DEL
// as ␡. It shows each C1 control character, and each byte that is not valid
// UTF-8, as U+FFFD. It returns s unchanged when s has none of them.
func Sanitize(s string) string {
	return sanitize(s, false)
}

// SanitizeLines is Sanitize for text with more than one line, for example an
// object preview. It keeps line feeds and tabs, and it drops the carriage
// return of a CRLF line end. A carriage return in other places shows as ␍.
func SanitizeLines(s string) string {
	return sanitize(s, true)
}

func sanitize(s string, lines bool) string {
	if isClean(s, lines) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			b.WriteRune(utf8.RuneError)
		case lines && (r == '\n' || r == '\t'):
			b.WriteByte(byte(r))
		case lines && r == '\r' && i+1 < len(s) && s[i+1] == '\n':
			// The carriage return of a CRLF line end.
		case r < 0x20:
			b.WriteRune(0x2400 + r) // the control pictures ␀ to ␟
		case r == 0x7f:
			b.WriteRune('␡')
		case r >= 0x80 && r < 0xa0:
			// A C1 control character.
			b.WriteRune(utf8.RuneError)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}

// isClean reports whether sanitize leaves s unchanged. Most text is clean,
// so this check reads s without allocating.
func isClean(s string, lines bool) bool {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c >= 0x20 && c < 0x7f:
			i++
		case c < 0x80:
			if !lines || (c != '\n' && c != '\t') {
				return false
			}
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if (r == utf8.RuneError && size == 1) || r < 0xa0 {
				return false
			}
			i += size
		}
	}
	return true
}

// StripUnsafe removes from a rendered screen the parts that the renderer
// passes on to the terminal unchanged: the string sequences DCS, SOS, PM,
// and APC, and the C1 control characters. It shows each byte that is not
// valid UTF-8 as U+FFFD. LazyCloud does not draw any of these. Views show
// AWS text through Sanitize, so StripUnsafe is a second guard for text that
// reaches the screen without it.
//
// A string sequence ends at the next ESC. StripUnsafe removes that ESC too
// when it starts the terminator ESC \, as the ANSI parser does.
func StripUnsafe(view string) string {
	if !hasUnsafe(view) {
		return view
	}
	var b strings.Builder
	b.Grow(len(view))
	for i := 0; i < len(view); {
		c := view[i]
		switch {
		case c == 0x1b && i+1 < len(view) && isStringIntro(view[i+1]):
			end := strings.IndexByte(view[i+2:], 0x1b)
			switch {
			case end < 0:
				i = len(view)
			case i+2+end+1 < len(view) && view[i+2+end+1] == '\\':
				i += 2 + end + 2
			default:
				i += 2 + end
			}
		case c < 0x80:
			b.WriteByte(c)
			i++
		default:
			r, size := utf8.DecodeRuneInString(view[i:])
			switch {
			case r == utf8.RuneError && size == 1:
				b.WriteRune(utf8.RuneError)
			case r < 0xa0:
				// A C1 control character: drop it.
			default:
				b.WriteString(view[i : i+size])
			}
			i += size
		}
	}
	return b.String()
}

// hasUnsafe reports whether StripUnsafe changes view.
func hasUnsafe(view string) bool {
	for i := 0; i < len(view); {
		c := view[i]
		switch {
		case c == 0x1b && i+1 < len(view) && isStringIntro(view[i+1]):
			return true
		case c < 0x80:
			i++
		default:
			r, size := utf8.DecodeRuneInString(view[i:])
			if (r == utf8.RuneError && size == 1) || r < 0xa0 {
				return true
			}
			i += size
		}
	}
	return false
}

// isStringIntro reports whether ESC c starts a string sequence: DCS (P),
// SOS (X), PM (^), or APC (_).
func isStringIntro(c byte) bool {
	return c == 'P' || c == 'X' || c == '^' || c == '_'
}

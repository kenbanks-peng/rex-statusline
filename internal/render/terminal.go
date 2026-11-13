package render

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Sanitize strips terminal controls except numeric Select Graphic Rendition.
// In particular OSC hyperlinks/clipboard and cursor/erase sequences never pass.
func Sanitize(s string) string {
	var out strings.Builder
	runes := make([]rune, 0, len(s))
	for len(s) > 0 {
		r, n := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && n == 1 {
			r = rune(s[0])
		}
		runes = append(runes, r)
		s = s[n:]
	}
	for i := 0; i < len(runes); {
		r := runes[i]
		i++
		kind := r
		if r == 0x1b {
			if i == len(runes) {
				break
			}
			kind = runes[i]
			i++
			if kind == '[' {
				kind = 0x9b
			} else if kind == ']' {
				kind = 0x9d
			} else if kind == 'P' || kind == 'X' || kind == '^' || kind == '_' {
				kind = 0x90
			} else {
				for kind >= 0x20 && kind <= 0x2f && i < len(runes) {
					kind = runes[i]
					i++
				}
				continue
			}
		}
		if kind == 0x9b {
			start := i
			for i < len(runes) && !(runes[i] >= 0x40 && runes[i] <= 0x7e) {
				i++
			}
			if i == len(runes) {
				break
			}
			final := runes[i]
			params := runes[start:i]
			i++
			valid := final == 'm'
			for _, p := range params {
				if !(p >= '0' && p <= '9') && p != ';' && p != ':' {
					valid = false
				}
			}
			if valid {
				out.WriteString("\x1b[")
				out.WriteString(string(params))
				out.WriteByte('m')
			}
			continue
		}
		if kind == 0x9d || kind == 0x90 || kind == 0x98 || kind == 0x9e || kind == 0x9f {
			for i < len(runes) {
				c := runes[i]
				i++
				if c == 0x9c || (kind == 0x9d && c == 7) {
					break
				}
				if c == 0x1b && i < len(runes) && runes[i] == '\\' {
					i++
					break
				}
			}
			continue
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// Fit produces a single safe line with a reset, truncating whole graphemes.
func Fit(s string, columns int) string {
	s = Sanitize(s)
	if columns <= 0 {
		return "\x1b[0m"
	}
	return ansi.Truncate(s, columns, "") + "\x1b[0m"
}

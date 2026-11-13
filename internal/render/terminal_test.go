package render

import (
	"github.com/charmbracelet/x/ansi"
	"strings"
	"testing"
)

func TestSanitize(t *testing.T) {
	tests := map[string]string{
		"a\nb\rc\td\x00\x7f":                           "abcd",
		"\x1b[31mred\x1b[0m":                           "\x1b[31mred\x1b[0m",
		"a\x1b[2Jb\x1b[Hc\x1b[?25ld":                   "abcd",
		"a\x1b]52;c;c2VjcmV0\aZ":                       "aZ",
		"\x1b]8;;https://evil\x1b\\link\x1b]8;;\x1b\\": "link",
		"a\x1bPmalicious\x1b\\b":                       "ab",
		"a\u009d52;c;secret\u009cb":                    "ab",
		"a\x9b2Jb":                                     "ab",
		"a\x1b[1 qz":                                   "az",
		"a\x1b]unterminated":                           "a",
		"e\u0301界👩‍💻":                                  "e\u0301界👩‍💻",
	}
	for input, want := range tests {
		if got := Sanitize(input); got != want {
			t.Errorf("%q => %q want %q", input, got, want)
		}
	}
}
func TestFitGraphemes(t *testing.T) {
	for _, s := range []string{"e\u0301x", "👩‍💻x", "界x", "\x1b[31m👨‍👩‍👧‍👦x"} {
		for width := 0; width < 5; width++ {
			got := Fit(s, width)
			if ansi.StringWidth(got) > width || !strings.HasSuffix(got, "\x1b[0m") {
				t.Errorf("fit %q %d: %q", s, width, got)
			}
			plain := ansi.Strip(got)
			if strings.Contains(plain, "\u200d") && !strings.Contains(plain, "👩‍💻") && !strings.Contains(plain, "👨‍👩‍👧‍👦") {
				t.Fatalf("split grapheme: %q", got)
			}
		}
	}
}

package widget

import (
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// WrapPreservingLines word-wraps each line of text to width display columns,
// keeping blank lines and hard-breaking overlong tokens. Callers sanitize
// control characters before wrapping.
func WrapPreservingLines(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	var out []string
	for _, para := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.TrimSpace(para) == "" {
			out = append(out, "")
			continue
		}
		var cur strings.Builder
		curW := 0
		flush := func() {
			if curW > 0 {
				out = append(out, cur.String())
				cur.Reset()
				curW = 0
			}
		}
		for _, word := range strings.Fields(para) {
			for runewidth.StringWidth(word) > width {
				flush()
				head := runewidth.Truncate(word, width, "")
				if head == "" {
					// A single rune wider than the whole line; emit it alone.
					_, size := utf8.DecodeRuneInString(word)
					head = word[:size]
				}
				out = append(out, head)
				word = word[len(head):]
			}
			ww := runewidth.StringWidth(word)
			if ww == 0 {
				continue
			}
			if curW > 0 && curW+1+ww > width {
				flush()
			}
			if curW > 0 {
				cur.WriteByte(' ')
				curW++
			}
			cur.WriteString(word)
			curW += ww
		}
		flush()
	}
	return out
}

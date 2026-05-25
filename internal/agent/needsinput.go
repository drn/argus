package agent

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/drn/argus/internal/sanitize"
)

// needsInputSelectionRe is the visible-text signature of Claude Code's
// selection UI: U+276F (❯) followed (after zero or more horizontal whitespace
// characters) by "1.". The same widget renders AskUserQuestion overlays,
// permission prompts, and plan-mode confirmations — matching the shared UI
// shape catches all current and future variants without chasing wording.
//
// Claude renders the line in two paths depending on layout:
//
//  1. `\x1b[...m❯\x1b[39m \x1b[...m1.\x1b[39m` — literal space between glyphs,
//     which becomes `❯ 1.` after ANSI strip.
//  2. `\x1b[...m❯\x1b[3G\x1b[...m1.\x1b[39m` — a CSI cursor-horizontal-absolute
//     ESC[3G positions the cursor in column 3 before drawing `1.`. There is
//     no actual space byte in the stream; the visible gap is rendering. After
//     ANSI strip this becomes `❯1.`.
//
// `❯[ \t]*1\.` covers both. Trailing space variants ("❯  1.") fall in too.
var needsInputSelectionRe = regexp.MustCompile(`❯[ \t]*1\.`)

// needsInputTailWindow is how far back in the ring buffer we scan. Claude's
// prompt UI is rendered at the bottom of the viewport; the rendered cells
// live inside the most recent few KB of bytes (cursor moves + repaints). We
// scan a generous window so wide terminals with rich repaint sequences still
// match — and ANSI stripping shrinks the effective text further.
const needsInputTailWindow = 16 * 1024

// DetectNeedsInput returns true if the tail of `buf` indicates the agent is
// blocked waiting for the user. Two signals fire:
//
//  1. Claude's numbered-selection prompt UI (`❯ 1.`) — AskUserQuestion overlays,
//     permission prompts, and plan-mode confirms all render through this widget.
//  2. The assistant's most recent text response ends with `?` — captures
//     plain-text questions where Claude stops generating without invoking the
//     selection widget (e.g. "Want me to ship it?").
//
// Pair with an "is idle" check at the call site — a prompt the agent is still
// streaming past is not blocking.
func DetectNeedsInput(buf []byte) bool {
	if len(buf) == 0 {
		return false
	}
	tail := buf
	if len(tail) > needsInputTailWindow {
		tail = tail[len(tail)-needsInputTailWindow:]
	}
	stripped := sanitize.StripANSI(string(tail))
	if needsInputSelectionRe.MatchString(stripped) {
		return true
	}
	return endsInQuestion(stripped)
}

// endsInQuestion returns true when the assistant's last visible line of text
// — the line immediately above Claude's input prompt box — ends with `?` (or
// the full-width `？`).
//
// Anchoring on Claude's prompt-box opener (`╭`) is what makes the heuristic
// usable: every hint line below the input box (e.g. `? for shortcuts`) is
// excluded, and we only inspect the genuine transcript above it. Without the
// anchor, those hint lines would dominate the search and produce constant
// false positives on every idle session.
//
// When `╭` is absent — either because the buffer is too short or Claude has
// not rendered the prompt yet — we conservatively return false. The selection-
// UI branch above still fires in those cases when it's actually warranted.
func endsInQuestion(stripped string) bool {
	idx := strings.LastIndex(stripped, "╭")
	if idx < 0 {
		return false
	}
	above := stripped[:idx]
	// Walk backward through whatever sits above the prompt box, skipping
	// blank lines until we hit the last content line of the transcript.
	for {
		newline := strings.LastIndexByte(above, '\n')
		line := above[newline+1:]
		trimmed := strings.TrimRightFunc(line, unicode.IsSpace)
		if trimmed != "" {
			r, _ := utf8.DecodeLastRuneInString(trimmed)
			return r == '?' || r == '？'
		}
		if newline < 0 {
			return false
		}
		above = above[:newline]
	}
}

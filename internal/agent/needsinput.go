package agent

import (
	"regexp"

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

// DetectNeedsInput returns true if the tail of `buf` contains Claude's
// numbered-selection prompt UI signature, indicating the agent is blocked
// waiting for the user to pick an option (AskUserQuestion, permission prompt,
// plan-mode confirm). Pair with an "is idle" check at the call site — a
// prompt the agent is still streaming past is not blocking.
func DetectNeedsInput(buf []byte) bool {
	if len(buf) == 0 {
		return false
	}
	tail := buf
	if len(tail) > needsInputTailWindow {
		tail = tail[len(tail)-needsInputTailWindow:]
	}
	stripped := sanitize.StripANSI(string(tail))
	return needsInputSelectionRe.MatchString(stripped)
}

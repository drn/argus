package agent

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestDetectNeedsInput(t *testing.T) {
	cases := []struct {
		name string
		buf  string
		want bool
	}{
		{"empty", "", false},
		{"plain output", "Reading file foo.go\nDone.\n", false},
		{
			"claude permission prompt",
			"some prior output\n\nDo you want to proceed?\n❯ 1. Yes\n  2. Yes, and don't ask again\n  3. No\n",
			true,
		},
		{
			"claude edit prompt",
			"...\nDo you want to make this edit to internal/foo.go?\n❯ 1. Yes\n",
			true,
		},
		{
			"claude ask-user-question without 'Do you want to'",
			"Which library should we use for date formatting?\n\n❯ 1. date-fns\n  2. dayjs\n  3. luxon\n",
			true,
		},
		{
			"plain output with U+276F but no numbered selection",
			"prompt> ❯ ready\n",
			false,
		},
		{
			"plain markdown numbered list without selection cursor",
			"1. First item\n2. Second item\n3. Third item\n",
			false,
		},
		{
			"marker at end of buffer past tail-window slice point",
			strings.Repeat("x", needsInputTailWindow+1024) + "❯ 1. Yes",
			// inside the window because it lands at the very end
			true,
		},
		{
			"marker before tail window",
			"❯ 1. Yes" + strings.Repeat("x", needsInputTailWindow+1024),
			// older than the window — should NOT match
			false,
		},
		{
			// Real bytes captured from a Claude Code AskUserQuestion overlay
			// (first form). Each visible character sits inside its own SGR
			// color pair separated by a literal space, so after ANSI strip
			// we get "❯ 1.". If this regresses, the live TUI silently misses
			// the spaced-form prompt.
			"claude askuserquestion with interleaved sgr escapes",
			"\x1b[38;2;177;185;249m❯\x1b[39m \x1b[38;2;153;153;153m1.\x1b[39m \x1b[38;2;177;185;249mYes\x1b[39m",
			true,
		},
		{
			// Real bytes captured from a Claude Code AskUserQuestion overlay
			// (second form). Claude positions "1." using a CSI cursor-
			// horizontal-absolute (`\x1b[3G`) instead of emitting a space.
			// After ANSI strip the visible text collapses to "❯1." — no
			// space character anywhere in the byte stream. The detector
			// regex must tolerate zero whitespace between ❯ and 1.
			"claude askuserquestion with CSI cursor-positioning between glyphs",
			"\x1b[38;2;177;185;249m❯\x1b[3G\x1b[38;2;153;153;153m1.\x1b[39m \x1b[38;2;177;185;249mYes\x1b[39m",
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testutil.Equal(t, DetectNeedsInput([]byte(c.buf)), c.want)
		})
	}
}

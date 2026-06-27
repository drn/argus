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
		{
			"plain text question above prompt box fires",
			"⏺ Want me to ship it?\n\n╭───╮\n│ > │\n╰───╯\n  ? for shortcuts\n",
			true,
		},
		{
			"plain text statement above prompt box does not fire",
			"⏺ Shipped it.\n\n╭───╮\n│ > │\n╰───╯\n  ? for shortcuts\n",
			false,
		},
		{
			"full-width question mark above prompt box fires",
			"⏺ 准备好了？\n\n╭───╮\n│ > │\n╰───╯\n",
			true,
		},
		{
			"hint line below prompt box must not dominate the search",
			"⏺ All done.\n\n╭───╮\n│ > │\n╰───╯\n  ? for shortcuts\n",
			false,
		},
		{
			"trailing whitespace after question mark still fires",
			"⏺ Ready?   \n\n╭───╮\n│ > │\n╰───╯\n",
			true,
		},
		{
			"no prompt box present — question-mark heuristic skipped",
			"⏺ Want me to ship it?\n",
			false,
		},
		{
			"multiple blank lines between transcript and prompt box are skipped",
			"⏺ Want me to ship it?\n\n\n\n╭───╮\n│ > │\n╰───╯\n",
			true,
		},
		{
			// Shape captured from a real session log (~/.argus/sessions): the
			// current Claude Code UI renders the idle input prompt as ❯ + NBSP
			// (U+00A0) with no ╭ box anywhere, visual lines are separated by
			// bare \r, and a spinner-glyph timing line ("✻ Brewed for 57s")
			// sits between the transcript and the prompt.
			"new-UI question with timing line above bare-prompt fires",
			"⏺ Does Section 1 look right?\r✻ Brewed for 57s\r\r❯\u00a0 \r\r",
			true,
		},
		{
			"new-UI statement with timing line above bare-prompt does not fire",
			"⏺ Shipped it.\r✻ Worked for 28m 5s\r\r❯\u00a0 \r\r",
			false,
		},
		{
			"new-UI question with multi-word duration and different verb fires",
			"⏺ Want me to ship it?\r✻ Cogitated for 6m 56s\r\r❯\u00a0\r · ← for agents\r\r",
			true,
		},
		{
			"new-UI question with alternate spinner glyph on timing line fires",
			"⏺ Ready to proceed?\r✶ Pondered for 12s\r\r❯\u00a0 \r\r",
			true,
		},
		{
			"new-UI question without timing line fires",
			"⏺ Does Section 1 look right?\r\r❯\u00a0 \r\r",
			true,
		},
		{
			"new-UI hint lines below prompt do not dominate",
			"⏺ All done.\r✻ Brewed for 57s\r\r❯\u00a0 \r\r" +
				"──────────────────\r\r⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents\r\r",
			false,
		},
		{
			"old-UI timing line between question and prompt box is skipped",
			"⏺ Does Section 1 look right?\n\n✻ Brewed for 57s\n\n╭───╮\n│ > │\n╰───╯\n",
			true,
		},
		{
			"separator rule above prompt is skipped",
			"⏺ Want me to ship it?\r────────────\r❯\u00a0 \r\r",
			true,
		},
		{
			"bare prompt with nothing above does not fire",
			"❯\u00a0 \r\r",
			false,
		},
		{
			// A transcript that merely *contains* ❯+NBSP earlier must not
			// anchor there: the prompt signature anchors on the LAST match,
			// and content below the real prompt is hint/status chrome.
			"question above latest prompt wins over earlier prompt occurrence",
			"❯\u00a0 \r\r⏺ Shipped it.\r✻ Brewed for 3s\r\r❯\u00a0 \r\r",
			false,
		},

		// AskUserQuestion chooser — footer signature
		{
			// AskUserQuestion renders a full-screen chooser widget. The distinctive
			// signature is the footer line containing both "Enter to select" and
			// "Esc to cancel". The ❯ cursor on the option does NOT follow the "❯ 1."
			// numbered format, so the existing selection regex doesn't catch it.
			"askuserquestion chooser footer fires",
			"  ❯  Execute in this session\n     Copy to clipboard\n\n  Enter to select · \u2191/\u2193 to navigate · Esc to cancel\n",
			true,
		},
		{
			// AskUserQuestion with a box border — footer inside a box still fires
			"askuserquestion chooser in box fires",
			"\u256d\u2500 How should we proceed? \u256e\n\u2502 ❯  Execute in this session  \u2502\n\u2502    Copy to clipboard          \u2502\n\u251c\u2500\u2500\u2500\u251e\n\u2502  Enter to select · \u2191/\u2193 to navigate · Esc to cancel \u2502\n\u2570\u2500\u2500\u2500\u256f\n",
			true,
		},
		{
			// Real ANSI-wrapped footer: each phrase in dim SGR pairs, separated
			// by middle-dot separators. After ANSI strip it collapses to plain text.
			"askuserquestion chooser with ANSI-wrapped footer fires",
			"\x1b[2mEnter to select\x1b[0m · \x1b[2m\u2191/\u2193 to navigate\x1b[0m · \x1b[2mEsc to cancel\x1b[0m",
			true,
		},
		{
			// Only "Esc to cancel" present — not enough without "Enter to select"
			"esc-to-cancel alone does not fire",
			"Press Esc to cancel the current operation.\n❯\u00a0 \r\r",
			false,
		},
		{
			// Only "Enter to select" present — not enough without "Esc to cancel"
			"enter-to-select alone does not fire",
			"Press Enter to select a file from the list.\n❯\u00a0 \r\r",
			false,
		},
		{
			// Both phrases on separate lines — must NOT match; chooser footer is
			// always a single continuous line with both phrases.
			"chooser phrases on separate lines do not fire",
			"Enter to select.\nPress Esc to cancel.\n❯\u00a0 \r\r",
			false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testutil.Equal(t, DetectNeedsInput([]byte(c.buf)), c.want)
		})
	}
}

// TestContentFingerprint pins the BUG-032 discriminator: a session parked at a
// prompt that only emits redraw/animation chrome must fingerprint STABLY, while
// a session producing genuinely new transcript content must fingerprint
// differently. The content-stability detector relies on exactly this property
// to flag a never-idle blocked session without false-positiving on a streaming
// one.
func TestContentFingerprint(t *testing.T) {
	// Two snapshots of the SAME parked permission prompt, differing only in
	// animation chrome: the spinner timing line ticks (3s → 7s, glyph cycles)
	// and cursor-positioning/blink escapes are emitted on the prompt line.
	frame := func(secs string, glyph string) string {
		return "⏺ Do you want to proceed with this edit?\r" +
			glyph + " Brewed for " + secs + "\r\r" +
			"❯ 1. Yes\r  2. Yes, and don't ask again\r  3. No\r\r" +
			"\x1b[?25l\x1b[2;5H❯\x1b[?25h  \r\r"
	}
	parkedA := []byte(frame("3s", "✻"))
	parkedB := []byte(frame("7s", "✶")) // later: more seconds, different spinner glyph

	t.Run("animation-only changes fingerprint identically", func(t *testing.T) {
		testutil.Equal(t, ContentFingerprint(parkedA), ContentFingerprint(parkedB))
	})

	t.Run("both parked snapshots are still detected as needs-input", func(t *testing.T) {
		testutil.Equal(t, DetectNeedsInput(parkedA), true)
		testutil.Equal(t, DetectNeedsInput(parkedB), true)
	})

	t.Run("new transcript content changes the fingerprint", func(t *testing.T) {
		streaming1 := []byte("⏺ Reading internal/foo.go\r✻ Brewed for 3s\r\r")
		streaming2 := []byte("⏺ Reading internal/foo.go\r⏺ Editing internal/bar.go\r✻ Brewed for 4s\r\r")
		if ContentFingerprint(streaming1) == ContentFingerprint(streaming2) {
			t.Fatal("fingerprint did not change when new transcript content arrived")
		}
	})

	t.Run("repaint count does not destabilize the fingerprint", func(t *testing.T) {
		// Same static frame buffered a different number of times (alt-screen
		// repaint): the trailing-line anchor must keep the fingerprint stable.
		one := []byte(frame("3s", "✻"))
		three := []byte(frame("3s", "✻") + frame("4s", "✶") + frame("5s", "✻"))
		testutil.Equal(t, ContentFingerprint(one), ContentFingerprint(three))
	})
}

func TestBlockedOnPrompt(t *testing.T) {
	t.Run("nil session is not blocked", func(t *testing.T) {
		testutil.Equal(t, BlockedOnPrompt(nil), false)
	})
	t.Run("plain output is not blocked", func(t *testing.T) {
		sess := &fakeSession{tail: []byte("Reading foo.go\nDone.\n")}
		testutil.Equal(t, BlockedOnPrompt(sess), false)
	})
	t.Run("selection-UI overlay is blocked", func(t *testing.T) {
		sess := &fakeSession{tail: []byte("Do you want to proceed?\n❯ 1. Yes\n  2. No\n")}
		testutil.Equal(t, BlockedOnPrompt(sess), true)
	})
}

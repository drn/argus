package agent

import (
	"strings"
	"testing"
	"time"

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
			// BUG-035 GAP B: the user navigated the permission cursor down to
			// option 2, so ❯ sits on "2." not "1.". Anchoring on "1." silently
			// missed this still-blocking prompt.
			"claude permission prompt with cursor navigated to option 2",
			"Do you want to proceed?\n  1. Yes\n❯ 2. Yes, and don't ask again\n  3. No\n",
			true,
		},
		{
			"claude permission prompt with cursor navigated to option 3",
			"Do you want to proceed?\n  1. Yes\n  2. Yes, and don't ask again\n❯ 3. No\n",
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
			// BUG-035 GAP B: footer wording has drifted across releases. The
			// matcher tolerates "confirm"/"choose" verbs and "Esc to go back".
			"askuserquestion chooser footer with 'confirm' verb fires",
			"  ❯  Option A\n     Option B\n\n  ↑/↓ to navigate · Enter to confirm · Esc to go back\n",
			true,
		},
		{
			"askuserquestion chooser footer with 'choose' verb and lowercase fires",
			"  enter to choose · esc to cancel\n",
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

// TestDetectSelectionPrompt pins the stricter signal the content-stability pass
// relies on: only the unambiguous selection widget (❯ 1. / chooser footer)
// fires, NEVER the fuzzy trailing-question heuristic. A busy agent whose last
// line ends in `?` above the input box must not qualify when the idle gate is
// removed.
func TestDetectSelectionPrompt(t *testing.T) {
	cases := []struct {
		name string
		buf  string
		want bool
	}{
		{"numbered selection fires", "Do you want to proceed?\n❯ 1. Yes\n  2. No\n", true},
		{"chooser footer fires", "  ❯  Execute\n     Copy\n\n  Enter to select · Esc to cancel\n", true},
		{
			// endsInQuestion would make DetectNeedsInput true here, but there is
			// NO selection widget — the stability pass must NOT treat this as
			// blocked.
			"trailing-question above prompt box does NOT fire",
			"⏺ Want me to ship it?\n\n╭───╮\n│ > │\n╰───╯\n  ? for shortcuts\n",
			false,
		},
		{"plain output does not fire", "Reading foo.go\nDone.\n", false},
		{"empty does not fire", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testutil.Equal(t, DetectSelectionPrompt([]byte(c.buf)), c.want)
			// Sanity: the trailing-question case IS caught by the broader
			// DetectNeedsInput, proving the two predicates genuinely differ.
			if c.name == "trailing-question above prompt box does NOT fire" {
				testutil.Equal(t, DetectNeedsInput([]byte(c.buf)), true)
			}
		})
	}
}

// altScreenPromptFrame builds a fullscreen (alt-screen) selection-prompt frame
// the way a cursor-addressed renderer paints it: the option text is written
// first, then the ❯ selection cursor is painted LAST at an absolute position to
// the LEFT of "1.". In byte order the ❯ therefore TRAILS "1.", so the raw
// `❯ … 1.` regex misses; only after vt emulation places the glyphs on the
// screen do they line up as "❯ 1.". `secs`/`glyph` vary the spinner timing
// chrome so successive frames differ in raw bytes but not in rendered content.
func altScreenPromptFrame(secs, glyph string) string {
	return "\x1b[?1049h\x1b[2J" +
		"\x1b[1;1H" + glyph + " Brewed for " + secs +
		"\x1b[3;5H\x1b[38;2;200;200;200mDo you want to proceed?\x1b[39m" +
		"\x1b[5;5H1. Yes" +
		"\x1b[6;5H2. No" +
		"\x1b[5;3H\x1b[38;2;177;185;249m❯\x1b[39m" +
		"\x1b[8;1H\x1b[?25l" // park cursor, hide it (animation chrome)
}

// TestDetectNeedsInputScreen pins BUG-033: a fullscreen agent's cursor-addressed
// prompt is invisible to the raw-StripANSI detector but visible once emulated.
func TestDetectNeedsInputScreen(t *testing.T) {
	altScreen := []byte(altScreenPromptFrame("3s", "✻"))

	t.Run("raw detection misses the alt-screen prompt", func(t *testing.T) {
		// The bug: cursor positioning is stripped, not applied, so ❯ and 1. are
		// not adjacent in the raw stream.
		testutil.Equal(t, DetectNeedsInput(altScreen), false)
		testutil.Equal(t, DetectSelectionPrompt(altScreen), false)
	})

	t.Run("emulated detection catches the alt-screen prompt", func(t *testing.T) {
		r := &ScreenRenderer{}
		testutil.Equal(t, DetectNeedsInputScreen(r, altScreen, 80, 24), true)
	})

	t.Run("nil renderer falls back to raw (== DetectNeedsInput)", func(t *testing.T) {
		testutil.Equal(t, DetectNeedsInputScreen(nil, altScreen, 80, 24), false)
	})

	t.Run("linear main-screen prompt still fires via the raw fast path", func(t *testing.T) {
		linear := []byte("Do you want to proceed?\n❯ 1. Yes\n  2. No\n")
		r := &ScreenRenderer{}
		testutil.Equal(t, DetectNeedsInputScreen(r, linear, 80, 24), true)
	})

	t.Run("plain alt-screen output without a prompt does not fire", func(t *testing.T) {
		busy := []byte("\x1b[?1049h\x1b[2J\x1b[2;5HReading internal/foo.go\x1b[3;5HEditing internal/bar.go")
		r := &ScreenRenderer{}
		testutil.Equal(t, DetectNeedsInputScreen(r, busy, 80, 24), false)
	})

	t.Run("renderer reuse across different sessions/sizes stays correct", func(t *testing.T) {
		r := &ScreenRenderer{}
		// First render a wide alt-screen prompt, then a plain narrow frame, then
		// the prompt again: RIS reset + resize must not bleed state between them.
		testutil.Equal(t, DetectNeedsInputScreen(r, altScreen, 120, 40), true)
		busy := []byte("\x1b[?1049h\x1b[2J\x1b[2;5HWorking...")
		testutil.Equal(t, DetectNeedsInputScreen(r, busy, 80, 24), false)
		testutil.Equal(t, DetectNeedsInputScreen(r, altScreen, 80, 24), true)
	})
}

// altScreenQuestionFrame builds a fullscreen (alt-screen) FREE-TEXT-question
// frame: the agent has finished a turn on a plain question (no numbered
// selection widget) and is parked at the idle input prompt. `working` toggles
// Claude's interrupt affordance ("esc to interrupt") on the spinner line: when
// present the agent is still generating (must NOT be flagged); when absent the
// agent is genuinely awaiting input (must be flagged). `secs`/`glyph` vary the
// spinner timing chrome so successive frames differ in raw bytes but not in
// rendered content.
func altScreenQuestionFrame(secs, glyph string, working bool) string {
	spinner := glyph + " Brewed for " + secs
	if working {
		spinner = glyph + " Cogitating… (" + secs + " · esc to interrupt)"
	}
	return "\x1b[?1049h\x1b[2J" +
		"\x1b[3;5H\x1b[38;2;200;200;200m⏺ Should I go ahead and ship it?\x1b[39m" +
		"\x1b[5;1H" + spinner +
		"\x1b[7;1H\x1b[38;2;177;185;249m❯ \x1b[39m" + // idle input prompt (❯ + NBSP)
		"\x1b[8;1H\x1b[?25l" // park cursor, hide it (animation chrome)
}

// TestAwaitingInputFingerprint pins the never-idle content-stability path for
// BOTH awaiting-input shapes: the unambiguous selection widget (BUG-032/033) and
// the gated free-text trailing question (BUG-035). A parked frame is stable
// across animation-only ticks (so the 2nd tick flags it); a working agent or a
// streaming agent is never flagged.
func TestAwaitingInputFingerprint(t *testing.T) {
	t.Run("linear prompt fingerprints the raw tail and matches ContentFingerprint", func(t *testing.T) {
		linear := []byte("Do you want to proceed?\n❯ 1. Yes\n  2. No\n")
		r := &ScreenRenderer{}
		fp, ok := AwaitingInputFingerprint(r, linear, 80, 24)
		testutil.Equal(t, ok, true)
		testutil.Equal(t, fp, ContentFingerprint(linear))
	})

	t.Run("alt-screen prompt is detected and stable across animation-only ticks", func(t *testing.T) {
		r := &ScreenRenderer{}
		// Two frames of the SAME parked prompt differing only in spinner chrome
		// (3s/✻ → 7s/✶). The raw bytes differ; the rendered prompt screen does
		// not (the spinner timing line is volatile-stripped from the fingerprint).
		fpA, okA := AwaitingInputFingerprint(r, []byte(altScreenPromptFrame("3s", "✻")), 80, 24)
		fpB, okB := AwaitingInputFingerprint(r, []byte(altScreenPromptFrame("7s", "✶")), 80, 24)
		testutil.Equal(t, okA, true)
		testutil.Equal(t, okB, true)
		testutil.Equal(t, fpA, fpB)
	})

	t.Run("streaming alt-screen content without a prompt is never flagged", func(t *testing.T) {
		r := &ScreenRenderer{}
		busy := []byte("\x1b[?1049h\x1b[2J\x1b[2;5HReading internal/foo.go\x1b[3;5HEditing internal/bar.go")
		_, ok := AwaitingInputFingerprint(r, busy, 80, 24)
		testutil.Equal(t, ok, false)
	})

	t.Run("alt-screen prompt with changing transcript shifts the fingerprint", func(t *testing.T) {
		r := &ScreenRenderer{}
		// Same prompt widget but the visible transcript line above it changes,
		// so the rendered-screen fingerprint must differ (genuine new content).
		frame := func(line string) string {
			return "\x1b[?1049h\x1b[2J" +
				"\x1b[3;5H" + line +
				"\x1b[5;5H1. Yes\x1b[6;5H2. No\x1b[5;3H❯"
		}
		fp1, ok1 := AwaitingInputFingerprint(r, []byte(frame("Applying edit 1 of 3")), 80, 24)
		fp2, ok2 := AwaitingInputFingerprint(r, []byte(frame("Applying edit 2 of 3")), 80, 24)
		testutil.Equal(t, ok1, true)
		testutil.Equal(t, ok2, true)
		if fp1 == fp2 {
			t.Fatal("fingerprint did not change when the visible transcript changed")
		}
	})

	// BUG-035 GAP A: a fullscreen agent parked at a FREE-TEXT question (no
	// selection widget) is invisible to the selection-only stability pass. It
	// must be flagged when the "working" affordance is ABSENT, and must NOT be
	// flagged while the agent is still working (interrupt hint present).
	t.Run("alt-screen free-text question with no working affordance is flagged and stable", func(t *testing.T) {
		r := &ScreenRenderer{}
		// Sanity: the raw byte stream does NOT contain a selection widget (this
		// is a free-text question), so this exercises the emulated-screen path.
		raw := []byte(altScreenQuestionFrame("3s", "✻", false))
		testutil.Equal(t, DetectSelectionPrompt(raw), false)

		fpA, okA := AwaitingInputFingerprint(r, []byte(altScreenQuestionFrame("3s", "✻", false)), 80, 24)
		fpB, okB := AwaitingInputFingerprint(r, []byte(altScreenQuestionFrame("9s", "✶", false)), 80, 24)
		testutil.Equal(t, okA, true)
		testutil.Equal(t, okB, true)
		testutil.Equal(t, fpA, fpB) // only spinner chrome changed → stable
	})

	t.Run("alt-screen free-text question WHILE WORKING is not flagged (BUG-032 guard)", func(t *testing.T) {
		r := &ScreenRenderer{}
		// Same "?"-ending transcript, but the working affordance ("esc to
		// interrupt") is present → the agent is still generating, not awaiting.
		_, ok := AwaitingInputFingerprint(r, []byte(altScreenQuestionFrame("3s", "✻", true)), 80, 24)
		testutil.Equal(t, ok, false)
	})

	t.Run("linear free-text question raw fast path: awaiting flagged, working not", func(t *testing.T) {
		r := &ScreenRenderer{}
		awaiting := []byte("⏺ Should I go ahead?\r✻ Brewed for 8s\r\r❯  \r\r")
		_, okAwait := AwaitingInputFingerprint(r, awaiting, 80, 24)
		testutil.Equal(t, okAwait, true)

		working := []byte("⏺ Should I go ahead?\r✻ Cogitating… (8s · esc to interrupt)\r\r❯  \r\r")
		_, okWork := AwaitingInputFingerprint(r, working, 80, 24)
		testutil.Equal(t, okWork, false)
	})
}

// TestParkedSelectionSignal pins the BUG-029 escalation heuristic's per-tick
// qualifying condition: the unambiguous selection-prompt shape present AND the
// "working" affordance absent, over either the raw tail (linear fast path) or
// the emulated screen (alt-screen fallback) — mirroring the
// raw-first-then-emulated pattern used throughout this file.
func TestParkedSelectionSignal(t *testing.T) {
	t.Run("linear parked selection prompt qualifies", func(t *testing.T) {
		r := &ScreenRenderer{}
		buf := []byte("Do you want to proceed?\n❯ 1. Yes\n  2. No\n")
		testutil.Equal(t, ParkedSelectionSignal(r, buf, 80, 24), true)
	})

	t.Run("linear selection prompt WHILE WORKING does not qualify", func(t *testing.T) {
		r := &ScreenRenderer{}
		buf := []byte("❯ 1. Yes\r  2. No\r✻ Cogitating… (8s · esc to interrupt)\r")
		testutil.Equal(t, ParkedSelectionSignal(r, buf, 80, 24), false)
	})

	t.Run("no selection prompt does not qualify", func(t *testing.T) {
		r := &ScreenRenderer{}
		buf := []byte("Reading foo.go\nDone.\n")
		testutil.Equal(t, ParkedSelectionSignal(r, buf, 80, 24), false)
	})

	t.Run("empty buffer does not qualify", func(t *testing.T) {
		testutil.Equal(t, ParkedSelectionSignal(&ScreenRenderer{}, nil, 80, 24), false)
	})

	t.Run("alt-screen parked selection prompt qualifies via the emulated screen", func(t *testing.T) {
		r := &ScreenRenderer{}
		testutil.Equal(t, ParkedSelectionSignal(r, []byte(altScreenPromptFrame("3s", "✻")), 80, 24), true)
	})

	t.Run("alt-screen selection prompt WHILE WORKING does not qualify", func(t *testing.T) {
		r := &ScreenRenderer{}
		testutil.Equal(t, ParkedSelectionSignal(r, []byte(altScreenQuestionFrame("3s", "✻", true)), 80, 24), false)
	})

	t.Run("nil renderer disables the alt-screen fallback", func(t *testing.T) {
		testutil.Equal(t, ParkedSelectionSignal(nil, []byte(altScreenPromptFrame("3s", "✻")), 80, 24), false)
	})
}

// TestEscalateParkedSelection pins the BUG-029/BUG-060 bounded-escalation
// counter: a pure (prevTicks, qualifies) -> (newTicks, escalated) step
// function. A streak of zero stays at zero on a miss (nothing to lose); an
// ONGOING streak's first miss is held in a one-tick GRACE period (a negative
// sentinel) rather than discarded, recovering in full if the very next tick
// qualifies again; a SECOND consecutive miss confirms a genuine break and
// resets for real. Escalates only once a qualifying tick's resumed/continued
// streak reaches NeedsInputEscalationTicks.
func TestEscalateParkedSelection(t *testing.T) {
	t.Run("non-qualifying tick with no prior streak stays at zero and never escalates", func(t *testing.T) {
		ticks, escalated := EscalateParkedSelection(0, false)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, escalated, false)
	})

	t.Run("qualifying streak escalates exactly at the threshold, not before", func(t *testing.T) {
		ticks := 0
		escalated := false
		for i := 0; i < NeedsInputEscalationTicks-1; i++ {
			ticks, escalated = EscalateParkedSelection(ticks, true)
			testutil.Equal(t, escalated, false)
		}
		testutil.Equal(t, ticks, NeedsInputEscalationTicks-1)
		ticks, escalated = EscalateParkedSelection(ticks, true)
		testutil.Equal(t, ticks, NeedsInputEscalationTicks)
		testutil.Equal(t, escalated, true)
	})

	// BUG-060: an ISOLATED single-tick miss (a blink-off cursor frame, a torn
	// read of the concurrently-written session log) must not cost an ongoing
	// streak — it is held in grace and fully recovered the moment the very
	// next tick qualifies again. Under the old all-or-nothing reset, a session
	// whose detection missed roughly once every few ticks (a realistic cadence
	// for either noise source) could NEVER reach the threshold even though it
	// never stopped being genuinely parked.
	t.Run("an isolated single miss holds the streak in grace and fully recovers", func(t *testing.T) {
		ticks := NeedsInputEscalationTicks - 1 // one tick away from escalating
		ticks, escalated := EscalateParkedSelection(ticks, false)
		if ticks >= 0 {
			t.Fatalf("expected a negative grace sentinel preserving the streak, got %d", ticks)
		}
		testutil.Equal(t, escalated, false)
		// The very next qualifying tick resumes at N+1, not 1 — full recovery.
		ticks, escalated = EscalateParkedSelection(ticks, true)
		testutil.Equal(t, ticks, NeedsInputEscalationTicks)
		testutil.Equal(t, escalated, true)
	})

	// BUG-060: once ALREADY escalated, a single grace-held miss must not
	// visibly flicker the flag off for that one tick — the caller's `escalated`
	// bool is the direct driver of whether a role shows "(?)" this tick, so a
	// negative (grace) newTicks that momentarily reports escalated=false would
	// flicker the rail glyph off and back on every time this recurs.
	t.Run("already-escalated streak stays escalated through a grace tick", func(t *testing.T) {
		ticks := NeedsInputEscalationTicks
		ticks, escalated := EscalateParkedSelection(ticks, false) // isolated miss, already past threshold
		if ticks >= 0 {
			t.Fatalf("expected a negative grace sentinel, got %d", ticks)
		}
		testutil.Equal(t, escalated, true)
		// Confirm the break on a second consecutive miss: THEN it de-escalates.
		ticks, escalated = EscalateParkedSelection(ticks, false)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, escalated, false)
	})

	t.Run("two consecutive misses confirm a genuine break and reset for real", func(t *testing.T) {
		ticks := NeedsInputEscalationTicks - 1
		ticks, escalated := EscalateParkedSelection(ticks, false) // 1st miss: grace
		testutil.Equal(t, escalated, false)
		ticks, escalated = EscalateParkedSelection(ticks, false) // 2nd consecutive miss: confirmed break
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, escalated, false)
		// Resuming after a REAL break starts over from 1, not N.
		ticks, escalated = EscalateParkedSelection(ticks, true)
		testutil.Equal(t, ticks, 1)
		testutil.Equal(t, escalated, false)
	})

	t.Run("sparse isolated matches while otherwise missing never accumulate (anti-false-positive)", func(t *testing.T) {
		// A busy/streaming agent that only OCCASIONALLY, coincidentally matches
		// the selection shape for a single tick amid many misses must never
		// escalate — each isolated match is surrounded by 2+ misses, which
		// confirms a break before any credit can build past 1-2 ticks.
		ticks := 0
		var escalated bool
		pattern := []bool{false, false, false, true, false, false, true, false, false, false, true, false, false}
		for _, q := range pattern {
			ticks, escalated = EscalateParkedSelection(ticks, q)
			if escalated {
				t.Fatalf("escalated on a sparse/non-continuous match pattern: %v", pattern)
			}
		}
		if ticks > 1 {
			t.Fatalf("expected no meaningful accumulated credit from sparse matches, got %d", ticks)
		}
	})
}

func TestResumeActivityTick(t *testing.T) {
	t.Run("a single non-working tick with no prior streak stays at zero and never resumes", func(t *testing.T) {
		ticks, resumed := ResumeActivityTick(0, false)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, resumed, false)
	})

	t.Run("a sustained working streak resumes exactly at the threshold, not before", func(t *testing.T) {
		ticks := 0
		resumed := false
		for i := 0; i < NeedsInputResumeTicks-1; i++ {
			ticks, resumed = ResumeActivityTick(ticks, true)
			testutil.Equal(t, resumed, false)
		}
		testutil.Equal(t, ticks, NeedsInputResumeTicks-1)
		ticks, resumed = ResumeActivityTick(ticks, true)
		testutil.Equal(t, ticks, NeedsInputResumeTicks)
		testutil.Equal(t, resumed, true)
	})

	// Unlike EscalateParkedSelection, there is deliberately NO grace period on a
	// miss: the asymmetry is that clearing too slowly (the flag lingers a few
	// extra ticks after a genuine resume) is safe, while clearing a still-stuck
	// agent is not — so a single non-working tick resets the streak outright,
	// even one tick before it would have crossed the threshold.
	t.Run("a single miss resets the streak immediately, even one tick before threshold", func(t *testing.T) {
		ticks := NeedsInputResumeTicks - 1
		ticks, resumed := ResumeActivityTick(ticks, false)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, resumed, false)
	})

	t.Run("an already-resumed streak still resets to zero on a miss (no stickiness)", func(t *testing.T) {
		ticks := NeedsInputResumeTicks
		ticks, resumed := ResumeActivityTick(ticks, false)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, resumed, false)
	})

	t.Run("resuming after a break starts over from 1, not the prior streak", func(t *testing.T) {
		ticks, resumed := ResumeActivityTick(0, false)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, resumed, false)
		ticks, resumed = ResumeActivityTick(ticks, true)
		testutil.Equal(t, ticks, 1)
		testutil.Equal(t, resumed, false)
	})

	t.Run("sparse isolated working ticks amid misses never accumulate (anti-false-clear)", func(t *testing.T) {
		// A brief single-utterance acknowledgment from an agent that is still
		// genuinely blocked (e.g. "still waiting on X") shows only a FEW
		// working ticks before re-parking — must never cross the threshold.
		ticks := 0
		var resumed bool
		pattern := []bool{false, true, true, false, false, true, false, true, true, false}
		for _, w := range pattern {
			ticks, resumed = ResumeActivityTick(ticks, w)
			if resumed {
				t.Fatalf("resumed on a sparse/non-continuous working pattern: %v", pattern)
			}
		}
		if ticks > 2 {
			t.Fatalf("expected no meaningful accumulated credit from sparse working ticks, got %d", ticks)
		}
	})

	// narrow-needs-input-sustained-active: reproduces the contrib-classifier
	// ground-truth finding — a role demonstrably, substantially active for many
	// minutes (30k+ tokens streamed over 7+ minutes) whose PTY content
	// nonetheless periodically LOOKS like a parked selection prompt to the
	// content classifier (ux.log showed this exact task's rerender/revive logic
	// alternating "busy" / "blocked on user prompt" within seconds, repeatedly).
	// Unlike the sparse single-utterance-acknowledgment pattern above (clearly
	// still blocked), this models MOSTLY-working output with only an OCCASIONAL
	// single-tick miss — the kind of variance ordinary tool-call-to-tool-call
	// pacing produces from a genuinely active agent, not a re-parking one.
	t.Run("mostly-working output with occasional single-tick misses never converges either (zero grace period)", func(t *testing.T) {
		ticks := 0
		var resumed bool
		// 40 ticks, one miss every 4th tick (75% "working") — never 5 CONSECUTIVE
		// working ticks, so under the current zero-grace design this can run
		// indefinitely without ever crossing NeedsInputResumeTicks.
		everConverged := false
		for i := 0; i < 40; i++ {
			working := i%4 != 3
			ticks, resumed = ResumeActivityTick(ticks, working)
			if resumed {
				everConverged = true
			}
		}
		if everConverged {
			t.Fatalf("did not expect convergence under the current zero-grace ResumeActivityTick for a mostly-working (miss every 4th tick) pattern — ticks=%d", ticks)
		}
	})
}

// TestSustainedActivityTick pins the grace-tolerant sibling introduced by
// narrow-needs-input-sustained-active — used ONLY by the Hera rail's
// SustainedActive signal, never by ResumeActivityTick's existing callers.
func TestSustainedActivityTick(t *testing.T) {
	t.Run("a sustained working streak resumes exactly at the threshold, not before (matches ResumeActivityTick)", func(t *testing.T) {
		ticks := 0
		var sustained bool
		for i := 0; i < NeedsInputResumeTicks-1; i++ {
			ticks, sustained = SustainedActivityTick(ticks, true)
			testutil.Equal(t, sustained, false)
		}
		testutil.Equal(t, ticks, NeedsInputResumeTicks-1)
		ticks, sustained = SustainedActivityTick(ticks, true)
		testutil.Equal(t, ticks, NeedsInputResumeTicks)
		testutil.Equal(t, sustained, true)
	})

	t.Run("mostly-working output with a miss every 4th tick DOES converge (the fix for finding #3)", func(t *testing.T) {
		ticks := 0
		var sustained bool
		converged := false
		convergedAtTick := -1
		for i := 0; i < 40; i++ {
			working := i%4 != 3
			ticks, sustained = SustainedActivityTick(ticks, working)
			if sustained && !converged {
				converged = true
				convergedAtTick = i
			}
		}
		if !converged {
			t.Fatalf("expected SustainedActivityTick to converge on a mostly-working (miss every 4th tick) pattern via its one-tick grace, ticks=%d", ticks)
		}
		if convergedAtTick > 10 {
			t.Fatalf("expected prompt convergence (well under 40 ticks), converged at tick %d", convergedAtTick)
		}
	})

	t.Run("a single isolated miss is forgiven — the streak resumes rather than resetting", func(t *testing.T) {
		ticks := 0
		ticks, _ = SustainedActivityTick(ticks, true) // 1
		ticks, _ = SustainedActivityTick(ticks, true) // 2
		ticks, _ = SustainedActivityTick(ticks, true) // 3
		ticks, sustained := SustainedActivityTick(ticks, false)
		if sustained {
			t.Fatalf("did not expect sustained on the grace tick itself")
		}
		// The held streak (3) must resume, not reset to 1, on the very next
		// working tick.
		ticks, sustained = SustainedActivityTick(ticks, true)
		testutil.Equal(t, ticks, 4)
		testutil.Equal(t, sustained, false)
		ticks, sustained = SustainedActivityTick(ticks, true)
		testutil.Equal(t, ticks, 5)
		testutil.Equal(t, sustained, true)
	})

	t.Run("two consecutive misses is a genuine break, not grace", func(t *testing.T) {
		ticks := 0
		ticks, _ = SustainedActivityTick(ticks, true)  // 1
		ticks, _ = SustainedActivityTick(ticks, true)  // 2
		ticks, _ = SustainedActivityTick(ticks, true)  // 3
		ticks, _ = SustainedActivityTick(ticks, false) // grace (held at -3)
		ticks, sustained := SustainedActivityTick(ticks, false)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, sustained, false)
		// Must start over from 1, not resume the discarded streak.
		ticks, sustained = SustainedActivityTick(ticks, true)
		testutil.Equal(t, ticks, 1)
		testutil.Equal(t, sustained, false)
	})

	t.Run("a sparse still-genuinely-blocked pattern still never converges despite the grace tolerance", func(t *testing.T) {
		// Same anti-false-clear pattern as ResumeActivityTick's sparse test above
		// (a brief single-utterance acknowledgment, still genuinely blocked) — the
		// one-tick grace must not be generous enough to let this converge.
		ticks := 0
		var sustained bool
		pattern := []bool{false, true, true, false, false, true, false, true, true, false}
		for _, w := range pattern {
			ticks, sustained = SustainedActivityTick(ticks, w)
			if sustained {
				t.Fatalf("sustained on a sparse/non-continuous working pattern despite grace: %v", pattern)
			}
		}
	})
}

// TestSettleTick pins the pure step function backing NeedsInputClear's
// settledOf clear path (BUG-072) — the complementary case to
// ResumeActivityTick: a session that resolves its own block and settles into
// idle FASTER than ResumeActivityTick's sustained-working threshold can never
// satisfy that path (going idle drives workingNow false, and an idle session
// never shows the working affordance again), so SettleTick recognizes the
// session going genuinely idle with no current blocking signal instead.
func TestSettleTick(t *testing.T) {
	t.Run("not idle stays at zero regardless of the signal", func(t *testing.T) {
		ticks, settled := SettleTick(0, false, false)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, settled, false)
	})

	t.Run("idle but the signal is still present stays at zero", func(t *testing.T) {
		ticks, settled := SettleTick(0, true, true)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, settled, false)
	})

	t.Run("idle with no signal settles exactly at the threshold, not before", func(t *testing.T) {
		ticks := 0
		settled := false
		for i := 0; i < NeedsInputSettleTicks-1; i++ {
			ticks, settled = SettleTick(ticks, true, false)
			testutil.Equal(t, settled, false)
		}
		testutil.Equal(t, ticks, NeedsInputSettleTicks-1)
		ticks, settled = SettleTick(ticks, true, false)
		testutil.Equal(t, ticks, NeedsInputSettleTicks)
		testutil.Equal(t, settled, true)
	})

	// No grace period on a miss, mirroring ResumeActivityTick: under-clearing
	// (staying flagged a tick or two longer) is safe, a false clear is not.
	t.Run("a tick that is not idle resets the streak immediately, even one tick before threshold", func(t *testing.T) {
		ticks := NeedsInputSettleTicks - 1
		ticks, settled := SettleTick(ticks, false, false)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, settled, false)
	})

	t.Run("a tick where the signal reappears resets the streak immediately, even one tick before threshold", func(t *testing.T) {
		ticks := NeedsInputSettleTicks - 1
		ticks, settled := SettleTick(ticks, true, true)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, settled, false)
	})

	t.Run("an already-settled streak still resets to zero on a miss (no stickiness)", func(t *testing.T) {
		ticks := NeedsInputSettleTicks
		ticks, settled := SettleTick(ticks, false, false)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, settled, false)
	})

	t.Run("resuming after a break starts over from 1, not the prior streak", func(t *testing.T) {
		ticks, settled := SettleTick(0, false, false)
		testutil.Equal(t, ticks, 0)
		testutil.Equal(t, settled, false)
		ticks, settled = SettleTick(ticks, true, false)
		testutil.Equal(t, ticks, 1)
		testutil.Equal(t, settled, false)
	})

	// A still-genuinely-blocked idle session (idle, but the tail STILL shows
	// the signal that raised the flag) never accumulates credit — this is
	// indistinguishable, by design, from the ordinary still-blocked case and
	// must never settle.
	t.Run("a still-blocked idle session never accumulates credit (anti-false-clear)", func(t *testing.T) {
		ticks := 0
		var settled bool
		for i := 0; i < 5; i++ {
			ticks, settled = SettleTick(ticks, true, true)
			if settled {
				t.Fatalf("settled while the signal was still present on tick %d", i+1)
			}
		}
		testutil.Equal(t, ticks, 0)
	})
}

// TestClearBlockedRoleStatus pins the pure decision function backing the
// hera_status "blocked" auto-clear (root-cause-and-fix-a-live): unlike
// NeedsInputClear (which governs the SEPARATE, auto-detected PTY needs-input
// flag), hera_status is a self-asserted signal set only via an explicit
// hera_status tool call or a manual rail s/S step — so this reproduces the
// EXACT live repro: a worker marks itself blocked, the coordinator/human
// replies DIRECTLY in the pane (a real keystroke, not a coordinator-relayed
// system message), and the agent's own follow-up reply is brief. The direct-
// reply condition must fire immediately regardless of how long the agent's
// own response takes — it must not need the resumed-activity threshold at all.
func TestClearBlockedRoleStatus(t *testing.T) {
	blockedAt := time.Unix(1000, 0)

	t.Run("no clear condition met stays blocked", func(t *testing.T) {
		testutil.Equal(t, ClearBlockedRoleStatus(blockedAt, time.Time{}, false), false)
	})

	t.Run("no clear condition met with input BEFORE the block stays blocked", func(t *testing.T) {
		before := blockedAt.Add(-time.Second)
		testutil.Equal(t, ClearBlockedRoleStatus(blockedAt, before, false), false)
	})

	t.Run("direct reply after the block clears immediately, even with no resumed activity", func(t *testing.T) {
		// This is the exact live repro: the user answers directly in the pane
		// ("all good.") and the agent's own reply is brief — it must not need
		// to sustain agent.NeedsInputResumeTicks of "working" affordance.
		after := blockedAt.Add(time.Second)
		testutil.Equal(t, ClearBlockedRoleStatus(blockedAt, after, false), true)
	})

	t.Run("input exactly at the block timestamp does not clear (strictly after required)", func(t *testing.T) {
		testutil.Equal(t, ClearBlockedRoleStatus(blockedAt, blockedAt, false), false)
	})

	t.Run("sustained resumed activity clears even with no direct user input", func(t *testing.T) {
		// Mirrors BUG-065: a coordinator-relayed answer (WriteInputSystem) never
		// advances LastUserInput, so resumed activity is the only signal.
		testutil.Equal(t, ClearBlockedRoleStatus(blockedAt, time.Time{}, true), true)
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

// TestNeedsInputClear covers BUG-034 (the deterministic clear of the
// needs-input flag on user input or archive, and its persistence otherwise)
// and BUG-063 (a stale re-candidacy at the same input timestamp, after a gap
// in candidacy, must not recapture a stuck baseline while the task's session
// is still running).
func TestNeedsInputClear(t *testing.T) {
	t0 := time.Unix(1000, 0)
	t1 := time.Unix(2000, 0) // strictly after t0
	t2 := time.Unix(3000, 0) // strictly after t1

	t.Run("nil deps pass candidates through unchanged", func(t *testing.T) {
		out, base, _ := NeedsInputClear([]string{"a", "b"}, []string{"a", "b"}, nil, nil, nil, nil, nil, nil, nil)
		testutil.DeepEqual(t, out, []string{"a", "b"})
		// Baselines are captured (zero) so subsequent ticks have state.
		testutil.Equal(t, len(base), 2)
	})

	t.Run("persists across ticks with no input (no decay)", func(t *testing.T) {
		lastInput := func(string) time.Time { return t0 } // input predates the flag
		var base map[string]time.Time
		var cleared map[string]ClearedMarker
		var out []string
		for i := 0; i < 5; i++ {
			out, base, cleared = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, nil, nil)
			testutil.DeepEqual(t, out, []string{"a"})
		}
		// Baseline frozen at the first-seen input time.
		testutil.Equal(t, base["a"].Equal(t0), true)
	})

	t.Run("clears on input delivered after the flag, stale tail still matching", func(t *testing.T) {
		// Tick 1: flagged; baseline captured at t0 (no input since the question).
		input := t0
		lastInput := func(string) time.Time { return input }
		out, base, cleared := NeedsInputClear([]string{"a"}, []string{"a"}, nil, nil, lastInput, nil, nil, nil, nil)
		testutil.DeepEqual(t, out, []string{"a"})

		// Tick 2: user responds (lastInput advances past the baseline). The
		// candidate is STILL passed in (the "?" is still in the tail), but it
		// must be cleared anyway — that is the crux.
		input = t1
		out, base, cleared = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, nil, nil)
		testutil.Equal(t, len(out), 0)

		// Tick 3: still a candidate (stale tail), no new input → stays cleared.
		out, _, _ = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, nil, nil)
		testutil.Equal(t, len(out), 0)
	})

	t.Run("input to a different session does not clear this one", func(t *testing.T) {
		// "a" never receives input (t0, predates flag); "b" receives input (t1).
		lastInput := func(id string) time.Time {
			if id == "b" {
				return t1
			}
			return t0
		}
		// Prime baselines for both at t0 by seeding the prev map.
		prev := map[string]time.Time{"a": t0, "b": t0}
		out, _, _ := NeedsInputClear([]string{"a", "b"}, []string{"a", "b"}, prev, nil, lastInput, nil, nil, nil, nil)
		testutil.DeepEqual(t, out, []string{"a"}) // a kept, b cleared
	})

	t.Run("archive clears regardless of signal and drops the baseline and cleared marker", func(t *testing.T) {
		archived := func(id string) bool { return id == "a" }
		prev := map[string]time.Time{"a": t0, "b": t0}
		// Only "a" (the archived task) carries a pre-existing cleared marker;
		// "b" has none, so it is unaffected and stays flagged via its baseline.
		prevCleared := map[string]ClearedMarker{"a": {At: t0}}
		out, base, cleared := NeedsInputClear([]string{"a", "b"}, []string{"a", "b"}, prev, prevCleared, nil, archived, nil, nil, nil)
		testutil.DeepEqual(t, out, []string{"b"})
		if _, ok := base["a"]; ok {
			t.Error("archived task baseline should be dropped")
		}
		if _, ok := cleared["a"]; ok {
			t.Error("archived task cleared marker should be dropped")
		}
	})

	// The task genuinely LEAVES the running set between the clear and the next
	// candidacy (e.g. the session stopped and restarted) — the cleared marker
	// is scoped to `running`, so it does not survive, and the fresh candidacy
	// re-arms exactly like a task's first-ever candidacy.
	t.Run("re-arms after the task leaves the running set then a fresh question arrives", func(t *testing.T) {
		input := t0
		lastInput := func(string) time.Time { return input }

		// Flagged, then user responds at t1 → cleared.
		_, base, cleared := NeedsInputClear([]string{"a"}, []string{"a"}, nil, nil, lastInput, nil, nil, nil, nil)
		input = t1
		out, base, cleared := NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, nil, nil)
		testutil.Equal(t, len(out), 0)

		// "a" leaves the running set entirely (not merely a candidacy gap) —
		// both the baseline and the cleared marker are dropped.
		_, base, cleared = NeedsInputClear(nil, nil, base, cleared, lastInput, nil, nil, nil, nil)
		if _, ok := base["a"]; ok {
			t.Error("baseline should drop when the task leaves the running set")
		}
		if _, ok := cleared["a"]; ok {
			t.Error("cleared marker should drop when the task leaves the running set")
		}

		// "a" comes back (still running) with a fresh candidacy, no further
		// input since t1 → re-arms like a brand-new candidacy.
		out, _, _ = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, nil, nil)
		testutil.DeepEqual(t, out, []string{"a"})
	})

	// BUG-063: the task's session STAYS running throughout — a gap tick with
	// no candidacy does not mean the task is gone, just that no detection pass
	// happened to flag it that tick. A later stale content-heuristic re-flag
	// (fingerprint match / escalation grace tick, simulated here as a plain
	// candidate re-appearance) at the SAME lastUserInput timestamp must not
	// recapture a baseline that can never clear again.
	t.Run("BUG-063: a stale re-candidacy at the same timestamp does not re-stick while the session keeps running", func(t *testing.T) {
		input := t0
		lastInput := func(string) time.Time { return input }
		running := []string{"a"} // "a" is running for the whole scenario

		// Tick 1: flagged.
		out, base, cleared := NeedsInputClear([]string{"a"}, running, nil, nil, lastInput, nil, nil, nil, nil)
		testutil.DeepEqual(t, out, []string{"a"})

		// Tick 2: user answers (lastInput advances past baseline) → real clear.
		input = t1
		out, base, cleared = NeedsInputClear([]string{"a"}, running, base, cleared, lastInput, nil, nil, nil, nil)
		testutil.Equal(t, len(out), 0)

		// Tick 3: gap — no detection pass flags "a" this tick, but its session
		// is still running.
		out, base, cleared = NeedsInputClear(nil, running, base, cleared, lastInput, nil, nil, nil, nil)
		testutil.Equal(t, len(out), 0)

		// Tick 4: a stale content-heuristic re-flag presents "a" as a candidate
		// again. Nothing new has happened — lastUserInput is still t1. This is
		// the exact race: the old implementation had forgotten "a"'s baseline
		// at tick 3, so it would recapture baseline == lastInput(id) here and
		// get stuck forever. Must NOT re-stick.
		out, base, cleared = NeedsInputClear([]string{"a"}, running, base, cleared, lastInput, nil, nil, nil, nil)
		testutil.Equal(t, len(out), 0)

		// And it stays cleared across further ticks too — not just a one-tick
		// reprieve — even with repeated stale re-candidacy.
		for i := 0; i < 3; i++ {
			out, base, cleared = NeedsInputClear([]string{"a"}, running, base, cleared, lastInput, nil, nil, nil, nil)
			testutil.Equal(t, len(out), 0)
		}

		// A genuinely NEWER input finally arrives → re-arms normally.
		input = t2
		out, _, _ = NeedsInputClear([]string{"a"}, running, base, cleared, lastInput, nil, nil, nil, nil)
		testutil.DeepEqual(t, out, []string{"a"})
	})

	// Resumed-activity clear (see ResumeActivityTick): a hera coordinator's
	// relayed answer is delivered via WriteInputSystem, which never advances
	// LastUserInput, so lastInputOf never progresses past baseline for this
	// scenario — resumedOf is the ONLY thing that clears it.
	t.Run("resumed activity clears despite no user input and a stale tail", func(t *testing.T) {
		lastInput := func(string) time.Time { return t0 } // never advances past baseline
		resumed := func(id string) bool { return id == "a" }
		out, base, cleared := NeedsInputClear([]string{"a", "b"}, []string{"a", "b"}, nil, nil, lastInput, nil, resumed, nil, nil)
		testutil.DeepEqual(t, out, []string{"b"}) // "a" cleared via resumedOf, "b" stays flagged
		if _, ok := base["a"]; ok {
			t.Error("resumed task should not carry a baseline forward")
		}
		if _, ok := cleared["a"]; !ok {
			t.Error("resumed task should record a cleared marker")
		}
	})

	// The resumed clear reuses the SAME cleared-marker machinery as the
	// input-based clear, so it inherits the BUG-063 stale-recandidacy guard for
	// free: once resumedOf's own signal later goes quiet again (e.g. the
	// caller's consecutive-tick streak resets), a stale re-candidacy at the
	// same lastInputOf timestamp must not re-stick the flag.
	t.Run("resumed clear is protected by the BUG-063 stale-recandidacy guard", func(t *testing.T) {
		lastInput := func(string) time.Time { return t0 }
		running := []string{"a"}
		resumedNow := true
		resumed := func(string) bool { return resumedNow }

		out, base, cleared := NeedsInputClear([]string{"a"}, running, nil, nil, lastInput, nil, resumed, nil, nil)
		testutil.Equal(t, len(out), 0)

		resumedNow = false
		for i := 0; i < 3; i++ {
			out, base, cleared = NeedsInputClear([]string{"a"}, running, base, cleared, lastInput, nil, resumed, nil, nil)
			testutil.Equal(t, len(out), 0)
		}
	})

	// Regression guard for BUG-034: a resumedOf that never reports true (no
	// sustained activity observed — e.g. an unrelated system nudge to a
	// genuinely still-parked agent) must never clear the flag on its own.
	t.Run("resumedOf false never clears a still-parked agent", func(t *testing.T) {
		lastInput := func(string) time.Time { return t0 } // predates the flag, never advances
		resumed := func(string) bool { return false }
		var base map[string]time.Time
		var cleared map[string]ClearedMarker
		var out []string
		for i := 0; i < 5; i++ {
			out, base, cleared = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, resumed, nil, nil)
			testutil.DeepEqual(t, out, []string{"a"})
		}
	})

	// Settled clear (see SettleTick, BUG-072): a session that resolves its own
	// block and settles into idle FASTER than resumedOf's sustained-working
	// threshold can never satisfy resumedOf (going idle drives workingNow
	// false, resetting that streak, and an idle session never shows the
	// working affordance again) — settledOf is the only thing that clears it.
	t.Run("settled activity clears despite no user input and no sustained resumed activity", func(t *testing.T) {
		lastInput := func(string) time.Time { return t0 } // never advances past baseline
		resumed := func(string) bool { return false }     // never sustains a working streak
		settled := func(id string) bool { return id == "a" }
		out, base, cleared := NeedsInputClear([]string{"a", "b"}, []string{"a", "b"}, nil, nil, lastInput, nil, resumed, settled, nil)
		testutil.DeepEqual(t, out, []string{"b"}) // "a" cleared via settledOf, "b" stays flagged
		if _, ok := base["a"]; ok {
			t.Error("settled task should not carry a baseline forward")
		}
		if _, ok := cleared["a"]; !ok {
			t.Error("settled task should record a cleared marker")
		}
	})

	// The settled clear reuses the SAME cleared-marker machinery as the other
	// clear paths, so it inherits the BUG-063 stale-recandidacy guard for free:
	// once settledOf's own signal later goes quiet again (e.g. the session
	// shows the blocking signal once more), a stale re-candidacy at the same
	// lastInputOf timestamp must not re-stick the flag.
	t.Run("settled clear is protected by the BUG-063 stale-recandidacy guard", func(t *testing.T) {
		lastInput := func(string) time.Time { return t0 }
		running := []string{"a"}
		settledNow := true
		settled := func(string) bool { return settledNow }

		out, base, cleared := NeedsInputClear([]string{"a"}, running, nil, nil, lastInput, nil, nil, settled, nil)
		testutil.Equal(t, len(out), 0)

		settledNow = false
		for i := 0; i < 3; i++ {
			out, base, cleared = NeedsInputClear([]string{"a"}, running, base, cleared, lastInput, nil, nil, settled, nil)
			testutil.Equal(t, len(out), 0)
		}
	})

	// Regression guard for BUG-072: a settledOf that never reports true (the
	// session never demonstrates settlement — e.g. it is still genuinely
	// blocked) must never clear the flag on its own.
	t.Run("settledOf false never clears a still-parked agent", func(t *testing.T) {
		lastInput := func(string) time.Time { return t0 } // predates the flag, never advances
		settled := func(string) bool { return false }
		var base map[string]time.Time
		var cleared map[string]ClearedMarker
		var out []string
		for i := 0; i < 5; i++ {
			out, base, cleared = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, settled, nil)
			testutil.DeepEqual(t, out, []string{"a"})
		}
	})

	// BUG-067: Claude's AskUserQuestion / /brainstorm flow routinely asks
	// several DISTINCT questions in sequence, each answered directly in the
	// pane. Answering question 1 clears the flag (a real, correct clear) and
	// records a cleared marker at that lastInputOf timestamp. Question 2 then
	// arrives — a genuinely different, still-unanswered prompt — before the
	// user types anything else, so lastInputOf(id) is UNCHANGED from the
	// marker. A fingerprint that provably differs from what was cleared must
	// let it re-arm instead of being silently swallowed by the BUG-063
	// stale-recandidacy guard forever.
	t.Run("BUG-067: a distinct later prompt at the same cleared timestamp re-arms when its fingerprint differs", func(t *testing.T) {
		input := t0
		lastInput := func(string) time.Time { return input }
		const fpQ1, fpQ2 uint64 = 111, 222
		fp := fpQ1
		fingerprintOf := func(string) (uint64, bool) { return fp, true }

		// Tick 1: Q1 shown, flagged.
		out, base, cleared := NeedsInputClear([]string{"a"}, []string{"a"}, nil, nil, lastInput, nil, nil, nil, fingerprintOf)
		testutil.DeepEqual(t, out, []string{"a"})

		// Tick 2: user answers Q1 directly -> lastInput advances past baseline
		// -> real clear. The marker records Q1's fingerprint.
		input = t1
		out, base, cleared = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, nil, fingerprintOf)
		testutil.Equal(t, len(out), 0)
		marker, ok := cleared["a"]
		testutil.Equal(t, ok, true)
		testutil.Equal(t, marker.HasFP, true)
		testutil.Equal(t, marker.FP, fpQ1)

		// Tick 3: Q2 -- a distinct, unanswered prompt -- appears. No further
		// input has arrived (still t1, matching the cleared marker's
		// timestamp exactly), but the fingerprint has changed.
		fp = fpQ2
		out, base, cleared = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, nil, fingerprintOf)
		testutil.DeepEqual(t, out, []string{"a"})

		// It stays flagged on subsequent ticks too (a fresh baseline was
		// captured, matching a first-ever candidacy).
		out, _, _ = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, nil, fingerprintOf)
		testutil.DeepEqual(t, out, []string{"a"})
	})

	// Companion guard: when the fingerprint is UNAVAILABLE or UNCHANGED, the
	// original BUG-063 behavior (suppress the stale re-candidacy) must still
	// hold — BUG-067 only widens what gets surfaced, never what gets
	// suppressed, when content can't be told apart.
	t.Run("BUG-067 companion: identical or unknown fingerprint still suppresses the stale re-candidacy", func(t *testing.T) {
		input := t0
		lastInput := func(string) time.Time { return input }

		t.Run("identical fingerprint stays suppressed", func(t *testing.T) {
			const fp uint64 = 42
			fingerprintOf := func(string) (uint64, bool) { return fp, true }
			input = t0
			out, base, cleared := NeedsInputClear([]string{"a"}, []string{"a"}, nil, nil, lastInput, nil, nil, nil, fingerprintOf)
			testutil.DeepEqual(t, out, []string{"a"})
			input = t1
			out, base, cleared = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, nil, fingerprintOf)
			testutil.Equal(t, len(out), 0)
			// A later stale re-candidacy with the SAME fingerprint (BUG-063's
			// classic case: a rendering catch-up artifact) must stay cleared.
			out, _, _ = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, nil, fingerprintOf)
			testutil.Equal(t, len(out), 0)
		})

		t.Run("nil fingerprintOf degrades to pre-BUG-067 timestamp-only behavior", func(t *testing.T) {
			input = t0
			out, base, cleared := NeedsInputClear([]string{"a"}, []string{"a"}, nil, nil, lastInput, nil, nil, nil, nil)
			testutil.DeepEqual(t, out, []string{"a"})
			input = t1
			out, base, cleared = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, nil, nil)
			testutil.Equal(t, len(out), 0)
			out, _, _ = NeedsInputClear([]string{"a"}, []string{"a"}, base, cleared, lastInput, nil, nil, nil, nil)
			testutil.Equal(t, len(out), 0)
		})
	})
}

// TestContentIdleFingerprint pins the content-aware idle building block (BUG-036):
// the emulated-screen fingerprint is stable across animation-only frames, and the
// "working" affordance is reported from the rendered screen.
func TestContentIdleFingerprint(t *testing.T) {
	t.Run("parked alt-screen frames fingerprint identically and are not working", func(t *testing.T) {
		r := &ScreenRenderer{}
		fpA, workA := ContentIdleFingerprint(r, []byte(altScreenPromptFrame("3s", "✻")), 80, 24)
		fpB, workB := ContentIdleFingerprint(r, []byte(altScreenPromptFrame("9s", "✶")), 80, 24)
		testutil.Equal(t, workA, false)
		testutil.Equal(t, workB, false)
		testutil.Equal(t, fpA, fpB) // only spinner chrome differs → stripped → same fp
	})

	t.Run("working alt-screen frame reports working", func(t *testing.T) {
		r := &ScreenRenderer{}
		_, working := ContentIdleFingerprint(r, []byte(altScreenQuestionFrame("8s", "✻", true)), 80, 24)
		testutil.Equal(t, working, true)
	})

	t.Run("nil renderer falls back to raw fingerprint + raw working detection", func(t *testing.T) {
		raw := []byte("⏺ Working on it\r✻ Cogitating… (8s · esc to interrupt)\r")
		fp, working := ContentIdleFingerprint(nil, raw, 80, 24)
		testutil.Equal(t, fp, ContentFingerprint(raw))
		testutil.Equal(t, working, true)
	})
}

// TestScreenRenderer_RepeatedIdenticalRenderIsIdempotent verifies, empirically
// rather than by assumption, the reuse-safety property the tui package's
// per-tick tail/fingerprint caching (dedupe-redundant-needsinput-reads) depends
// on: ScreenRenderer.render RIS-resets the emulator before every render after
// the first (see render's doc comment), so two calls with byte-identical
// (tail, cols, rows) — even with OTHER, DIFFERENT renders on the SAME shared
// renderer interleaved between them, exactly like one detectNeedsInputSticky
// tick's multiple passes over multiple sessions — must return byte-identical
// results. If this were false (cumulative emulator state bleeding across
// calls), caching a pass's ContentIdleFingerprint result for reuse in a LATER
// pass would be unsound and would need per-(id,tail-hash) memoization of the
// return value instead of relying on "call it again gets the same answer".
func TestScreenRenderer_RepeatedIdenticalRenderIsIdempotent(t *testing.T) {
	t.Run("ContentIdleFingerprint: identical args interleaved with other renders stay identical", func(t *testing.T) {
		r := &ScreenRenderer{}
		tailA := []byte(altScreenPromptFrame("3s", "✻"))
		tailB := []byte(altScreenQuestionFrame("5s", "✶", true))

		fp1, work1 := ContentIdleFingerprint(r, tailA, 80, 24)
		// Interleave unrelated renders on the SAME renderer — different
		// content, different dimensions — mirroring how detectNeedsInputSticky
		// shares one a.needsInputScreen across every session's passes in a
		// single tick.
		ContentIdleFingerprint(r, tailB, 100, 30)
		ContentIdleFingerprint(r, []byte("plain linear output, no prompt\n"), 80, 24)
		fp2, work2 := ContentIdleFingerprint(r, tailA, 80, 24)

		testutil.Equal(t, fp1, fp2)
		testutil.Equal(t, work1, work2)
	})

	t.Run("render (via DetectNeedsInputScreen) is idempotent across a busy interleaving session", func(t *testing.T) {
		r := &ScreenRenderer{}
		altScreen := []byte(altScreenPromptFrame("3s", "✻"))

		got1 := DetectNeedsInputScreen(r, altScreen, 80, 24)
		for i := 0; i < 5; i++ {
			// Simulate other sessions' tail reads landing on the shared
			// renderer in between, each at varying sizes.
			DetectNeedsInputScreen(r, []byte("\x1b[?1049h\x1b[2J\x1b[2;5HBusy..."), 120, 40)
		}
		got2 := DetectNeedsInputScreen(r, altScreen, 80, 24)

		testutil.Equal(t, got1, true)
		testutil.Equal(t, got2, true)
	})

	t.Run("resumed-activity and sustained-active passes read the SAME (fp, working) for identical args", func(t *testing.T) {
		// Mirrors the exact call shape detectNeedsInputSticky's resumed-activity
		// pass and sustained-active pass make for the SAME session in one tick:
		// both call ContentIdleFingerprint(r, tail, cols, rows) with byte-
		// identical arguments, with the content-stability pass's own renders
		// for OTHER sessions having already touched the shared renderer first.
		r := &ScreenRenderer{}
		tail := []byte(altScreenQuestionFrame("4s", "✻", true))

		// Simulate the content-stability pass rendering a handful of other
		// sessions first (the loop order in detectNeedsInputSticky).
		ContentIdleFingerprint(r, []byte(altScreenPromptFrame("1s", "✻")), 80, 24)
		ContentIdleFingerprint(r, []byte("linear, no prompt\n"), 80, 24)

		_, workingResumePass := ContentIdleFingerprint(r, tail, 80, 24)
		_, workingSustainedPass := ContentIdleFingerprint(r, tail, 80, 24)
		testutil.Equal(t, workingResumePass, workingSustainedPass)
		testutil.Equal(t, workingResumePass, true)
	})
}

// TestContentIdle drives the content-aware idle classification end to end through
// the real ScreenRenderer + fingerprint path, both directions (BUG-036).
func TestContentIdle(t *testing.T) {
	t0 := time.Unix(1000, 0)
	size := func(string) (int, int) { return 80, 24 }
	// A streaming alt-screen frame whose visible transcript changes per tick.
	streamFrame := func(n string) []byte {
		return []byte("\x1b[?1049h\x1b[2J\x1b[2;5HReading internal/file_" + n + ".go")
	}

	t.Run("parked fullscreen agent becomes content-idle after the threshold", func(t *testing.T) {
		r := &ScreenRenderer{}
		// Tail is the parked prompt frame; only spinner chrome varies tick-to-tick.
		var frame []byte
		tailOf := func(string) []byte { return frame }

		// Tick 1: first observation → since=t0 → 0 < threshold → not idle yet.
		frame = []byte(altScreenPromptFrame("3s", "✻"))
		idle, st := ContentIdle([]string{"w"}, nil, tailOf, size, r, nil, t0)
		testutil.Equal(t, len(idle), 0)

		// Tick 2: same rendered screen (new spinner chrome), idleThreshold later.
		frame = []byte(altScreenPromptFrame("8s", "✶"))
		idle, _ = ContentIdle([]string{"w"}, nil, tailOf, size, r, st, t0.Add(idleThreshold))
		testutil.DeepEqual(t, idle, []string{"w"})
	})

	t.Run("working fullscreen agent is never content-idle", func(t *testing.T) {
		r := &ScreenRenderer{}
		// The interrupt affordance is present every tick → working → skipped.
		tailOf := func(string) []byte { return []byte(altScreenQuestionFrame("8s", "✻", true)) }
		var st *ContentIdleState
		for i := 0; i < 4; i++ {
			var idle []string
			idle, st = ContentIdle([]string{"w"}, nil, tailOf, size, r, st, t0.Add(time.Duration(i)*idleThreshold))
			testutil.Equal(t, len(idle), 0)
		}
	})

	t.Run("streaming fullscreen agent is never content-idle (timer resets)", func(t *testing.T) {
		r := &ScreenRenderer{}
		n := "0"
		tailOf := func(string) []byte { return streamFrame(n) }
		var st *ContentIdleState
		for i := 0; i < 4; i++ {
			n = string(rune('0' + i)) // content changes every tick
			var idle []string
			idle, st = ContentIdle([]string{"w"}, nil, tailOf, size, r, st, t0.Add(time.Duration(i)*idleThreshold))
			testutil.Equal(t, len(idle), 0)
		}
	})

	t.Run("raw-idle sessions are skipped (already idle)", func(t *testing.T) {
		r := &ScreenRenderer{}
		tailOf := func(string) []byte { return []byte(altScreenPromptFrame("3s", "✻")) }
		rawIdle := map[string]bool{"w": true}
		// Even after threshold, a raw-idle session is not returned by the augmentation.
		_, st := ContentIdle([]string{"w"}, rawIdle, tailOf, size, r, nil, t0)
		idle, _ := ContentIdle([]string{"w"}, rawIdle, tailOf, size, r, st, t0.Add(idleThreshold))
		testutil.Equal(t, len(idle), 0)
	})

	t.Run("empty tail is skipped", func(t *testing.T) {
		r := &ScreenRenderer{}
		tailOf := func(string) []byte { return nil }
		idle, _ := ContentIdle([]string{"w"}, nil, tailOf, size, r, nil, t0)
		testutil.Equal(t, len(idle), 0)
	})

	// TestContentIdle/escalation pins BUG-029: a parked selection prompt whose
	// surrounding tail also carries unrelated per-tick-varying content (a status
	// counter, here) never lets the full-screen fingerprint converge — so the
	// ordinary stability timer restarts every tick (`since` never carries
	// forward) and content-idle would otherwise NEVER fire, leaving the rail
	// spinner running forever. The escalation counter is independent of the
	// fingerprint and must still fire after NeedsInputEscalationTicks.
	t.Run("never-converging fingerprint escalates to content-idle after N ticks", func(t *testing.T) {
		r := &ScreenRenderer{}
		n := 0
		tailOf := func(string) []byte {
			return []byte(altScreenPromptFrame("3s", "✻") + "\x1b[10;1Hnoise " + string(rune('a'+n))) //nolint:gosec // G115: n is bounded to [0,NeedsInputEscalationTicks) in this test, no overflow risk
		}
		var st *ContentIdleState
		for i := 0; i < NeedsInputEscalationTicks-1; i++ {
			n = i
			var idle []string
			// Advance by only 1s per tick (well under idleThreshold=3s) so the
			// ordinary wall-clock stability path cannot be what fires idle.
			idle, st = ContentIdle([]string{"w"}, nil, tailOf, size, r, st, t0.Add(time.Duration(i)*time.Second))
			testutil.Equal(t, len(idle), 0)
		}
		n = NeedsInputEscalationTicks - 1
		idle, _ := ContentIdle([]string{"w"}, nil, tailOf, size, r, st, t0.Add(time.Duration(NeedsInputEscalationTicks-1)*time.Second))
		testutil.DeepEqual(t, idle, []string{"w"})
	})

	t.Run("escalation counter resets when the working affordance appears alongside the same selection shape", func(t *testing.T) {
		r := &ScreenRenderer{}
		n := 0
		tailOf := func(string) []byte {
			return []byte(altScreenPromptFrame("3s", "✻") + "\x1b[10;1Hnoise " + string(rune('a'+n))) //nolint:gosec // G115: n is bounded to [0,NeedsInputEscalationTicks) in this test, no overflow risk
		}
		// Same selection-prompt shape as tailOf, but with the "esc to interrupt"
		// working affordance ALSO present — must suppress qualification even
		// though the selection cursor is still on screen.
		workingTail := func(string) []byte {
			return []byte("\x1b[?1049h\x1b[2J" +
				"\x1b[1;1H✻ Cogitating… (4s · esc to interrupt)" +
				"\x1b[3;5HDo you want to make this edit?" +
				"\x1b[5;5H1. Yes\x1b[6;5H2. No" +
				"\x1b[5;3H❯" +
				"\x1b[8;1H\x1b[?25l")
		}
		var st *ContentIdleState
		half := NeedsInputEscalationTicks / 2
		for i := 0; i < half; i++ {
			n = i
			var idle []string
			idle, st = ContentIdle([]string{"w"}, nil, tailOf, size, r, st, t0.Add(time.Duration(i)*time.Second))
			testutil.Equal(t, len(idle), 0)
		}
		// Working affordance appears for one tick — must reset the counter.
		idle, st2 := ContentIdle([]string{"w"}, nil, workingTail, size, r, st, t0.Add(time.Duration(half)*time.Second))
		testutil.Equal(t, len(idle), 0)
		// Resume the parked prompt: must NOT immediately escalate since the
		// streak restarted rather than resumed from half.
		n = half + 1
		idle, _ = ContentIdle([]string{"w"}, nil, tailOf, size, r, st2, t0.Add(time.Duration(half+1)*time.Second))
		testutil.Equal(t, len(idle), 0)
	})
}

// blinkCycle reproduces the exact ~65-byte redraw Claude Code emits for its
// blinking cursor/status-glyph animation, observed live in a BUG-061 repro:
// a cursor reposition + 24-bit color-code + one glyph, toggling between a
// space and "⏺" at a fixed screen position. It never stops, even while
// genuinely parked at a permission prompt.
func blinkCycle(glyph string) string {
	return "\x1b[?2026l\x1b[?2026h\x1b[H\r\x1b[29B\x1b[38;2;153;153;153m" + glyph + "\x1b[39m\x1b[50;1H\x1b[43;2H"
}

// TestDegenerateSuffixStart pins BUG-061's root cause: a fixed-size tail
// window can be entirely consumed by Claude's blinking-cursor redraw, pushing
// real content (here, the permission prompt) out of any FLAT last-N-bytes
// scan permanently — not intermittently, unlike the isolated-miss case
// BUG-029/060 targeted.
func TestDegenerateSuffixStart(t *testing.T) {
	real := "Do you want to proceed?\n❯ 1. Yes\n  2. No\n"

	t.Run("finds the boundary behind many blink cycles", func(t *testing.T) {
		var blink strings.Builder
		for i := 0; i < 200; i++ {
			if i%2 == 0 {
				blink.WriteString(blinkCycle(" "))
			} else {
				blink.WriteString(blinkCycle("⏺"))
			}
		}
		buf := []byte(real + blink.String())
		end := degenerateSuffixStart(buf)
		testutil.Equal(t, end, len(real))
		testutil.Equal(t, string(TrimToSubstantiveTail(buf)), real)
	})

	t.Run("a short coincidental repeat below the minimum does not trigger", func(t *testing.T) {
		buf := []byte(real + strings.Repeat("ab", blinkMinRepeats-1))
		testutil.Equal(t, degenerateSuffixStart(buf), -1)
		testutil.Equal(t, string(TrimToSubstantiveTail(buf)), real+strings.Repeat("ab", blinkMinRepeats-1))
	})

	t.Run("ordinary streaming content is never trimmed", func(t *testing.T) {
		buf := []byte("Reading internal/foo.go\nEditing internal/bar.go\nRunning go test ./...\n")
		testutil.Equal(t, degenerateSuffixStart(buf), -1)
	})

	t.Run("entirely-blink buffer (real content not yet within reach) returns 0", func(t *testing.T) {
		var blink strings.Builder
		for i := 0; i < 50; i++ {
			blink.WriteString(blinkCycle("⏺"))
		}
		buf := []byte(blink.String())
		testutil.Equal(t, degenerateSuffixStart(buf), 0)
	})
}

// TestSubstantiveTail pins the expand-on-degenerate-tail behavior: a caller
// asking for a small window gets progressively more of the source until real
// content surfaces, capped at maxBytes.
func TestSubstantiveTail(t *testing.T) {
	real := "Do you want to proceed?\n❯ 1. Yes\n  2. No\n"
	var blink strings.Builder
	for i := 0; i < 2000; i++ { // ~130KB of pure blink noise, well past a 16KB window
		if i%2 == 0 {
			blink.WriteString(blinkCycle(" "))
		} else {
			blink.WriteString(blinkCycle("⏺"))
		}
	}
	full := []byte(real + blink.String())

	// readN simulates a source (file/ring) that returns the last n bytes of
	// `full`, tracking the largest n it was ever asked for.
	maxAsked := 0
	readN := func(n int) []byte {
		if n > maxAsked {
			maxAsked = n
		}
		if n >= len(full) {
			return full
		}
		return full[len(full)-n:]
	}

	t.Run("expands past the blink flood to recover real content", func(t *testing.T) {
		maxAsked = 0
		got := SubstantiveTail(readN, 4096, NeedsInputMaxExpandBytes)
		testutil.Equal(t, strings.Contains(string(got), "proceed"), true)
		testutil.Equal(t, needsInputSelectionRe.MatchString(string(got)), true)
		// Must have actually expanded beyond the initial ask.
		if maxAsked <= 4096 {
			t.Fatalf("expected SubstantiveTail to expand past 4096 bytes, only asked for %d", maxAsked)
		}
	})

	t.Run("does not expand when the window already has real content", func(t *testing.T) {
		maxAsked = 0
		small := []byte(real)
		readSmall := func(n int) []byte {
			maxAsked = n
			return small
		}
		got := SubstantiveTail(readSmall, 4096, NeedsInputMaxExpandBytes)
		testutil.Equal(t, string(got), real)
		testutil.Equal(t, maxAsked, 4096) // exactly one read, no expansion
	})

	t.Run("gives up at maxBytes without finding real content", func(t *testing.T) {
		var onlyBlink strings.Builder
		for i := 0; i < 20000; i++ {
			onlyBlink.WriteString(blinkCycle("⏺"))
		}
		src := []byte(onlyBlink.String())
		readAll := func(n int) []byte {
			if n >= len(src) {
				return src
			}
			return src[len(src)-n:]
		}
		got := SubstantiveTail(readAll, 4096, 8192)
		// No real content anywhere within reach of the cap — must not panic or
		// hang, and must return SOMETHING (old flat-tail behavior), not empty.
		if len(got) == 0 {
			t.Fatalf("expected a non-empty fallback tail, got empty")
		}
	})
}

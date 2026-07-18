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

// TestNeedsInputClear covers BUG-034: the deterministic clear of the needs-input
// flag on user input or archive, and its persistence otherwise.
func TestNeedsInputClear(t *testing.T) {
	t0 := time.Unix(1000, 0)
	t1 := time.Unix(2000, 0) // strictly after t0

	t.Run("nil deps pass candidates through unchanged", func(t *testing.T) {
		out, base := NeedsInputClear([]string{"a", "b"}, nil, nil, nil)
		testutil.DeepEqual(t, out, []string{"a", "b"})
		// Baselines are captured (zero) so subsequent ticks have state.
		testutil.Equal(t, len(base), 2)
	})

	t.Run("persists across ticks with no input (no decay)", func(t *testing.T) {
		lastInput := func(string) time.Time { return t0 } // input predates the flag
		var base map[string]time.Time
		var out []string
		for i := 0; i < 5; i++ {
			out, base = NeedsInputClear([]string{"a"}, base, lastInput, nil)
			testutil.DeepEqual(t, out, []string{"a"})
		}
		// Baseline frozen at the first-seen input time.
		testutil.Equal(t, base["a"].Equal(t0), true)
	})

	t.Run("clears on input delivered after the flag, stale tail still matching", func(t *testing.T) {
		// Tick 1: flagged; baseline captured at t0 (no input since the question).
		input := t0
		lastInput := func(string) time.Time { return input }
		out, base := NeedsInputClear([]string{"a"}, nil, lastInput, nil)
		testutil.DeepEqual(t, out, []string{"a"})

		// Tick 2: user responds (lastInput advances past the baseline). The
		// candidate is STILL passed in (the "?" is still in the tail), but it
		// must be cleared anyway — that is the crux.
		input = t1
		out, base = NeedsInputClear([]string{"a"}, base, lastInput, nil)
		testutil.Equal(t, len(out), 0)

		// Tick 3: still a candidate (stale tail), no new input → stays cleared.
		out, _ = NeedsInputClear([]string{"a"}, base, lastInput, nil)
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
		out, _ := NeedsInputClear([]string{"a", "b"}, prev, lastInput, nil)
		testutil.DeepEqual(t, out, []string{"a"}) // a kept, b cleared
	})

	t.Run("archive clears regardless of signal and drops the baseline", func(t *testing.T) {
		archived := func(id string) bool { return id == "a" }
		prev := map[string]time.Time{"a": t0, "b": t0}
		out, base := NeedsInputClear([]string{"a", "b"}, prev, nil, archived)
		testutil.DeepEqual(t, out, []string{"b"})
		if _, ok := base["a"]; ok {
			t.Error("archived task baseline should be dropped")
		}
	})

	t.Run("re-arms after the signal disappears then a fresh question arrives", func(t *testing.T) {
		input := t0
		lastInput := func(string) time.Time { return input }

		// Flagged, then user responds at t1 → cleared, baseline frozen at t0.
		_, base := NeedsInputClear([]string{"a"}, nil, lastInput, nil)
		input = t1
		out, base := NeedsInputClear([]string{"a"}, base, lastInput, nil)
		testutil.Equal(t, len(out), 0)

		// Agent responds: "a" is no longer a candidate this tick → baseline drops.
		_, base = NeedsInputClear(nil, base, lastInput, nil)
		if _, ok := base["a"]; ok {
			t.Error("baseline should drop when the task leaves the candidate set")
		}

		// Fresh question arrives: baseline re-captured at the CURRENT input (t1),
		// nothing has advanced past it → flagged again.
		out, _ = NeedsInputClear([]string{"a"}, base, lastInput, nil)
		testutil.DeepEqual(t, out, []string{"a"})
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

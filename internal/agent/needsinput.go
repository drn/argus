package agent

import (
	"hash/fnv"
	"io"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	xvt "github.com/charmbracelet/x/vt"

	"github.com/drn/argus/internal/sanitize"
	"github.com/drn/argus/internal/uxlog"
)

// needsInputChooserFooterRe matches the footer line of Claude Code's
// AskUserQuestion chooser widget. The footer renders an Enter-action affordance
// and an Esc-action affordance on the SAME line, separated by navigation hints
// ("↑/↓ to navigate" and middle-dot separators), e.g.
// "Enter to select · ↑/↓ to navigate · Esc to cancel". The footer is the robust
// signature because it is present regardless of which option the cursor sits on
// (the chooser's options don't follow the `❯ 1.` numbered shape).
//
// The matcher is deliberately tolerant of the exact wording, which has drifted
// across Claude Code releases: the Enter verb may be "select", "confirm", or
// "choose", and matching is case-insensitive. The load-bearing invariant is the
// PAIR of affordances on one line — an Enter-to-<action> phrase followed by an
// "Esc to" phrase, with [^\n\r]* explicitly barring newlines between them — so a
// lone "Esc to cancel" (or a lone "Enter to select") in ordinary prose, or the
// two phrases split across lines, does not trigger it.
var needsInputChooserFooterRe = regexp.MustCompile(`(?i)(?:enter|⏎|↵) to (?:select|confirm|choose)[^\n\r]*esc to`)

// needsInputWorkingRe matches Claude Code's "working" affordance — the interrupt
// hint ("esc to interrupt" / "ctrl+c to interrupt") it renders on the active
// spinner line WHILE generating or executing and REMOVES the moment it returns
// to the idle input prompt. Its ABSENCE is the discriminator the never-idle
// free-text-question pass keys off (see awaitingInputText): a busy agent whose
// narration happens to end in "?" still shows this affordance, so content
// stability alone never re-breaks BUG-032. The phrase is specific enough that it
// does not appear in ordinary transcript prose.
var needsInputWorkingRe = regexp.MustCompile(`(?i)(?:esc|ctrl-c|ctrl\+c) to interrupt`)

// needsInputSelectionRe is the visible-text signature of Claude Code's
// selection UI: U+276F (❯) followed (after zero or more horizontal whitespace
// characters) by ANY numbered option (one or more digits and a period). The same
// widget renders AskUserQuestion overlays, permission prompts, and plan-mode
// confirmations — matching the shared UI shape catches all current and future
// variants without chasing wording.
//
// Matching any numbered option, not just "1.", is deliberate: when the user
// navigates the permission cursor down, the ❯ glyph moves onto option 2 or 3
// (e.g. `❯ 2. Yes, and don't ask again`), and anchoring on "1." would silently
// miss a navigated-but-still-blocking prompt (BUG-035).
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
// `❯[ \t]*\d+\.` covers both. Trailing space variants ("❯  1.") fall in too.
var needsInputSelectionRe = regexp.MustCompile(`❯[ \t]*\d+\.`)

// needsInputTailWindow is how far back in the ring buffer we scan. Claude's
// prompt UI is rendered at the bottom of the viewport; the rendered cells
// live inside the most recent few KB of bytes (cursor moves + repaints). We
// scan a generous window so wide terminals with rich repaint sequences still
// match — and ANSI stripping shrinks the effective text further.
//
// Keep in sync with tui.detectNeedsInputTailBytes — the TUI reads the same-sized
// tail from the on-disk log, and the stale-log tradeoff documented on
// tui.sessionBlockedOnPrompt ("~16 KB of newer output") assumes they match.
const needsInputTailWindow = 16 * 1024

// DetectNeedsInput returns true if the tail of `buf` indicates the agent is
// blocked waiting for the user. Three signals fire:
//
//  1. Claude's numbered-selection prompt UI (`❯ 1.`) — permission prompts and
//     plan-mode confirms render through this widget.
//  2. The AskUserQuestion chooser footer (`Enter to select … Esc to cancel`) —
//     the full-screen chooser widget clears the viewport so earlier ❯ 1. rows
//     are gone; the footer line is the reliable signature.
//  3. The assistant's most recent text response ends with `?` — captures
//     plain-text questions where Claude stops generating without invoking a
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
	return detectNeedsInputText(sanitize.StripANSI(string(tail)))
}

// detectNeedsInputText is the body of DetectNeedsInput operating on already
// plain (ANSI-stripped or vt-rendered) text. Shared with DetectNeedsInputScreen
// so the alt-screen fallback matches the identical three signals against the
// reconstructed screen text.
func detectNeedsInputText(text string) bool {
	if needsInputSelectionRe.MatchString(text) {
		return true
	}
	if needsInputChooserFooterRe.MatchString(text) {
		return true
	}
	return endsInQuestion(text)
}

// DetectSelectionPrompt reports whether the tail shows one of Claude's
// UNAMBIGUOUS blocking selection widgets — the numbered-selection cursor
// (`❯ 1.`, permission / edit / plan-mode confirms and open-ended choices) or
// the AskUserQuestion chooser footer (`Enter to select … Esc to cancel`). It
// deliberately EXCLUDES the fuzzy trailing-question heuristic (endsInQuestion):
// a transcript line ending in `?` can sit above the rendered input box while
// the agent is merely between steps, so on its own it is a reliable "blocked"
// signal only behind the strong idle gate.
//
// The content-stability pass (BUG-032) flags a session that NEVER reaches the
// idle set, i.e. it removes that gate — so it MUST use this stricter signal,
// not DetectNeedsInput, or a busy agent whose last line happens to end in `?`
// and whose content is briefly stable for a tick would false-positive. The
// idle-gated and sticky passes keep using DetectNeedsInput (idle is gate
// enough for the question heuristic — unchanged behavior).
func DetectSelectionPrompt(buf []byte) bool {
	if len(buf) == 0 {
		return false
	}
	tail := buf
	if len(tail) > needsInputTailWindow {
		tail = tail[len(tail)-needsInputTailWindow:]
	}
	return detectSelectionPromptText(sanitize.StripANSI(string(tail)))
}

// detectSelectionPromptText is the body of DetectSelectionPrompt operating on
// already plain (ANSI-stripped or vt-rendered) text.
func detectSelectionPromptText(text string) bool {
	return needsInputSelectionRe.MatchString(text) ||
		needsInputChooserFooterRe.MatchString(text)
}

// awaitingInputText reports whether plain (ANSI-stripped or vt-rendered) text
// shows the agent AWAITING user input, for the never-idle content-stability pass
// (see AwaitingInputFingerprint). It is broader than detectSelectionPromptText:
// it ALSO flags a free-text trailing question, but only when the agent is
// genuinely waiting rather than working.
//
// Two awaiting-input shapes qualify:
//
//  1. The UNAMBIGUOUS selection widget (❯ N. / chooser footer) — safe without
//     further gating; a streaming agent that flashes it is caught by the
//     content-stability fingerprint, not here.
//  2. A FREE-TEXT trailing question (endsInQuestion) WHEN the "working"
//     affordance (needsInputWorkingRe — "esc to interrupt") is ABSENT. The
//     absence is the discriminator that keeps this from re-breaking BUG-032: a
//     busy agent whose last line ends in `?` and stalls on a spinner frame for a
//     tick is content-stable AND ends in `?`, but it still renders the interrupt
//     hint while working, so it never qualifies. Content stability ALONE is NOT
//     a sufficient guard for endsInQuestion — this gate is required.
func awaitingInputText(text string) bool {
	if detectSelectionPromptText(text) {
		return true
	}
	return endsInQuestion(text) && !needsInputWorkingRe.MatchString(text)
}

// BlockedOnPrompt reports whether the session's recent output shows the agent
// blocked on a user prompt (selection UI overlay or a trailing question). A
// rerender kick must never fire while this is true: stop+restart via
// --session-id rehydrates the conversation but NOT the ephemeral prompt
// overlay, silently dismissing the question. Reads the session's local ring
// buffer — correct server-side (daemon owns the live ring) but unreliable in
// daemon-client mode where the ring only fills after a stream attaches; that
// path detects via the disk log instead (see the TUI's readSessionLogTailBytes).
//
// Like DetectNeedsInput, pair this with an idle check at the call site — a
// prompt the agent is still streaming past is not blocking. The sole caller
// (the API resize handler) gates on IsIdle() before reaching here.
func BlockedOnPrompt(sess SessionHandle) bool {
	if sess == nil {
		return false
	}
	return DetectNeedsInput(sess.RecentOutputTail(needsInputTailWindow))
}

// promptNBSP is the visible-text signature of Claude Code's current idle
// input prompt: U+276F (❯) immediately followed by a non-breaking space
// (U+00A0). The NBSP is the discriminator — transcript text that happens to
// contain ❯ (shell prompts in command output, the selection UI's `❯ 1.`)
// uses a regular space, while Claude's input-line renderer emits the NBSP.
const promptNBSP = "❯\u00a0"

// endsInQuestion returns true when the assistant's last visible line of text
// — the line immediately above Claude's input prompt — ends with `?` (or the
// full-width `？`).
//
// Anchoring on Claude's input prompt is what makes the heuristic usable:
// every hint/status line below the input (e.g. `? for shortcuts`,
// `· ← for agents`) is excluded, and we only inspect the genuine transcript
// above it. Without the anchor, those hint lines would dominate the search
// and produce constant false positives on every idle session. Two prompt
// shapes are recognized, latest occurrence wins:
//
//   - `❯` + NBSP — the current Claude Code idle input line (no box).
//   - `╭` — the prompt-box opener drawn by older Claude Code versions.
//
// When neither is present — buffer too short, or Claude has not rendered the
// prompt yet — we conservatively return false. The selection-UI branch above
// still fires in those cases when it's actually warranted.
//
// The backward walk skips decoration lines, not just blank ones: Claude
// renders a spinner-glyph timing line (`✻ Brewed for 57s`) between the last
// transcript line and the prompt, so the question we're after is one line
// further up. See decorationLine.
func endsInQuestion(stripped string) bool {
	idx := strings.LastIndex(stripped, "╭")
	if j := strings.LastIndex(stripped, promptNBSP); j > idx {
		idx = j
	}
	if idx < 0 {
		return false
	}
	above := stripped[:idx]
	// Walk backward through whatever sits above the prompt, skipping blank
	// and decoration lines until we hit the last content line of the
	// transcript. Lines split on `\r` as well as `\n`: after ANSI strip the
	// live PTY stream separates visual lines with bare carriage returns.
	for {
		cut := strings.LastIndexAny(above, "\n\r")
		line := above[cut+1:]
		trimmed := strings.TrimRightFunc(line, unicode.IsSpace)
		if trimmed != "" && !decorationLine(trimmed) {
			r, _ := utf8.DecodeLastRuneInString(trimmed)
			return r == '?' || r == '？'
		}
		if cut < 0 {
			return false
		}
		above = above[:cut]
	}
}

// contentFingerprintLines bounds how many trailing DISTINCT substantive lines
// feed the content fingerprint. The lines are de-duplicated (first-occurrence
// order) before this cap is applied, which is what keeps the fingerprint stable
// for an alt-screen session that repaints the same frame over and over: the
// buffered byte window may hold a varying number of identical frames, but the
// set of distinct lines on screen is the same every tick. Hashing raw window
// lines would flap as whole frames slide in and out of the 16 KB tail. Capping
// at the tail of the distinct list discriminates genuinely-new output (fresh
// distinct lines arrive) from a static prompt (distinct set unchanged).
const contentFingerprintLines = 40

// ContentFingerprint returns a stable hash of a session's recent MEANINGFUL
// output — the agent's transcript content with animation/redraw chrome removed.
// It is the discriminator BUG-032 needs: a session parked at a prompt emits a
// steady trickle of redraw bytes (spinner, cursor blink, alt-screen repaint)
// that bumps the raw-output clock and keeps Session.IsIdle() false forever, so
// the idle-gated needs-input detector never scans it. Two output tails that
// differ ONLY in that animation chrome fingerprint identically; a tail with
// genuinely new transcript content fingerprints differently. Callers compare a
// session's fingerprint across detector ticks: unchanged ⇒ content-stable
// (treat as blocked when the prompt signature is also present), changed ⇒ the
// agent is still producing output (not blocked).
//
// Normalization, in order: strip ANSI, fold bare \r line breaks (the live PTY
// stream separates visual lines with carriage returns after strip), drop blank
// and volatile-chrome lines (fingerprintVolatileLine), de-duplicate (first
// occurrence wins) so repeated repaint frames collapse, then keep the trailing
// contentFingerprintLines distinct lines and hash them.
func ContentFingerprint(tail []byte) uint64 {
	return fingerprintText(sanitize.StripANSI(string(tail)))
}

// fingerprintText is the body of ContentFingerprint operating on already plain
// text. Shared with the alt-screen path (SelectionPromptFingerprint), which
// feeds the vt-rendered screen — naturally stable for a parked prompt — instead
// of the raw byte tail (which never stabilizes while the prompt repaints).
func fingerprintText(stripped string) uint64 {
	stripped = strings.ReplaceAll(stripped, "\r\n", "\n")
	stripped = strings.ReplaceAll(stripped, "\r", "\n")

	var lines []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(stripped, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || fingerprintVolatileLine(trimmed) || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		lines = append(lines, trimmed)
	}
	if len(lines) > contentFingerprintLines {
		lines = lines[len(lines)-contentFingerprintLines:]
	}

	h := fnv.New64a()
	for _, line := range lines {
		h.Write([]byte(line))
		h.Write([]byte{'\n'})
	}
	return h.Sum64()
}

// fingerprintVolatileLine reports whether a stripped, trimmed line is chrome
// that redraws frame-to-frame without representing new agent output, so it must
// be excluded from the content fingerprint. Two sources of volatility:
//
//   - decorationLine: the spinner-glyph timing line ("✻ Brewed for 57s") and
//     box-drawing horizontal rules — the spinner verb/seconds tick while the
//     agent waits, and the glyph cycles, but none of it is new output.
//   - Claude's input-prompt line (leading ❯): it carries a blinking cursor
//     block that toggles on every redraw, and the selection cursor (❯ 1.) is
//     repainted as the user navigates — neither is agent output.
func fingerprintVolatileLine(trimmed string) bool {
	if decorationLine(trimmed) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(trimmed)
	return r == '❯'
}

// spinnerGlyphs are the runes Claude Code's spinner animation cycles through.
// The post-response timing line ("✻ Brewed for 57s", "✶ Pondered for 12s", …)
// starts with one of these; the verb varies per response, so we key on the
// glyph (UI shape), never the wording.
var spinnerGlyphs = map[rune]bool{
	'·': true, '✢': true, '✳': true, '✶': true, '✻': true, '✽': true,
}

// decorationLine reports whether a non-blank line above the prompt is UI
// chrome rather than transcript content: the spinner-glyph timing line, or a
// horizontal rule of box-drawing dashes. Transcript content lines start with
// `⏺`/`⎿` or plain text, so neither check can swallow a real question.
func decorationLine(line string) bool {
	line = strings.TrimLeftFunc(line, unicode.IsSpace)
	r, _ := utf8.DecodeRuneInString(line)
	if spinnerGlyphs[r] {
		return true
	}
	for _, r := range line {
		if r != '─' && r != '━' && r != '═' {
			return false
		}
	}
	return true
}

// risReset is the RIS (Reset to Initial State, ESC c) control that clears a
// reused emulator back to a blank main screen between renders.
var risReset = []byte("\x1bc")

// ScreenRenderer reconstructs the VISIBLE terminal screen from a session's raw
// PTY tail bytes, so needs-input detection can match the EMULATED screen rather
// than sanitize.StripANSI of the raw stream. The distinction matters for a
// FULLSCREEN (alt-screen) Claude agent: it paints its prompt with cursor-
// addressed in-place redraws, so the `❯` and `1.` glyphs are not linearly
// adjacent in the byte stream (StripANSI only removes escapes, it does NOT apply
// cursor positioning) — the selection regex never matches the raw tail and the
// session is silently never flagged needs-input (BUG-033). Feeding the tail
// through a vt emulator places the glyphs where they actually render, so the
// existing regexes fire.
//
// Reuse ONE renderer per detector context (the daemon's idle watcher; the TUI
// tick): it lazily allocates a single emulator and drives one drain goroutine
// for its lifetime, resetting via RIS between renders. This mirrors
// terminal.PreviewVT's reuse-via-RIS pattern — allocating-and-Close-ing a fresh
// drained emulator per render would race the drain goroutine's unlocked Read
// against Close on the emulator's closed flag (flagged by -race; see
// terminal.NewDrainedEmulator's lifecycle note). A zero ScreenRenderer is ready
// to use; it is NOT safe for concurrent use (each detector context is single-
// goroutine).
type ScreenRenderer struct {
	emu        *xvt.SafeEmulator
	cols, rows int
}

// render writes tail through the emulator sized to cols×rows and returns the
// visible screen as plain text. Non-positive dimensions fall back to the
// session default (80×24) — wrong cols changes wrapping, so callers should pass
// the session's real PTY size (agent.LoadSessionSize) when known.
func (r *ScreenRenderer) render(tail []byte, cols, rows int) string {
	if cols <= 0 {
		cols = int(DefaultTermCols)
	}
	if rows <= 0 {
		rows = int(DefaultTermRows)
	}
	if r.emu == nil {
		r.emu = xvt.NewSafeEmulator(cols, rows)
		r.cols, r.rows = cols, rows
		// One drain goroutine for the renderer's lifetime: x/vt writes query
		// responses (DA/DSR/cursor-position) to an internal io.Pipe that blocks
		// Write until read — Claude's output stream contains such queries.
		go io.Copy(io.Discard, r.emu) //nolint:errcheck
	} else {
		// Reset to a clean blank screen, then re-size if this session differs
		// from the last one rendered (RIS preserves dimensions).
		safeEmuWrite(r.emu, risReset)
		if cols != r.cols || rows != r.rows {
			r.emu.Resize(cols, rows)
			r.cols, r.rows = cols, rows
		}
	}
	safeEmuWrite(r.emu, tail)
	return r.emu.String()
}

// safeEmuWrite writes to the emulator, recovering from panics in upstream vt
// code (e.g. cursor positions from a larger terminal). Mirrors
// terminal.SafeEmuWrite; duplicated here to keep internal/agent free of an
// import cycle through internal/tui/terminal.
func safeEmuWrite(emu *xvt.SafeEmulator, data []byte) {
	defer func() {
		if rec := recover(); rec != nil {
			uxlog.Log("[needsinput] recovered from emulator panic: %v\n%s", rec, debug.Stack())
		}
	}()
	if _, err := emu.Write(data); err != nil {
		uxlog.Log("[needsinput] emulator write error: %v", err)
	}
}

// NeedsInputClear filters a candidate needs-input set (already computed by the
// idle / content-stability / sticky passes) down to the set that should still
// surface "(?)", applying the two BUG-034 clear conditions that the entry
// heuristics never apply on their own:
//
//	(a) Clear on user input. A free-text question (endsInQuestion) leaves its
//	    "?" in the recent-output tail indefinitely, so the entry heuristic
//	    re-matches every tick and the flag would never clear even after the user
//	    answered. The clear is deterministic and does NOT wait for the question
//	    to scroll out of the tail: per candidate task we freeze the session's
//	    last-input timestamp observed when it FIRST entered the set (its
//	    "baseline"), and once lastInputOf(id) advances past that baseline the
//	    user has responded — the task is dropped and not re-added while that
//	    input remains the latest, even though the stale "?" still matches.
//	(b) Clear on archive. An archived task is dropped regardless of its signal
//	    so it stops lighting "(?)" and stops rolling up to ancestor coordinators.
//
// The baseline map is the carry-forward state: pass the previous tick's return
// as prevBaseline. An entry is kept (frozen) as long as the task remains a
// non-archived candidate (whether surfaced or input-cleared), and is dropped
// once the task is no longer a candidate (its signal disappeared) or is
// archived — so a fresh question raised after the user's response re-captures a
// new baseline and re-arms the flag.
//
// lastInputOf returns a session's most-recent-input wall-clock time (zero if
// never / unknown); a nil func disables clear-on-input (every baseline stays
// zero, nothing ever advances past it). archivedOf reports whether a task is
// archived; a nil func disables clear-on-archive. With both nil the candidate
// set passes through unchanged, so callers that cannot observe input/archive
// state degrade to pre-BUG-034 behavior.
func NeedsInputClear(candidates []string, prevBaseline map[string]time.Time, lastInputOf func(string) time.Time, archivedOf func(string) bool) (out []string, newBaseline map[string]time.Time) {
	out = make([]string, 0, len(candidates))
	newBaseline = make(map[string]time.Time, len(candidates))
	for _, id := range candidates {
		if archivedOf != nil && archivedOf(id) {
			// Archived: drop from the set AND from the baseline map (so an
			// un-archive later re-arms cleanly).
			continue
		}
		// Capture the baseline on first entry; freeze it across subsequent
		// ticks while the task stays a candidate.
		baseline, tracked := prevBaseline[id]
		if !tracked {
			if lastInputOf != nil {
				baseline = lastInputOf(id)
			}
		}
		newBaseline[id] = baseline
		// Input arrived after the flag was raised → the user responded → clear.
		if lastInputOf != nil && lastInputOf(id).After(baseline) {
			continue
		}
		out = append(out, id)
	}
	return out, newBaseline
}

// ContentIdleFingerprint powers content-aware idle classification (BUG-036). It
// renders the EMULATED screen and returns its animation-stripped content
// fingerprint plus whether the agent shows Claude's "working" affordance (the
// interrupt hint, `needsInputWorkingRe` — "esc to interrupt"). A session is
// content-idle when this fingerprint is UNCHANGED across the idle threshold AND
// `working` is false (see ContentIdle).
//
// The fingerprint is taken over the EMULATED screen, never the raw tail: a
// fullscreen (alt-screen) agent's raw bytes never stabilize while it repaints
// (the BUG-033 lesson), but its rendered screen does once it parks. A nil
// renderer falls back to the raw-tail fingerprint (linear agents only); the
// working-affordance check then runs over the ANSI-stripped raw tail.
func ContentIdleFingerprint(r *ScreenRenderer, buf []byte, cols, rows int) (fp uint64, working bool) {
	if r == nil || len(buf) == 0 {
		tail := buf
		if len(tail) > needsInputTailWindow {
			tail = tail[len(tail)-needsInputTailWindow:]
		}
		stripped := sanitize.StripANSI(string(tail))
		return ContentFingerprint(buf), needsInputWorkingRe.MatchString(stripped)
	}
	screen := r.render(buf, cols, rows)
	return fingerprintText(screen), needsInputWorkingRe.MatchString(screen)
}

// NeedsInputEscalationTicks bounds the worst-case delay before a genuinely
// parked session (showing Claude's selection-prompt widget with no "working"
// affordance) is recognized when its content fingerprint never converges
// tick-to-tick — e.g. unrelated per-tick-varying text elsewhere in the 16 KB
// tail window keeps shifting the fingerprint even though the agent itself is
// truly parked (BUG-029). The ordinary content-stability pass requires the
// FULL fingerprint to match across just two consecutive ticks; that never
// happens here, so without this escalation the session would stay flagged
// "active" forever (spinner never stops, "(?)" never appears).
//
// Chosen in the middle of the 5-10 consecutive-tick range this fix's design
// settled on: long enough that a transient/coincidental match — the agent
// scrolls a "❯ 1."-looking line past while still genuinely generating — can't
// misfire, short enough that "stuck forever" becomes "stuck at most ~8
// ticks" (ticks run about once per second — see the TUI's onTick and the
// daemon's idle watcher). Tune here, nowhere else.
const NeedsInputEscalationTicks = 8

// ParkedSelectionSignal reports whether tail (or, for an alt-screen session,
// its emulated screen) currently shows Claude's UNAMBIGUOUS selection-prompt
// widget (detectSelectionPromptText — the numbered cursor or chooser footer)
// with the "working" affordance absent. This is the BUG-029 escalation
// fallback's per-tick qualifying condition (feed the result to
// EscalateParkedSelection): selection-shape-present + working-absent is
// already the strong "parked" signal AwaitingInputFingerprint and
// DetectSelectionPrompt trust elsewhere; requiring it to hold for
// NeedsInputEscalationTicks consecutive ticks (not just once) is what keeps
// false-positive risk low without loosening the shared chrome-recognition
// allowlist (fingerprintVolatileLine / decorationLine).
//
// Mirrors the raw-first-then-emulated-fallback pattern used throughout this
// file (see AwaitingInputFingerprint / DetectNeedsInputScreen): a linear
// agent's raw tail is authoritative the moment it shows (or doesn't show) the
// selection shape, so it never touches the emulator. Only when the raw tail
// misses the selection shape entirely (a candidate alt-screen, cursor-
// addressed prompt) does it fall back to the reconstructed screen.
func ParkedSelectionSignal(r *ScreenRenderer, buf []byte, cols, rows int) bool {
	if len(buf) == 0 {
		return false
	}
	tail := buf
	if len(tail) > needsInputTailWindow {
		tail = tail[len(tail)-needsInputTailWindow:]
	}
	stripped := sanitize.StripANSI(string(tail))
	if detectSelectionPromptText(stripped) {
		return !needsInputWorkingRe.MatchString(stripped)
	}
	if r == nil {
		return false
	}
	screen := r.render(buf, cols, rows)
	if !detectSelectionPromptText(screen) {
		return false
	}
	return !needsInputWorkingRe.MatchString(screen)
}

// EscalateParkedSelection advances the BUG-029 consecutive-tick escalation
// counter for a single session: prevTicks is the previous tick's count (0 if
// never tracked / first tick), qualifies is this tick's ParkedSelectionSignal.
// It returns the new count and whether the session has now reached
// NeedsInputEscalationTicks.
//
// A non-qualifying tick RESETS the counter to zero rather than merely
// pausing it — the escalation is meant to catch a combination that holds
// CONTINUOUSLY, so a broken streak (the prompt disappears, or the working
// affordance appears, even for a single tick) must restart the wait from
// scratch. This is a pure step function; callers own the per-ID map (see
// ContentIdleState.esc and the TUI's needsInputEscalation field) exactly like
// the fingerprint/since carry-forward maps elsewhere in this file.
func EscalateParkedSelection(prevTicks int, qualifies bool) (newTicks int, escalated bool) {
	if !qualifies {
		return 0, false
	}
	newTicks = prevTicks + 1
	return newTicks, newTicks >= NeedsInputEscalationTicks
}

// ContentIdleState carries the per-task content-idle bookkeeping across ticks:
// each tracked session's last emulated-screen fingerprint and the wall-clock
// time that fingerprint was FIRST observed at its current value. Pass nil to
// ContentIdle on the first tick; thread the returned state back as `prev` each
// subsequent tick. A zero-value pointer is not usable — always use the value
// ContentIdle returns.
type ContentIdleState struct {
	fp    map[string]uint64
	since map[string]time.Time
	// esc carries the BUG-029 escalation counter (EscalateParkedSelection) per
	// task ID across ticks — independent of fp/since, since its whole purpose
	// is to fire even when the fingerprint never converges.
	esc map[string]int
}

// ContentIdle returns the subset of running task IDs that are CONTENT-IDLE — a
// session that is NOT already raw-idle but whose animation-stripped
// emulated-screen fingerprint has been unchanged for at least idleThreshold AND
// which shows no "working" affordance. This is the augmentation Session.IsIdle()
// (raw-byte based) misses for a fullscreen/alt-screen agent parked at its
// prompt: continuous repaint bytes keep its raw-idle clock false forever, yet
// its visible screen is stable when it is doing nothing (BUG-036). Callers union
// it with the raw-idle set to drive the Hera spinner (a parked agent shows a
// static idle glyph, not a perpetual spinner) and the idle-push busy→idle
// transition (fires once when content stabilizes; the caller's per-work-cycle
// gate guarantees exactly-once, so a re-asserted signal cannot storm).
//
// A genuinely working agent is never flagged by EITHER of two guards: its
// emulated content changes tick-to-tick (fingerprint differs → the stability
// timer resets to `now`), AND/OR it renders the interrupt affordance (`working`
// → it is skipped and never accrues stability). Both matter — content stability
// ALONE would false-idle a thinking agent stalled on a spinner frame, the same
// reasoning that gates the BUG-032/035 needs-input passes.
//
// A session is ALSO reported content-idle once EscalateParkedSelection
// escalates (BUG-029): if its tail shows the unambiguous selection-prompt
// signal with the working affordance absent for NeedsInputEscalationTicks
// CONSECUTIVE ticks, it is treated as idle even if its full-screen fingerprint
// never converged tick-to-tick (unrelated per-tick-varying content elsewhere
// in the window kept shifting it). This bounds the worst case for a genuinely
// parked-but-noisy session from "forever" to NeedsInputEscalationTicks ticks,
// without loosening what the fingerprint itself treats as chrome.
//
// rawIdle is the set of already-raw-idle task IDs (skipped — already idle, and
// dropped from the returned state). tailOf returns a task's recent output tail
// (nil/empty → skipped). sizeOf returns its PTY dims for the emulator. screen is
// the reused per-context ScreenRenderer (single-goroutine). The returned state
// must be threaded back as `prev` next tick.
func ContentIdle(running []string, rawIdle map[string]bool, tailOf func(string) []byte, sizeOf func(string) (cols, rows int), screen *ScreenRenderer, prev *ContentIdleState, now time.Time) (idle []string, next *ContentIdleState) {
	next = &ContentIdleState{
		fp:    make(map[string]uint64),
		since: make(map[string]time.Time),
		esc:   make(map[string]int),
	}
	for _, id := range running {
		if rawIdle[id] {
			continue // already idle — not an augmentation, and reset its tracking
		}
		tail := tailOf(id)
		if len(tail) == 0 {
			continue
		}
		cols, rows := sizeOf(id)
		fp, working := ContentIdleFingerprint(screen, tail, cols, rows)

		// BUG-029 escalation: advance the consecutive-tick counter regardless of
		// whether the fingerprint itself converged this tick (see
		// EscalateParkedSelection). Computed before the working-affordance early
		// return below so a working tick correctly resets it to zero — the
		// qualifying condition is selection-shape-present AND working-absent, so
		// it can never be true while working is true anyway.
		prevTicks := 0
		if prev != nil {
			prevTicks = prev.esc[id]
		}
		newTicks, escalated := EscalateParkedSelection(prevTicks, ParkedSelectionSignal(screen, tail, cols, rows))
		if newTicks > 0 {
			next.esc[id] = newTicks
		}

		if working {
			// The agent is generating — it must never accrue stability. Drop it
			// from the carried state so a later quiet period starts a fresh timer.
			continue
		}
		since := now
		if prev != nil {
			if pfp, ok := prev.fp[id]; ok && pfp == fp {
				if t, ok := prev.since[id]; ok {
					since = t // fingerprint unchanged — keep the original stable-since time
				}
			}
		}
		next.fp[id] = fp
		next.since[id] = since
		if now.Sub(since) >= idleThreshold || escalated {
			idle = append(idle, id)
		}
	}
	return idle, next
}

// DetectNeedsInputScreen is the alt-screen-aware form of DetectNeedsInput. It
// first matches the raw byte stream (fast path — linear / main-screen agents
// behave EXACTLY as DetectNeedsInput, and the emulator is never touched), and
// only on a raw miss does it reconstruct the visible screen via r and re-match
// the same three signals against the rendered text. A nil renderer disables the
// fallback, making it identical to DetectNeedsInput.
func DetectNeedsInputScreen(r *ScreenRenderer, buf []byte, cols, rows int) bool {
	if DetectNeedsInput(buf) {
		return true
	}
	if r == nil || len(buf) == 0 {
		return false
	}
	return detectNeedsInputText(r.render(buf, cols, rows))
}

// AwaitingInputFingerprint powers the never-idle content-stability pass
// (BUG-032) with alt-screen support (BUG-033) and free-text-question coverage
// (BUG-035). It reports whether the tail shows the agent awaiting user input
// (awaitingInputText: the unambiguous selection widget, OR a free-text trailing
// question with the "working" affordance absent) and, when it does, the
// stability fingerprint to compare across ticks. The source is paired with how
// the signal was detected so linear agents stay byte-identical to pre-BUG-033:
//
//   - Raw match (linear): fingerprint the raw tail (== ContentFingerprint).
//   - Raw miss → emulated screen (alt-screen): if the signal appears on the
//     rendered screen, fingerprint THAT text. A parked alt-screen prompt's
//     visible screen is naturally stable tick-to-tick (only off-screen repaint
//     bytes change), so its fingerprint holds and the 2nd qualifying tick flags
//     it; a streaming agent's rendered content shifts, so the fingerprint
//     differs and it is never flagged — the same false-positive guard, now on
//     the emulated screen. The screen is rendered AT MOST ONCE per call.
//
// ok=false ⇒ no awaiting-input signal; fp is meaningless and must not be stored.
func AwaitingInputFingerprint(r *ScreenRenderer, buf []byte, cols, rows int) (fp uint64, ok bool) {
	if len(buf) > 0 {
		tail := buf
		if len(tail) > needsInputTailWindow {
			tail = tail[len(tail)-needsInputTailWindow:]
		}
		if awaitingInputText(sanitize.StripANSI(string(tail))) {
			return ContentFingerprint(buf), true
		}
	}
	if r == nil || len(buf) == 0 {
		return 0, false
	}
	screen := r.render(buf, cols, rows)
	if !awaitingInputText(screen) {
		return 0, false
	}
	return fingerprintText(screen), true
}

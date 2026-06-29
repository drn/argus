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
// AskUserQuestion chooser widget. The footer always renders "Enter to select"
// and "Esc to cancel" on the same line, separated by navigation hints
// ("↑/↓ to navigate" and middle-dot separators). Both phrases must appear on
// the same line — [^\n\r]* explicitly bars newlines — so an isolated
// "Esc to cancel" elsewhere in the output doesn't trigger it.
var needsInputChooserFooterRe = regexp.MustCompile(`Enter to select[^\n\r]*Esc to cancel`)

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

// SelectionPromptFingerprint powers the BUG-032 content-stability pass with
// alt-screen support. It reports whether the tail shows the UNAMBIGUOUS
// selection widget (❯ 1. / chooser footer — NOT the fuzzy trailing-question
// heuristic) and, when it does, the stability fingerprint to compare across
// ticks. The source is paired with how the widget was detected so linear agents
// stay byte-identical to pre-BUG-033:
//
//   - Raw match (linear): fingerprint the raw tail (== ContentFingerprint).
//   - Raw miss → emulated screen (alt-screen): if the widget appears on the
//     rendered screen, fingerprint THAT text. A parked alt-screen prompt's
//     visible screen is naturally stable tick-to-tick (only off-screen repaint
//     bytes change), so its fingerprint holds and the 2nd qualifying tick flags
//     it; a streaming agent's rendered content shifts, so the fingerprint
//     differs and it is never flagged — the same false-positive guard, now on
//     the emulated screen.
//
// ok=false ⇒ no selection widget; fp is meaningless and must not be stored.
func SelectionPromptFingerprint(r *ScreenRenderer, buf []byte, cols, rows int) (fp uint64, ok bool) {
	if DetectSelectionPrompt(buf) {
		return ContentFingerprint(buf), true
	}
	if r == nil || len(buf) == 0 {
		return 0, false
	}
	screen := r.render(buf, cols, rows)
	if !detectSelectionPromptText(screen) {
		return 0, false
	}
	return fingerprintText(screen), true
}

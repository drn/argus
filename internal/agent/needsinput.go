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
// surface "(?)", applying the BUG-034 clear conditions plus the BUG-063
// stale-re-candidacy guard the entry heuristics never apply on their own:
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
//	(c) Clear on demonstrated resumed activity (resumedOf, see
//	    ResumeActivityTick). A hera coordinator relays the human's answer via
//	    reliable-notify delivery (WriteInputSystem), which deliberately does
//	    NOT advance lastUserInput (that is the whole point of the BUG-034 fix
//	    below) — so (a) can never fire for a relayed answer, and the task would
//	    stay flagged forever even after the worker provably resumes real work.
//	    resumedOf reports whether the session has shown Claude's "working"
//	    affordance for NeedsInputResumeTicks CONSECUTIVE ticks since being
//	    flagged, which only a genuinely un-stuck agent can sustain (an
//	    unrelated system nudge to a still-parked agent does not make it
//	    generate/execute for a sustained stretch) — see ResumeActivityTick.
//	(d) Clear on demonstrated settlement (settledOf, see SettleTick; BUG-072).
//	    resumedOf (c) can only fire while the session is ACTIVELY showing the
//	    working affordance — its streak resets to zero the instant the session
//	    goes idle. A worker that resolves its own block and wraps up in FEWER
//	    than NeedsInputResumeTicks consecutive ticks settles into idle before
//	    that streak can ever reach threshold, and can never resume it afterward
//	    (an idle session never shows the working affordance again) — so without
//	    a separate path, such a worker stays flagged until an unrelated,
//	    incidental keystroke happens to advance LastUserInput. settledOf reports
//	    whether the session has been genuinely idle AND shown NONE of the
//	    needs-input signals for NeedsInputSettleTicks CONSECUTIVE ticks — see
//	    SettleTick.
//
// The baseline map is the carry-forward state: pass the previous tick's return
// as prevBaseline. An entry is kept (frozen) as long as the task remains a
// non-archived candidate (whether surfaced or input-cleared), and is dropped
// once the task is no longer a candidate (its signal disappeared) or is
// archived — so a fresh question raised after the user's response re-captures a
// new baseline and re-arms the flag.
//
// BUG-063: candidates is NOT a monotonic, always-growing set — a task drops
// out of it the instant it clears, and its baseline is forgotten in that same
// instant (the baseline map is rebuilt fresh from `candidates` every call). A
// LATER tick's content-stability fingerprint match or BUG-060 escalation grace
// tick (both scan every RUNNING session independent of candidacy — see
// detectNeedsInputSticky / computeNeedsInput) can re-present the SAME task as
// a candidate with NOTHING new having happened — lastInputOf(id) is still the
// exact timestamp that already cleared it. Without a guard, that re-candidacy
// looks identical to a task's first-ever candidacy: it recaptures
// baseline = lastInputOf(id), which can never be "after" itself, sticking the
// task in the set until the user produces a strictly newer timestamp.
//
// running (the full set of currently-running task IDs — a superset of
// candidates; both callers already have this) and prevCleared/newCleared (the
// carry-forward state, threaded exactly like prevBaseline/newBaseline) close
// this gap: whenever a real clear fires for a task, the lastInputOf value at
// that moment is recorded as its cleared marker and threaded forward for every
// ID in running, regardless of whether that ID is a candidate on any given
// tick. A later candidacy is checked against this marker BEFORE the ordinary
// fresh-baseline path: if lastInputOf(id) has not advanced past the marker,
// the candidacy is stale and is suppressed outright (not added to out, no
// baseline recaptured). A genuinely newer lastInputOf(id) fails that check on
// its own — no explicit expiry needed — and falls through to re-arm normally,
// capturing a fresh baseline exactly like a first-ever candidacy. The marker
// is dropped (not carried into newCleared) when a task is archived or leaves
// running, mirroring the baseline's existing archive-drop behavior, so a
// restart or un-archive re-arms cleanly.
//
// Scope limit fixed by BUG-067 (see context/knowledge/gotchas/events.md): this
// function originally could not distinguish "the same already-answered
// content re-detected" from "a second, textually distinct, still-unanswered
// prompt that happens to arrive before the user's next keystroke" — both
// present as the same task ID at the same lastInputOf timestamp. That was
// evaluated and ACCEPTED as a trade-off at the time (BUG-063), on the
// assumption that a second distinct prompt arriving before the user's next
// keystroke was a rare, bounded scenario. It is not: Claude Code's
// AskUserQuestion / /brainstorm flow routinely asks several DISTINCT
// questions in sequence within one session, each one answered directly in the
// pane — exactly the shape that collides with the stale-marker guard.
// fingerprintOf (see below) closes this gap by comparing content, not just
// timestamp, so a genuinely distinct later prompt re-arms instead of being
// silently swallowed forever.
//
// lastInputOf returns a session's most-recent-input wall-clock time (zero if
// never / unknown); a nil func disables clear-on-input (every baseline stays
// zero, nothing ever advances past it, and no cleared marker is ever
// recorded). archivedOf reports whether a task is archived; a nil func
// disables clear-on-archive. resumedOf reports whether a task has
// demonstrated sustained resumed activity (see ResumeActivityTick); a nil func
// disables clear-on-resume. settledOf reports whether a task has demonstrated
// settlement — genuinely idle with no current needs-input signal, sustained
// for NeedsInputSettleTicks consecutive ticks (see SettleTick, BUG-072); a nil
// func disables clear-on-settle. fingerprintOf returns a task's CURRENT
// content fingerprint (e.g. agent.AwaitingInputFingerprint's fp, as already
// computed by both callers' content-stability pass) and whether one is
// available this tick; a nil func (or an unavailable fingerprint on either
// side of a comparison) degrades to the pre-BUG-067 timestamp-only behavior —
// safe, since it only widens what gets suppressed, never what gets surfaced.
// With all five nil the candidate set passes through unchanged, so callers
// that cannot observe input/archive/activity/settlement/content state degrade
// to pre-BUG-034 behavior.
func NeedsInputClear(candidates []string, running []string, prevBaseline map[string]time.Time, prevCleared map[string]ClearedMarker, lastInputOf func(string) time.Time, archivedOf func(string) bool, resumedOf func(string) bool, settledOf func(string) bool, fingerprintOf func(string) (uint64, bool)) (out []string, newBaseline map[string]time.Time, newCleared map[string]ClearedMarker) {
	out = make([]string, 0, len(candidates))
	newBaseline = make(map[string]time.Time, len(candidates))
	newCleared = make(map[string]ClearedMarker, len(running))

	lastInput := func(id string) time.Time {
		if lastInputOf == nil {
			return time.Time{}
		}
		return lastInputOf(id)
	}
	fingerprint := func(id string) (uint64, bool) {
		if fingerprintOf == nil {
			return 0, false
		}
		return fingerprintOf(id)
	}

	// Carry forward a still-settled cleared marker for every RUNNING task,
	// independent of whether it is a candidate this tick — a stale re-flag can
	// arrive any number of ticks after the real clear that produced it.
	for _, id := range running {
		if archivedOf != nil && archivedOf(id) {
			continue
		}
		if marker, ok := prevCleared[id]; ok && !lastInput(id).After(marker.At) {
			newCleared[id] = marker
		}
	}

	for _, id := range candidates {
		if archivedOf != nil && archivedOf(id) {
			// Archived: drop from the set AND from the baseline/cleared maps
			// (so an un-archive later re-arms cleanly).
			continue
		}
		li := lastInput(id)
		if marker, ok := newCleared[id]; ok && !li.After(marker.At) {
			// Same timestamp as a real clear. BUG-067: only suppress if the
			// content also matches (or can't be compared) — a fingerprint
			// that provably DIFFERS means this is a distinct, still-unanswered
			// prompt (e.g. the next question in a multi-question brainstorm
			// flow), not a stale re-detection of the already-resolved one.
			curFP, curOK := fingerprint(id)
			if !marker.HasFP || !curOK || curFP == marker.FP {
				continue
			}
		}
		// Capture the baseline on first entry; freeze it across subsequent
		// ticks while the task stays a candidate.
		baseline, tracked := prevBaseline[id]
		if !tracked {
			baseline = li
		}
		// Input arrived after the flag was raised → the user responded →
		// clear, and remember the input timestamp (and content fingerprint,
		// when known) that cleared it so a later stale re-candidacy at this
		// same timestamp AND content is recognized too.
		if lastInputOf != nil && li.After(baseline) {
			fp, ok := fingerprint(id)
			newCleared[id] = ClearedMarker{At: li, FP: fp, HasFP: ok}
			continue
		}
		// The session has demonstrated sustained resumed activity since being
		// flagged — clear regardless of who delivered the input that unstuck
		// it (user keystroke or a coordinator's relayed answer). Marked with
		// the SAME cleared-marker mechanism as the user-input clear above, so
		// a later stale re-candidacy of the identical already-resolved tail
		// content is suppressed exactly like BUG-063 already guards.
		if resumedOf != nil && resumedOf(id) {
			fp, ok := fingerprint(id)
			newCleared[id] = ClearedMarker{At: li, FP: fp, HasFP: ok}
			continue
		}
		// The session has settled: genuinely idle with no current
		// needs-input signal, sustained for NeedsInputSettleTicks (BUG-072).
		// This is the complementary case to resumedOf above — a worker that
		// resolves its own block and wraps up FASTER than
		// NeedsInputResumeTicks never sustains that streak, and once idle it
		// can never resume it (an idle session never shows the working
		// affordance again), so without this path such a worker would stay
		// flagged until an unrelated, incidental keystroke arrived.
		if settledOf != nil && settledOf(id) {
			fp, ok := fingerprint(id)
			newCleared[id] = ClearedMarker{At: li, FP: fp, HasFP: ok}
			continue
		}
		newBaseline[id] = baseline
		out = append(out, id)
	}
	return out, newBaseline, newCleared
}

// ClearedMarker is the BUG-034/BUG-063/BUG-067 cleared-marker NeedsInputClear
// threads across ticks: the wall-clock lastInputOf value observed at the
// moment a needs-input flag cleared, plus — when known — the content
// fingerprint that was showing at (or just before) that moment. A later
// candidacy at the identical timestamp is only suppressed as a stale
// re-detection when the content ALSO matches; see NeedsInputClear's BUG-067
// doc comment. HasFP is false when no fingerprint could be determined at
// clear time (e.g. the screen had already moved on to a busy/narrating frame
// with no awaiting-input signal) — the suppression then falls back to the
// pre-BUG-067 timestamp-only comparison.
type ClearedMarker struct {
	At    time.Time
	FP    uint64
	HasFP bool
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
// A non-qualifying tick that follows an ONGOING streak is held in a one-tick
// GRACE PERIOD rather than discarding the streak outright (BUG-060): a
// negative return value encodes "streak of -newTicks, one miss pending
// confirmation". The very next tick either resumes the streak (qualifies
// again — the miss was a transient blip) or confirms a genuine break (misses
// again — two CONSECUTIVE non-qualifying ticks reset to zero for real). A
// streak that was already at zero (nothing accumulated, or grace already
// consumed) simply stays at zero on a miss.
//
// A grace tick's `escalated` return STAYS true when the streak had ALREADY
// reached NeedsInputEscalationTicks before the miss — a session that has
// already escalated must not visibly flicker back to "not flagged" for the
// one tick a blip is being confirmed or forgiven; it only actually
// de-escalates once the SECOND consecutive miss confirms a real break (which
// resets to zero, `escalated` false, same as any other confirmed break).
//
// This grace tolerance exists because Claude's own fullscreen redraw can
// produce an isolated tick where ParkedSelectionSignal misses even though the
// session is genuinely, continuously parked at the SAME prompt: the widget's
// cursor glyph can blink off for a single redraw frame, and
// readSessionLogTailBytes has no synchronization with the daemon's concurrent
// log-file writer, so an occasional read can land on a torn/partial frame
// mid-redraw. Under the OLD all-or-nothing reset, either of these — recurring
// roughly once every few ticks — could permanently prevent the streak from
// EVER reaching NeedsInputEscalationTicks, even though the session never
// stopped being parked: a hera worker whose blocked-prompt frame happened to
// hit this cadence would never surface "(?)", while a sibling whose ticks
// landed cleanly would. Two consecutive misses still reset for real — the
// anti-false-positive guarantee BUG-029 established is unchanged: a genuinely
// busy/streaming agent showing only sparse, ISOLATED coincidental matches
// (not a real parked streak) never accumulates escalation credit under this
// scheme either, because each such match is immediately followed by more
// misses that confirm the surrounding non-parked state.
//
// This is a pure step function; callers own the per-ID map (see
// ContentIdleState.esc and the TUI's needsInputEscalation field) exactly like
// the fingerprint/since carry-forward maps elsewhere in this file — the
// negative-sentinel encoding keeps the map value type (plain int) unchanged.
func EscalateParkedSelection(prevTicks int, qualifies bool) (newTicks int, escalated bool) {
	if qualifies {
		streak := prevTicks
		if streak < 0 {
			streak = -streak // resume the streak a prior isolated miss held in grace
		}
		newTicks = streak + 1
		return newTicks, newTicks >= NeedsInputEscalationTicks
	}
	if prevTicks > 0 {
		// First miss after a streak: hold it in grace rather than discarding —
		// confirmed or forgiven by the very next tick. Stay escalated through
		// this one grace tick if the streak had already crossed the threshold,
		// so an already-flagged session doesn't flicker off for a single blip.
		return -prevTicks, prevTicks >= NeedsInputEscalationTicks
	}
	// prevTicks <= 0: already at zero, or this is the SECOND consecutive miss
	// while already in grace — a genuine break, reset for real.
	return 0, false
}

// NeedsInputResumeTicks bounds how many CONSECUTIVE ticks a flagged session
// must show Claude's "working" affordance (the same needsInputWorkingRe
// discriminator ContentIdleFingerprint/BUG-035/036 use for "genuinely
// generating or executing a tool, not merely idling/animating") before its
// sticky needs-input flag is treated as resolved by demonstrated resumed
// activity — see NeedsInputClear's resumedOf parameter.
//
// A coordinator's reliable-notify delivery (WriteInputSystem) deliberately
// does not advance lastUserInput (BUG-034), so a relayed human answer can
// never clear the flag through the existing clear-on-input path even after
// the worker provably resumes real work — this is the gap this counter
// closes. No grace period is needed on a miss (unlike EscalateParkedSelection):
// the failure mode of clearing too SLOWLY (the flag lingers a few extra ticks
// after a genuine resume) is safe, while the failure mode this guards against
// — clearing a still-stuck agent — is not, so a single non-working tick resets
// the streak outright.
//
// Deliberately independent of NeedsInputEscalationTicks even though the same
// tick-count style applies: escalation bounds a false-negative (never
// flagging a genuinely parked session); this bounds how confidently "resumed"
// can be claimed before trusting it enough to clear an existing flag — a
// distinct, independently-tunable dial. Short enough that a genuinely
// resumed worker (running shell commands, ticking off task checkboxes —
// producing many seconds of continuous tool-call/generation activity) clears
// promptly; long enough that a brief single-utterance acknowledgment ("still
// blocked on X") from an agent that immediately re-parks at the same or a new
// prompt cannot cross the threshold before the awaiting-input shape
// reappears and the resumed pass's own working=false reading resets the
// streak.
const NeedsInputResumeTicks = 5

// ResumeActivityTick advances the per-session "sustained resumed activity"
// counter that backs the NeedsInputClear resumedOf clear path: prevTicks is
// the previous tick's count (0 if never tracked / first tick), workingNow is
// this tick's Claude "working" affordance reading (see
// ContentIdleFingerprint's working return value). Returns the new count and
// whether the session has now sustained it for NeedsInputResumeTicks
// consecutive ticks.
//
// This is a pure step function; callers own the per-ID map exactly like
// EscalateParkedSelection's callers own ContentIdleState.esc /
// App.needsInputEscalation — a fresh map each tick, rebuilt only from
// currently-running sessions, so a task that stops running is dropped, not
// leaked.
func ResumeActivityTick(prevTicks int, workingNow bool) (newTicks int, resumed bool) {
	if !workingNow {
		return 0, false
	}
	newTicks = prevTicks + 1
	return newTicks, newTicks >= NeedsInputResumeTicks
}

// SustainedActivityTick is a grace-tolerant sibling of ResumeActivityTick, used
// ONLY by the Hera rail's SustainedActive signal (narrow-needs-input-sustained-
// active) — NOT by NeedsInputClear's resumedOf (BUG-065) or
// autoClearBlockedHeraRoles' per-role blocked-status auto-clear, both of which
// keep calling ResumeActivityTick unchanged.
//
// ResumeActivityTick's zero-grace design is deliberately strict for BUG-065's
// coordinator-relay-answer clear path: clearing a still-genuinely-stuck agent is
// unsafe, so a single non-working tick resets the streak outright (see its own
// doc comment). But TestResumeActivityTick's "mostly-working... never converges
// either" case demonstrates that same strictness never lets a genuinely,
// substantially active agent reach the threshold at all when its content
// classification is bursty — an occasional single-tick miss amid mostly-working
// output (ordinary tool-call-to-tool-call pacing), rather than a session that is
// still genuinely parked — which is exactly the false-positive this signal
// exists to suppress (ground-truth repro: hera role contrib-classifier, active
// 7+ minutes / 30k+ tokens, its content classifier alternating "busy"/"blocked on
// user prompt" within seconds).
//
// Mirrors EscalateParkedSelection's BUG-060 one-tick grace exactly: a single
// ISOLATED miss holds the streak pending the next tick (encoded as a negative
// sentinel, matching EscalateParkedSelection's own encoding) rather than
// discarding it outright; a SECOND consecutive miss while already in grace is a
// genuine break and resets for real. Reuses NeedsInputResumeTicks as the
// threshold — no new dial.
func SustainedActivityTick(prevTicks int, workingNow bool) (newTicks int, sustained bool) {
	if workingNow {
		streak := prevTicks
		if streak < 0 {
			streak = -streak // resume the streak a prior isolated miss held in grace
		}
		newTicks = streak + 1
		return newTicks, newTicks >= NeedsInputResumeTicks
	}
	if prevTicks > 0 {
		// First miss after a streak: hold it in grace rather than discarding —
		// confirmed or forgiven by the very next tick.
		return -prevTicks, prevTicks >= NeedsInputResumeTicks
	}
	// prevTicks <= 0: already at zero, or this is the SECOND consecutive miss
	// while already in grace — a genuine break, reset for real.
	return 0, false
}

// NeedsInputSettleTicks bounds how many CONSECUTIVE ticks a flagged session
// must be genuinely RAW-IDLE (Session.IsIdle — no new PTY output, not merely
// "not currently generating") with NO current needs-input signal in its tail
// before NeedsInputClear's settledOf clear path (BUG-072) treats it as
// settled and clears the flag. See SettleTick.
//
// Deliberately much smaller than NeedsInputEscalationTicks (8) or
// NeedsInputResumeTicks (5): SettleTick is not guarding against the BUG-061
// tail-flooding hazard (that requires the session to keep producing bytes
// indefinitely, which raw-idle means has stopped) or a BUG-065-style brief
// acknowledgment burst (that risk only applies while the session is still
// actively producing). A session that has gone genuinely raw-idle cannot be
// mid-flood by construction, so a fresh read of its tail is trustworthy — two
// ticks purely guards against an isolated torn read, the same category
// BUG-060 named for the escalation counter's own grace period.
const NeedsInputSettleTicks = 2

// SettleTick advances the per-session "settled" counter backing
// NeedsInputClear's settledOf clear path (BUG-072) — the complementary case
// to ResumeActivityTick. ResumeActivityTick clears when the agent SUSTAINS
// visible work for NeedsInputResumeTicks consecutive ticks; it can never fire
// for a worker that wraps up in FEWER ticks and settles straight into idle,
// because going idle drives workingNow false and resets that streak to zero
// the instant work stops — and a genuinely idle session never shows the
// working affordance again, so the streak can never resume either. SettleTick
// recognizes exactly that case: a flagged session that goes genuinely idle
// (no new output) AND whose current tail no longer shows any of the three
// DetectNeedsInput signals — the same idle-gated check that raises the flag
// in the first place, re-applied here as a negative/clearing signal.
//
// prevTicks is the previous tick's count; idleNow is this tick's raw-idle
// reading; awaitingNow is whether the CURRENT tail still shows a blocking
// signal (agent.DetectNeedsInputScreen). Not idle, or still showing the
// signal, resets the streak to zero outright — no grace period, mirroring
// ResumeActivityTick: under-clearing (staying flagged a tick or two longer)
// is safe, a false clear is not. In particular, a session that is idle but
// STILL genuinely blocked (its tail still shows the signal) never qualifies —
// it is indistinguishable, by design, from the ordinary idle-gated
// re-detection case, and correctly stays flagged.
//
// Safe against the BUG-061 tail-flooding hazard that motivated removing the
// sticky pass's own re-match requirement: flooding is caused by Claude's
// continuous blinking-cursor redraw, which by construction keeps the session
// OUT of raw-idle (every redraw byte bumps lastOutput). A session that has
// gone genuinely raw-idle has, by definition, stopped producing those bytes,
// so it cannot be flooding its own tail — a fresh read is trustworthy.
func SettleTick(prevTicks int, idleNow, awaitingNow bool) (newTicks int, settled bool) {
	if !idleNow || awaitingNow {
		return 0, false
	}
	newTicks = prevTicks + 1
	return newTicks, newTicks >= NeedsInputSettleTicks
}

// ClearBlockedRoleStatus reports whether a hera role's self-reported
// hera_status ("blocked") should auto-clear back to "working". hera_status is
// a WHOLLY SEPARATE signal from the PTY-content needs-input flag
// NeedsInputClear governs — it is set only by an explicit hera_status tool
// call or a manual rail s/S step, and ORs into the rail's "(?)" glyph
// alongside that flag (RoleView.needsInputOwn) with no auto-clear of its own.
// A role that marked itself blocked (e.g. awaiting a check-in) and was then
// answered directly in its pane, with the agent replying conversationally
// rather than re-invoking hera_status, showed a stale "(?)" that nothing but a
// manual s/S step could ever clear — this is the fix.
//
// Two independent conditions, mirroring NeedsInputClear's own two-tier design
// for the separate PTY flag:
//
//  1. lastUserInput is strictly after blockedAt — the user replied directly in
//     the pane after the block was raised. Fires immediately, no threshold: a
//     genuine reply is a genuine reply however brief the agent's own response
//     afterward is (unlike the resumed-activity condition below, this one
//     cannot be defeated by a short acknowledgment).
//  2. resumed is true — the session has shown ResumeActivityTick's sustained
//     "working" streak since being flagged blocked. This is the ONLY signal
//     available when the block was resolved via a coordinator-relayed answer
//     (WriteInputSystem, which never advances LastUserInput — see
//     NeedsInputClear) rather than a direct keystroke.
//
// Unlike NeedsInputClear, this needs no BUG-063 stale-recandidacy guard:
// hera_status is an authoritative DB row, read fresh each check, not a fuzzy
// content match that can spuriously re-present an already-resolved signal —
// so a direct timestamp comparison is sufficient, no baseline/cleared-marker
// bookkeeping required.
func ClearBlockedRoleStatus(blockedAt, lastUserInput time.Time, resumed bool) bool {
	return lastUserInput.After(blockedAt) || resumed
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
		if newTicks != 0 {
			// A negative value is a BUG-060 one-tick grace state (an isolated
			// miss holding the streak pending the next tick), not "nothing to
			// store" — only a true zero (no streak, or a confirmed break) needs
			// no entry, since a missing map key already reads back as zero.
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

// blinkProbeWindow bounds how many trailing bytes degenerateSuffixStart scans
// to IDENTIFY a repeating cycle (cheap, fixed cost); once a period is found it
// walks the FULL buffer backward to find where the run actually starts.
const blinkProbeWindow = 4096

// blinkMaxPeriod is the longest single repeat cycle degenerateSuffixStart will
// recognize. Chosen comfortably above the two-frame (on/off) blink cycle
// observed in the wild (~130 bytes: a cursor-position + color-code + one-glyph
// redraw, alternating a single space and a single multi-byte UTF-8 glyph), so
// a real BUG-061 blink cycle is always found on the FIRST qualifying (small)
// period rather than requiring the full range.
const blinkMaxPeriod = 512

// blinkMinRepeats is the minimum number of consecutive repeats of a candidate
// period required before degenerateSuffixStart calls it a degenerate run
// rather than a coincidental short match. Low enough to catch a run within one
// tick of it starting, high enough that ordinary content (which occasionally
// repeats a byte or two by chance) never false-positives.
const blinkMinRepeats = 6

// degenerateSuffixStart finds where a long run — ending at the very end of
// buf — of some short byte sequence (length <= blinkMaxPeriod) repeating at
// least blinkMinRepeats times begins, or returns -1 if the tail isn't
// dominated by such a run.
//
// This is BUG-061's root cause: Claude Code renders a blinking cursor/status
// glyph (observed: a fixed ~130-byte cursor-reposition + color-code + single-
// glyph redraw, toggling a space and "⏺" at a fixed screen position) that
// NEVER STOPS, even while genuinely parked at a permission prompt with the
// "esc to interrupt" working-affordance correctly absent. A detector that
// scans only a fixed-size tail of raw bytes (needsInputTailWindow) can have
// that ENTIRE window consumed by this repeating redraw once enough real time
// passes — permanently, not intermittently, since the byte gap between "most
// recent bytes" and "the last real content" only grows. Confirmed via live
// repro: a session's on-disk log had "proceed" (the permission dialog text)
// sitting 37KB+ behind the current end of a 59KB file after ~4 minutes
// parked, while `DetectNeedsInputScreen`/`ParkedSelectionSignal` missed 100%
// of the time across a 9-second, 6-round sampling window — a deterministic
// loss, not the "occasional torn read" BUG-029/060 targeted. No escalation-
// counter retuning can fix this: escalation requires the raw per-tick signal
// to be true SOMETIMES; once flooded it is false ALWAYS.
func degenerateSuffixStart(buf []byte) int {
	n := len(buf)
	if n < blinkMinRepeats*2 {
		return -1
	}
	probeLen := n
	if probeLen > blinkProbeWindow {
		probeLen = blinkProbeWindow
	}
	maxP := blinkMaxPeriod
	if maxP > probeLen/blinkMinRepeats {
		maxP = probeLen / blinkMinRepeats
	}
	for p := 1; p <= maxP; p++ {
		repeatBytes := p * blinkMinRepeats
		qualifies := true
		for i := n - repeatBytes; i < n-p; i++ {
			if buf[i] != buf[i+p] {
				qualifies = false
				break
			}
		}
		if !qualifies {
			continue
		}
		// Found a qualifying period within the probe window — walk it backward
		// through the WHOLE buffer to find the true start of the run (which may
		// extend further back than the probe window itself, in which case the
		// caller should expand its read and try again).
		j := n - p - 1
		for j >= 0 && buf[j] == buf[j+p] {
			j--
		}
		return j + 1
	}
	return -1
}

// TrimToSubstantiveTail drops a trailing degenerate repeat run (see
// degenerateSuffixStart) from buf, so a detector sees the last GENUINE content
// instead of however many blink-redraw cycles happen to fit in the window.
// Returns buf unchanged when no qualifying run is found.
func TrimToSubstantiveTail(buf []byte) []byte {
	if end := degenerateSuffixStart(buf); end >= 0 {
		return buf[:end]
	}
	return buf
}

// NeedsInputMaxExpandBytes bounds how far back SubstantiveTail will expand its
// read in search of real content past a degenerate blink run. A hard ceiling
// so a session parked for a very long time (beyond what this budget's blink
// rate could fill) has a bounded, documented worst case rather than an
// unbounded per-tick disk/ring read.
const NeedsInputMaxExpandBytes = 2 * 1024 * 1024

// SubstantiveTail reads a session's recent output via readN — which returns
// the last n bytes from whatever source a caller has (an on-disk log file, an
// in-memory ring buffer) for a requested n — and, if the result is dominated
// by a trailing degenerate repeat run (BUG-061), asks for progressively more
// (doubling n) until real content surfaces, readN stops returning more data
// (source exhausted), or maxBytes is reached. Returns up to the last
// wantBytes of that real content.
//
// Falls back to the raw, untrimmed read when no qualifying run is ever found
// (the common case — most sessions are actively producing real content, so
// the degenerate-run check fails fast and this never expands) or when
// trimming leaves nothing at all (better than returning empty). A caller is
// therefore never worse off than the pre-fix flat read.
func SubstantiveTail(readN func(n int) []byte, wantBytes, maxBytes int) []byte {
	n := wantBytes
	var buf, trimmed []byte
	for {
		buf = readN(n)
		trimmed = TrimToSubstantiveTail(buf)
		if len(trimmed) >= wantBytes || len(buf) < n || n >= maxBytes {
			break
		}
		n *= 2
		if n > maxBytes {
			n = maxBytes
		}
	}
	if len(trimmed) == 0 {
		return buf
	}
	if len(trimmed) > wantBytes {
		trimmed = trimmed[len(trimmed)-wantBytes:]
	}
	return trimmed
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

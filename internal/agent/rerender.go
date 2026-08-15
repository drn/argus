package agent

// Rerender decisions are the outcome of ShouldKickRerender, the shared gating
// logic for the kick. The TUI's agent-view-entry path calls ShouldKickRerender
// directly; the API's resize handler (Server.maybeKickRerender) inlines the
// equivalent gates in the same order (margin → idle → BlockedOnPrompt) rather
// than calling this function, so it can interleave DB lookups and cache
// invalidation between them. Keep the two in sync: a new gate added here must
// be mirrored in the API handler (and vice versa).
//
// The kick mechanism — stop the live session, restart it with --session-id so
// the agent re-emits the entire conversation at the new PTY size — is the only
// known way to repair a session whose scrollback was committed at a different
// width than the current viewer. SIGWINCH alone re-flows live UI but cursor
// positioning codes baked into the ring buffer (e.g. \e[5;3H) keep the wrapping
// of historical bytes wrong. See context/knowledge/gotchas/pty-terminal.md.

// RerenderMargin is the minimum |panelCols - initCols| required to trigger a
// kick. Smaller deltas aren't worth the visual hiccup of a kill+resume.
//
// Tightened from 30→15 with defect 3 (live emulator rebuild reads up to 8MB
// from the on-disk log instead of the 256KB ring): a sub-margin width change
// no longer corrupts scrollback through history loss, but the live agent's
// own rewrap of its conversation summary still benefits from a kick at
// moderate deltas — 15 cols is the smallest delta that meaningfully shifts
// claude-code's status-bar layout.
const RerenderMargin = 15

// RerenderDecision is the outcome of ShouldKickRerender.
type RerenderDecision int

const (
	// RerenderSkip — nothing to do. Either the gate inputs are wrong (no
	// session ID, kick already in flight, unknown init width) or the width
	// delta is too small to matter.
	RerenderSkip RerenderDecision = iota
	// RerenderDeferBusy — predicate matches but the agent isn't idle. Don't
	// kill mid-tool-call. The caller should retry on the next opportunity
	// (e.g., next agent-view entry, next resize).
	RerenderDeferBusy
	// RerenderDeferPrompt — predicate matches and the agent is idle, but it's
	// blocked on a user prompt (AskUserQuestion overlay, permission request,
	// plan-mode confirm, or a trailing plain-text question). Stopping the
	// session would rehydrate the conversation via --session-id but NOT the
	// ephemeral prompt UI, silently dismissing the question the user came back
	// to answer. Defer until the user responds; the caller should invalidate
	// any cols cache so a later resize re-evaluates once the agent moves on.
	RerenderDeferPrompt
	// RerenderKick — stop the session. The exit handler is responsible for
	// resuming via --session-id at the new dimensions.
	RerenderKick
)

// MarginExceedsRerenderThreshold reports whether |panelCols - initCols| is
// large enough to justify a kick. Caller is responsible for the other gates
// (sessionID, alreadyPending, idle). Use this when those gates are checked
// separately from the predicate.
//
// initCols=0 means "unknown" (older daemon without InitialPTYSize support) and
// is treated as already-sane to avoid surprise restarts.
func MarginExceedsRerenderThreshold(initCols, panelCols int) bool {
	if initCols <= 0 {
		return false
	}
	delta := panelCols - initCols
	if delta < 0 {
		delta = -delta
	}
	return delta >= RerenderMargin
}

// ShouldKickRerender decides whether the live session should be stopped+resumed
// to re-render its scrollback at a different PTY width. Pure function for
// testability.
//
// Bidirectional: kicks when the panel is meaningfully wider OR meaningfully
// narrower than the session's initial cols. The session's `initialCols` reflects
// the width at which the agent last fully re-emitted its conversation (set on
// session start; updated implicitly when a kick succeeds and the next session
// starts at the new width). Width changes during the session (SIGWINCH) don't
// move it — they only affect live UI, leaving scrollback baked at the original
// width.
//
// committedCols is a SECOND, caller-tracked anchor (0 means "none tracked
// yet") covering a gap initCols can't: a mid-session bind at a width that
// crosses the margin but never resolves (deferred while busy/blocked, never
// goes idle at that width, then the pane moves on) leaves the session's real
// scrollback drifted to that width forever, while initCols stays fixed at
// session start. A LATER bind back near initCols (or matching some OTHER
// viewer's cached width) would otherwise read as "no drift" even though the
// content actually on screen is still committed at the abandoned width. The
// decision fires if EITHER anchor shows drift — this can only WIDEN which
// candidates trigger a kick, never narrow it, so it can't regress the
// existing initCols-only retry behavior for repeated same-width evaluations.
// See gotchas/pty-terminal.md (fix-committed-width-drift) and BUG-078 in
// gotchas/hera-view.md.
//
// needsInput gates the kick on whether the agent is blocked on a user prompt.
// An agent waiting on an AskUserQuestion overlay (or any selection/confirm UI)
// reads as idle, so without this gate a resize that crosses the margin would
// stop+restart it and dismiss the prompt the user returned to answer. The kick
// only repairs scrollback wrapping — never worth losing an in-flight question.
func ShouldKickRerender(hasSessionID bool, initCols, committedCols, panelCols int, idle, alreadyPending, needsInput bool) RerenderDecision {
	if !hasSessionID || alreadyPending {
		return RerenderSkip
	}
	exceeds := MarginExceedsRerenderThreshold(initCols, panelCols)
	if !exceeds && committedCols > 0 {
		exceeds = MarginExceedsRerenderThreshold(committedCols, panelCols)
	}
	if !exceeds {
		return RerenderSkip
	}
	if !idle {
		return RerenderDeferBusy
	}
	if needsInput {
		return RerenderDeferPrompt
	}
	return RerenderKick
}

package agent

// Rerender decisions are the outcome of ShouldKickRerender. Both the TUI's
// agent-view-entry path and the API's resize handler share this predicate so
// the gate is identical no matter who initiates a kick.
//
// The kick mechanism — stop the live session, restart it with --session-id so
// the agent re-emits the entire conversation at the new PTY size — is the only
// known way to repair a session whose scrollback was committed at a different
// width than the current viewer. SIGWINCH alone re-flows live UI but cursor
// positioning codes baked into the ring buffer (e.g. \e[5;3H) keep the wrapping
// of historical bytes wrong. See context/knowledge/gotchas/pty-terminal.md.

// RerenderMargin is the minimum |panelCols - initCols| required to trigger a
// kick. Smaller deltas aren't worth the visual hiccup of a kill+resume.
const RerenderMargin = 30

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
func ShouldKickRerender(hasSessionID bool, initCols, panelCols int, idle, alreadyPending bool) RerenderDecision {
	if !hasSessionID || alreadyPending {
		return RerenderSkip
	}
	if !MarginExceedsRerenderThreshold(initCols, panelCols) {
		return RerenderSkip
	}
	if !idle {
		return RerenderDeferBusy
	}
	return RerenderKick
}

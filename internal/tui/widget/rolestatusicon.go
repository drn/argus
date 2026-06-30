package widget

import (
	"github.com/drn/argus/internal/tui/theme"
	"github.com/gdamore/tcell/v2"
)

// RoleStatusInputs is the set of primitive signals that decide a Hera role's
// status glyph + style. It is the SINGLE source of truth shared by the rail's
// statusIcon (internal/tui/hera/rail.go) and the plan-view node projection
// (internal/tui/hera/plan.go), so the two surfaces can never drift (BUG-007).
// The caller derives these from its RoleView; this package stays hera-agnostic
// (it must not import hera, which would cycle).
type RoleStatusInputs struct {
	// ReadyToClose: bound task carries meta:hera.ready_to_close — a finished worker
	// awaiting close-out. Highest precedence.
	ReadyToClose bool
	// NeedsInput: the role's needs-input signal (own OR subtree rollup) — a worker
	// blocked on a user prompt. Ranks below ready_to_close, above done/active.
	NeedsInput bool
	// Failed: the hera role status is "failed" (D2, make-hera-plan-living) — a
	// worker that self-reported defeat. Ranks below NeedsInput, above Done. Renders
	// a red ✕ distinct from the Done ✓ (a failed task is not done).
	Failed bool
	// Done: the hera role status is "done".
	Done bool
	// Active: genuinely producing output (live binding + bound task in_progress) —
	// the honest "working" signal that animates the spinner (NOT the stale hera
	// role-status field). See RoleView.IsActive / BUG-003.
	Active bool
	// Idle: the hera role status is "idle" (live but quiet).
	Idle bool
	// Live: the role holds a live binding (no other status applied).
	Live bool
}

// RoleStatusIcon resolves a role's status glyph + style from its signals — the
// rail's exact vocabulary, reused so the plan view renders 1:1 with the rail
// (BUG-007). `frame` is the current spinner animation frame (the Active case
// animates via SpinnerFrame); `dim` forces the dimmed style for archived
// placement (the glyph never lies — only the style dims). Precedence:
// needs-input → ready_to_close → failed(red ✕) → done → active(spinner) → idle → live → default.
//
// needs-input outranks ready_to_close (BUG-A): a worker GENUINELY blocked on a
// user prompt is not "ready to close" — the done-roll's ready_to_close stamp is
// contradicted by an active block, so the actionable "(?)" must win and not be
// masked by the review glyph. needs-input is content-aware upstream, so a worker
// merely idling at its done summary (no interactive affordance) is never flagged
// and still renders the ready_to_close review glyph.
func RoleStatusIcon(in RoleStatusInputs, dim bool, frame int) (rune, tcell.Style) {
	var glyph rune
	var style tcell.Style
	switch {
	case in.NeedsInput:
		glyph, style = theme.IconNeedsInput, theme.StyleNeedsInput
	case in.ReadyToClose:
		glyph, style = theme.IconReview, tcell.StyleDefault.Foreground(theme.ColorComplete).Bold(true)
	case in.Failed:
		// D2 (make-hera-plan-living): explicit self-reported defeat — red ✕,
		// distinct from the Done ✓ (a failed task is not done, not ready to close).
		glyph, style = '✕', theme.StyleError
	case in.Done:
		glyph, style = '✓', theme.StyleComplete
	case in.Active:
		glyph, style = SpinnerFrame(frame), theme.StyleInProgress
	case in.Idle:
		glyph, style = theme.IconMoonOutline, theme.StyleInReview
	case in.Live:
		glyph, style = theme.IconMoonStars, theme.StyleInReview
	default:
		glyph, style = theme.IconMoonOutline, theme.StyleDimmed
	}
	if dim {
		style = theme.StyleDimmed
	}
	return glyph, style
}

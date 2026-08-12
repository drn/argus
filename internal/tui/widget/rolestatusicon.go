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
	// Accepted: bound task's TaskStatus is complete — coordinator-accepted via
	// hera_accept, or gater auto-accept on plan-DAG rollforward
	// (add-hera-accept-lifecycle). A coordinator-authoritative terminal signal
	// that supersedes the self-reported ladder (ReadyToClose/Failed/Done/Idle/
	// Live), but still ranks below needs-input (BUG-A) and active (BUG-F) — a
	// role genuinely blocked on input or still producing output shows that
	// first, even if its task was accepted out from under it.
	Accepted bool
	// ReadyToClose: bound task carries meta:hera.ready_to_close — a finished worker
	// awaiting close-out. Ranks below needs-input (BUG-A) and active (BUG-F),
	// above failed/done.
	ReadyToClose bool
	// NeedsInput: the role's needs-input signal (own OR subtree rollup) — a worker
	// blocked on a user prompt. Highest precedence: outranks ready_to_close (BUG-A)
	// and everything else — the one actionable thing in the subtree.
	NeedsInput bool
	// Failed: the hera role status is "failed" (D2, make-hera-plan-living) — a
	// worker that self-reported defeat. Ranks below active (BUG-F), above Done.
	// Renders a red ✕ distinct from the Done ✓ (a failed task is not done).
	Failed bool
	// Done: the hera role status is "done".
	Done bool
	// Active: genuinely producing output (Live && SessionRunning && !SessionIdle,
	// BUG-C) — the honest, content-derived "working" signal that animates the
	// spinner (NOT the stale hera role-status/meta). Ranks just below needs-input:
	// it OUTRANKS the stale-able resting states ready_to_close/failed/done (BUG-F).
	// See RoleView.IsActive / BUG-003 / BUG-C.
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
// needs-input → active(spinner) → accepted(bold ✓) → ready_to_close → failed(red ✕) → done → idle → live → default.
//
// needs-input outranks everything (BUG-A): a worker GENUINELY blocked on a user
// prompt is the one actionable thing in the subtree — it must never be masked.
//
// accepted (add-hera-accept-lifecycle) outranks the self-reported ladder
// ready_to_close/failed/done/idle/live — a coordinator's hera_accept (or the
// gater's auto-accept) is an authoritative decision, so it must read as
// visibly distinct from a merely self-reported ready_to_close. It renders a
// BOLD ✓ (theme.StyleComplete), distinct from both plain Done's ✓ (not bold)
// and ReadyToClose's bold clipboard-check icon — three separate glyphs for
// three separate states rather than reusing one across two of them.
//
// active outranks the stale-able resting states ready_to_close/failed/done
// (BUG-F, the icon-precedence completion of BUG-C). Active is the HONEST,
// content-derived "producing output right now" signal (Live && SessionRunning &&
// !SessionIdle) — NOT a stale hera role-status/meta. A worker genuinely producing
// output is working again, so the spinner is the truer current state and must not
// be masked by the done-roll's ready_to_close stamp (or a stale done/failed
// role-status). When the worker goes idle again, IsActive drops false (the
// SessionRunning/!SessionIdle gate) and the resting glyph correctly returns — so
// the resting case is preserved. needs-input stays highest (a worker blocked on
// the user is more urgent than one merely producing; the two are mutually
// exclusive in practice). needs-input is content-aware upstream, so a
// ready_to_close worker merely idling at its done summary (no interactive
// affordance, not active) still renders the review glyph.
func RoleStatusIcon(in RoleStatusInputs, dim bool, frame int) (rune, tcell.Style) {
	var glyph rune
	var style tcell.Style
	switch {
	case in.NeedsInput:
		glyph, style = theme.IconNeedsInput, theme.StyleNeedsInput
	case in.Active:
		// BUG-F: honest content-derived "producing output now" — outranks the
		// stale-able ready_to_close/failed/done stamps below. Drops to false the
		// moment the session idles/exits, so those resting glyphs then return.
		glyph, style = SpinnerFrame(frame), theme.StyleInProgress
	case in.Accepted:
		// add-hera-accept-lifecycle: bold checkmark, distinct from Done's plain
		// checkmark and ReadyToClose's bold clipboard-check icon.
		glyph, style = '✓', theme.StyleComplete.Bold(true)
	case in.ReadyToClose:
		glyph, style = theme.IconReview, tcell.StyleDefault.Foreground(theme.ColorComplete).Bold(true)
	case in.Failed:
		// D2 (make-hera-plan-living): explicit self-reported defeat — red ✕,
		// distinct from the Done ✓ (a failed task is not done, not ready to close).
		glyph, style = '✕', theme.StyleError
	case in.Done:
		glyph, style = '✓', theme.StyleComplete
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

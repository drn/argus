# Hera View

## MODIFIED Requirements

### Requirement: Active agents animate a spinner glyph (area 3)

The system SHALL render a genuinely-active role's status glyph as an animated spinner frame from the active spinner (`widget.SpinnerFrame`), advancing with the wall-clock frame counter, rather than a static glyph. A role is genuinely active (`RoleView.IsActive`) when it holds a live binding AND its bound argus task is `in_progress` AND its session is NOT content-idle — sourced from REAL session activity, NOT the hera role `working` status field. The hera role status is a manual/MCP-set ladder value that never reconciles down (it stays `working` after a session idles, stops, or dies), so it MUST NOT drive the spinner: a stale-`working` role whose binding is gone, dead, or no longer `in_progress` is static (BUG-003).

The content-idle gate fixes a fullscreen (alt-screen) agent parked at its prompt (BUG-036): such an agent repaints continuously, so it never reaches the raw-byte idle set and would otherwise animate the spinner forever even though it is doing nothing. When the App's content-idle signal (the animation-stripped emulated-screen stability classification) marks the role's bound session idle, the role is NOT active and renders a static idle/live glyph (or the needs-input glyph if it is at a prompt, which already outranks the spinner). A genuinely content-ACTIVE agent — emulated content changing tick-to-tick, or showing the "working" affordance — still spins. This mirrors the plugin's `stateGlyph`, which animates only on a known `in_progress` + running argus state.

An operator/agent-set `blocked` assertion takes precedence over the spinner (the needs-input glyph renders even while the task is still `in_progress`), as does `done` and `ready_to_close`. Non-active states (idle, content-idle, blocked, done, ready_to_close, unbound, stopped) remain static.

Derived from: `internal/tui/hera/rail.go` (`statusIcon`), `internal/tui/hera/model.go` (`RoleView.IsActive`, `RoleView.SessionIdle`), `internal/tui/widget/spinnerstate.go` (`SpinnerFrame`).

#### Scenario: Genuinely active role spins

- **WHEN** a role holds a live binding and its bound argus task is `in_progress`, its session is not content-idle, and it is not blocked/done/ready_to_close
- **THEN** its status glyph is the active spinner's frame for the current animation frame, and the glyph differs across frames

#### Scenario: Stale-working stopped role is static

- **WHEN** a role's hera status is `working` but it holds no live binding (a stopped/dead session)
- **THEN** its status glyph does not animate

#### Scenario: Live-but-not-in_progress role is static

- **WHEN** a role holds a live binding but its bound argus task has left `in_progress` (e.g. an auto-completed coordinator now `in_review`), even with a stale `working` hera status
- **THEN** its status glyph does not animate

#### Scenario: Content-idle fullscreen role is static

- **WHEN** a role holds a live binding and its bound argus task is `in_progress`, but the App marks its session content-idle (parked fullscreen agent, stable emulated screen, no "working" affordance)
- **THEN** its status glyph does not animate — it renders a static idle/live glyph (or the needs-input glyph if it is at a prompt)

#### Scenario: Blocked outranks activity

- **WHEN** a role's hera status is `blocked` and its bound argus task is still `in_progress`
- **THEN** its status glyph is the needs-input glyph, not the spinner

#### Scenario: Details coordinator label is honest about stale working

- **WHEN** the Details pane renders a coordinator whose hera status is `working` but which is not genuinely active
- **THEN** the label reads `live` (binding still alive) or `stopped` (binding gone), not `working`

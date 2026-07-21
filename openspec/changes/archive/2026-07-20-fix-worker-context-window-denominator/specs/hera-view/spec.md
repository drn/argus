## MODIFIED Requirements

### Requirement: Worker/freelance rail rows show a context-pressure indicator (area 3)

A live worker-kind or freelance-kind rail role row SHALL reserve a trailing 2-character slot — a
blank separator column followed by a single glyph column — regardless of the role's current
context percentage, so a row's name width never changes as it crosses a threshold. Coordinator
rows SHALL NOT reserve or render this slot; they carry the live-count badge in that position
instead (see the requirement above).

The glyph column SHALL render, based on the role's context percentage — `ContextSize` relative to
the project's configured `worker_context_window` for a worker or freelance role (see
`config-management`; NOT `coordinator_context_budget`, which is a coordinator-specific recycle-nudge
policy threshold, not a context window size), resolved locally — see the capability's Non-Goals for
remote mode:

- nothing, when the percentage is under 40
- a dot (`•`) in a pale-yellow style, when the percentage is 40 up to (not including) 65
- a dot (`•`) in a hot-orange style, when the percentage is 65 up to (not including) 90
- a `!` in a red, bold style, when the percentage is 90 or above

The indicator SHALL NOT render for a role that is not live, or that is archived, even if it carries
a stale non-zero context percentage from a prior session. When both the PR indicator (see "PR
indicator on rail role rows") and the context-pressure indicator apply to the same row, both SHALL
render without either overwriting the other — the role name SHALL truncate further to make room
for both reserved trailing regions.

Derived from: `internal/tui/hera/rail.go` (`drawRoleRow`, `contextIndicator`), `internal/tui/hera/model.go` (`RoleView.ContextSize`, `RoleView.ContextPercent`), `internal/tui/hera_tiering.go` (`resolveHeraTier`), `internal/tui/theme/theme.go` (`ColorContextWarm`/`ColorContextHot`/`ColorContextCritical`).

#### Scenario: No indicator under 40%

- **WHEN** a live worker role's context percentage is under 40
- **THEN** its row's trailing indicator slot renders no glyph (reserved but blank)

#### Scenario: Pale-yellow dot in the 40-65% band

- **WHEN** a live worker role's context percentage is 40 or more and less than 65
- **THEN** its row renders a `•` in the pale-yellow style

#### Scenario: Hot-orange dot in the 65-90% band

- **WHEN** a live worker role's context percentage is 65 or more and less than 90
- **THEN** its row renders a `•` in the hot-orange style

#### Scenario: Red bang at 90% and above

- **WHEN** a live worker or freelance role's context percentage is 90 or more
- **THEN** its row renders a `!` in the red, bold style

#### Scenario: Percentage is computed against the worker context window, not the coordinator budget

- **WHEN** a worker role's `ContextSize` is `400000` and the project's `worker_context_window` is
  the default `1000000`
- **THEN** its context percentage is `40`, not a value computed against `coordinator_context_budget`

#### Scenario: Coordinator rows never show the indicator

- **WHEN** a role row is a coordinator (rendered via the orchestrator header, or a worker-bridge row acting as one)
- **THEN** no context-pressure indicator is reserved or rendered on that row, regardless of its context percentage

#### Scenario: A dead or archived role shows no indicator regardless of stale context data

- **WHEN** a worker role is not live, or is archived, and still carries a non-zero `ContextSize` from a previous session
- **THEN** its row's indicator slot renders no glyph

#### Scenario: PR tag and context indicator compose on the same row

- **WHEN** a live worker role's bound task has an actionable PR state AND its context percentage is 90 or more
- **THEN** its row renders both the `PR` tag and the `!` indicator, with the role name truncated to make room for both, and neither overwrites the other

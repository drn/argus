# Hera View

## MODIFIED Requirements

### Requirement: PR indicator in the roster (area 6)

The system SHALL mark a roster task with a `PR` indicator when that task's
cached `task_meta` "pr" namespace `state` parses (via `model.ParsePRState`) to
an actionable review state — `awaiting-review`, `changes-requested`, or
`approved` (`model.PRState.IsActionable()`). A `merged-closed`, `draft`,
`unknown`, or empty/unparseable state SHALL NOT show the mark, even when the
namespace's `url` field is still populated (the poller retains `url` after a
PR merges or closes). The indicator is best-effort and read once per refresh
via `ListMetaByNamespace("pr")`; it is never fetched by the view. A `ready`
mark renders for a `ready_to_close` worker, and both marks may appear
together.

Derived from: `internal/tui/hera/details.go` (`roleMark`), `internal/tui/hera/page.go` (`doRefresh` reads "pr" namespace).

#### Scenario: PR mark from an actionable cached state

- **WHEN** a roster task's "pr" meta has `state: "awaiting-review"` (or `changes-requested` / `approved`)
- **THEN** its roster row shows a `PR` mark

#### Scenario: No mark for a merged or closed PR

- **WHEN** a roster task's "pr" meta has `state: "merged-closed"` and a non-empty `url`
- **THEN** its roster row shows no `PR` mark (a `ready` mark may still show if the worker is `ready_to_close`)

#### Scenario: No mark for draft, unknown, or empty state

- **WHEN** a roster task's "pr" meta has `state: "draft"`, `state: "unknown"`, or no `state` at all
- **THEN** its roster row shows no `PR` mark

### Requirement: PR indicator on rail role rows (area 3)

The system SHALL render a `PR` indicator on a managed (non-coordinator) rail
role row when that role's bound task's cached `task_meta` "pr" namespace
`state` parses to an actionable review state — `awaiting-review`,
`changes-requested`, or `approved` (`model.PRState.IsActionable()`), the same
predicate the Details roster and the TUI task list (`theme.PRGlyph`) use. A
`merged-closed`, `draft`, `unknown`, or empty/unparseable state SHALL NOT
render the indicator, even when `url` is still populated. The indicator is
best-effort, read once per refresh via `ListMetaByNamespace("pr")` and
threaded into the rail; it is never fetched by the view. It reuses the same
cached `prMeta` the Details roster reads.

Derived from: `internal/tui/hera/rail.go` (`rolePR`), `internal/tui/hera/page.go` (`doRefresh` reads "pr", passes `prMeta` to the rail).

#### Scenario: PR mark on a managed rail row with an actionable state

- **WHEN** a managed role's bound task has "pr" meta `state: "awaiting-review"` (or `changes-requested` / `approved`)
- **THEN** its rail row renders a `PR` indicator

#### Scenario: No indicator once the PR is merged or closed

- **WHEN** a managed role's bound task has "pr" meta `state: "merged-closed"` and a non-empty `url`
- **THEN** its rail row renders no `PR` indicator

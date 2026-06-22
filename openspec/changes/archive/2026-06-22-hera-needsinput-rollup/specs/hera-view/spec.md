# Hera View

## ADDED Requirements

### Requirement: Needs-input "(?)" propagates up the orchestration tree to the root (area rail)

The system SHALL surface the needs-input attention state of any role on ALL of
its ancestor coordinators, transitively up to the root coordinator. A
coordinator's rail status icon SHALL show the needs-input "(?)" indicator
(`theme.IconNeedsInput` / `theme.StyleNeedsInput`) when the coordinator role
ITSELF is in needs-input OR ANY descendant role in its orchestration subtree is
in needs-input. The descendant walk SHALL be transitive across nested and
BRIDGED sub-orchestrators (a sub-coordinator is a separate orchestrator bridged
in as a worker row) and SHALL be cycle-safe, reusing the same
`BridgeSubtree`/`bridgeIndex` traversal that drives rail nesting and the Ctrl+D
cascade. The indicator SHALL clear on an ancestor as soon as no descendant (and
not the ancestor itself) needs input.

The authoritative per-role needs-input signal SHALL be the SAME set the task
list consumes — the App's `needsInputIDs` (the idle-gated, sticky
`agent.DetectNeedsInput` PTY-tail scan) — threaded into `BuildModel`, plus the
role's own hera `blocked` status. No new needs-input detection SHALL be invented
for the rail. The rollup SHALL be computed in the MODEL (`BuildModel`) and
exposed as a `RoleView` field, so `statusIcon` stays a pure projection that only
reads it (no Draw-time I/O, no `screen.Sync()`).

Precedence: the needs-input rollup SHALL rank immediately below a role's OWN
`ready_to_close` mark and ABOVE the role's `done`, active-spinner, idle, and live
glyphs — so a descendant needing input surfaces on an ancestor even when the
ancestor is itself idle, working, or done. A role's own `ready_to_close`
(a distinct actionable check-off mark) SHALL still win on the role that carries
it.

Derived from: `internal/tui/hera/model.go` (`RoleView.NeedsInput`,
`RoleView.SubtreeNeedsInput`, `needsInputOwn`, `ShowsNeedsInput`, `BuildModel`
needs-input parameter, `rollupNeedsInput`, `orchSubtreeNeedsInput`),
`internal/tui/hera/rail.go` (`statusIcon` reads `ShowsNeedsInput`),
`internal/tui/hera/page.go` (`SetNeedsInput`, `doRefresh`),
`internal/tui/app.go` (push `needsInputIDs` to the Hera page each tick).

#### Scenario: A blocked worker bubbles "(?)" to its parent and the root coordinator

- **WHEN** a worker bound under a sub-coordinator enters needs-input and that sub-coordinator is bridged under a root coordinator
- **THEN** the worker row, the sub-coordinator's rail row, AND the root coordinator's header all render the needs-input "(?)" indicator

#### Scenario: The rollup clears when the descendant resolves

- **WHEN** the only needs-input descendant in a subtree is no longer in needs-input
- **THEN** the ancestor coordinators stop rendering "(?)" and revert to their own status glyph

#### Scenario: Propagation crosses multiple bridge levels

- **WHEN** a needs-input role sits two or more bridged sub-orchestrator levels below the root
- **THEN** every intervening sub-coordinator AND the root coordinator render "(?)"

#### Scenario: No false-positive without a needs-input descendant

- **WHEN** no role anywhere in a coordinator's subtree is in needs-input
- **THEN** that coordinator does NOT render "(?)" and shows its own status glyph

#### Scenario: The rollup is cycle-safe

- **WHEN** the bridge graph contains a cycle (A bridges B and B bridges A)
- **THEN** the rollup terminates and still reports needs-input for the reachable members

### Requirement: Needs-input "(?)" CLEARS and propagates up when a descendant resolves (area rail)

The needs-input "(?)" rollup SHALL clear on every ancestor coordinator,
transitively to the root, as soon as a descendant's needs-input resolves —
mirroring the SET propagation in reverse — on the next rail refresh. The system
SHALL recompute the rollup from the current model on each refresh (each app tick
while the Hera tab is active, and after each `s`/`S` status step), so a cleared
descendant clears its ancestors with no stale `SubtreeNeedsInput` carried between
builds.

Because the authoritative PTY needs-input scan (`App.needsInputIDs`) is STICKY —
it carries a task forward while the `agent.DetectNeedsInput` marker remains in
the session log tail and the session is still running — the system SHALL gate the
per-role PTY needs-input signal on the bound task being `in_progress`. A worker
whose task has finished (rolled to `in_review`/`complete`) SHALL NOT contribute
the PTY needs-input signal to the rollup even while its task remains in the
`needsInputIDs` set, so an ancestor coordinator's "(?)" clears as soon as the
descendant finishes. A task missing from the task snapshot (read failure) SHALL
be treated as not in_progress.

The role's own hera `blocked` status SHALL remain an INDEPENDENT, ungated
needs-input source (it is a deliberate "I'm blocked" assertion, honest even while
the task is in_progress); it SHALL clear by stepping the role off `blocked`
(`s`/`S`). The gate SHALL be hera-view-local: the task list's sticky needs-input
semantics are unchanged.

Derived from: `internal/tui/hera/model.go` (`buildRoleView` gates `RoleView.NeedsInput`
on `task.Status == in_progress`; `rollupNeedsInput` recomputed per `BuildModel`),
`internal/tui/heraactions.go` (`heraStatusStep` → `heraRefresh`),
`internal/tui/app.go` (`SetNeedsInput` + `ScheduleRefresh` each tick).

#### Scenario: A finished worker stops rolling up "(?)" even while still flagged

- **WHEN** a worker that was in needs-input finishes (its bound task rolls to in_review) but the App's needs-input set still flags the task because its final prompt lingers in the log tail
- **THEN** the worker's own row and every ancestor coordinator stop rendering "(?)" on the next refresh

#### Scenario: Stepping a descendant off `blocked` clears the ancestor rollup

- **WHEN** a deep worker's hera status is stepped off `blocked` (and it has no live PTY needs-input)
- **THEN** every intervening sub-coordinator AND the root coordinator stop rendering "(?)" on the next refresh

## MODIFIED Requirements

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

A live needs-input signal SHALL surface for a blocked role even when its bound
argus task is NO LONGER `in_progress`, for any role that does not "finish" by task
status while its session is alive — specifically a COORDINATOR (and freelance)
role. A coordinator routinely rolls its bound task to complete/in_review while its
session stays alive and keeps coordinating, and may itself block on a user prompt;
gating its needs-input on `in_progress` hid the "(?)" on its (usually collapsed)
header. The in_progress gate SHALL therefore apply ONLY to WORKER-kind roles (the
finished-worker clear, BUG-023): a worker that leaves `in_progress` is finished
and its lingering sticky marker SHALL NOT keep "(?)" pinned, whereas a live
non-worker role SHALL surface "(?)" regardless of task status. A non-worker role's
"finished" condition is its session exiting, which drops it from the sticky
needs-input set upstream, so there is no stale-marker hazard. The App's Hera-rail
needs-input feed SHALL admit a task that is `in_progress` OR bound to a hera
coordinator role (regardless of task status); admitting a non-in_progress
coordinator (a MANAGED task) SHALL NOT affect the unmanaged attention-summary
count (BUG-005), which stays `in_progress`-gated for unmanaged tasks.

When an orchestrator has NO coordinator role to carry the glyph (for example its
coordinator role was nuked, BUG-022 Tier-2), the orchestrator HEADER itself SHALL
surface the subtree needs-input rollup with the SAME `theme.IconNeedsInput` /
`theme.StyleNeedsInput` indicator, so a blocked worker is visible from the
default collapsed ("tidy summary") view without expanding — mirroring the task
list's project-folder aggregate, which always shows "(?)" for any blocked task.
The per-orchestrator rollup SHALL therefore be exposed on the `OrchView`
(`SubtreeNeedsInput`), not only on the coordinator role. When a coordinator role
IS present its status glyph already carries the rollup and the header SHALL NOT
double-render the indicator.

The authoritative per-role needs-input signal SHALL be the SAME set the task
list consumes — the App's `needsInputIDs` (the idle-gated, sticky
`agent.DetectNeedsInput` PTY-tail scan) — threaded into `BuildModel`, plus the
role's own hera `blocked` status. No new needs-input detection SHALL be invented
for the rail. The rollup SHALL be computed in the MODEL (`BuildModel`) and
exposed as a `RoleView` field (and an `OrchView` field for the header), so
`statusIcon` and `drawOrchRow` stay pure projections that only read it (no
Draw-time I/O, no `screen.Sync()`).

Precedence: the needs-input rollup SHALL rank immediately below a role's OWN
`ready_to_close` mark and ABOVE the role's `done`, active-spinner, idle, and live
glyphs — so a descendant needing input surfaces on an ancestor even when the
ancestor is itself idle, working, or done. A role's own `ready_to_close`
(a distinct actionable check-off mark) SHALL still win on the role that carries
it.

Derived from: `internal/tui/hera/model.go` (`RoleView.NeedsInput`,
`RoleView.SubtreeNeedsInput`, `OrchView.SubtreeNeedsInput`, `needsInputOwn`,
`ShowsNeedsInput`, `BuildModel` needs-input parameter, `rollupNeedsInput`,
`orchSubtreeNeedsInput`),
`internal/tui/hera/rail.go` (`statusIcon` reads `ShowsNeedsInput`; `drawOrchRow`
surfaces `OrchView.SubtreeNeedsInput` when no coordinator role is present),
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

- **WHEN** the orchestration subtree contains a bridge cycle (A bridges B and B bridges A)
- **THEN** the rollup terminates and still reports needs-input for the reachable members

#### Scenario: A coordinator-less orchestrator header surfaces a blocked worker

- **WHEN** a collapsed orchestrator has a blocked (needs-input) worker in its subtree but no coordinator role (e.g. the coordinator was nuked)
- **THEN** the orchestrator header renders the needs-input "(?)" indicator so the blocked worker is visible without expanding

#### Scenario: A coordinator-less header rollup clears when the worker finishes

- **WHEN** the only blocked worker under a coordinator-less orchestrator finishes (its bound task rolls to in_review) even though the App's sticky needs-input set still flags it
- **THEN** the orchestrator header stops rendering "(?)" on the next refresh

#### Scenario: A blocked coordinator surfaces "(?)" even when its task is complete

- **WHEN** a coordinator's bound task has rolled to complete/in_review but its session is alive and blocked on a user prompt (its task is in the needs-input set)
- **THEN** the coordinator's (collapsed) header renders the needs-input "(?)" indicator, instead of being hidden by the in_progress gate

#### Scenario: A finished worker stays cleared even when its task is complete

- **WHEN** a worker's bound task has rolled to complete/in_review (finished) but the sticky needs-input set still flags it
- **THEN** the worker's row and its ancestor rollup do NOT render "(?)" — the in_progress gate stays worker-only (BUG-023 preserved)

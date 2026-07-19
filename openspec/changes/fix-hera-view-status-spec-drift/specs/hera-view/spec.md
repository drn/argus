## MODIFIED Requirements

### Requirement: Status-icon precedence on role rows (area 3)

The system SHALL choose a role row's status glyph by this precedence: (1) `NeedsInput` — the role's own needs-input signal (a PTY prompt or a `blocked` hera role status) OR its subtree rollup — wins over EVERYTHING, including a role's own `ready_to_close` mark (BUG-A: a role genuinely blocked on a user prompt is the one actionable thing in the subtree, and must never be masked); otherwise (2) GENUINE activity (`RoleView.IsActive` — `Live && SessionRunning && !SessionIdle`, a session/content-derived signal, NOT gated on the bound argus task's status, BUG-C) renders the ACTIVE SPINNER's animated frame (see "Active agents animate a spinner glyph") — this outranks the stale-able resting states below it (BUG-F), because a role producing output again is more current than any of those stamps; otherwise (3) `ready_to_close` renders a distinct review glyph; otherwise (4) an operator/agent-set `failed` hera role status renders a distinct red `✕` (D2, `make-hera-plan-living`), never conflated with `done`; otherwise (5) a `done` hera role status renders its distinct static glyph; otherwise (6) an `idle` hera role status renders the static idle glyph; otherwise (7) binding presence (`Live`) renders a "live" glyph; otherwise (8) an unbound/dimmed glyph. The spinner is sourced from REAL session activity, never the stale `working` hera role status (BUG-003): a `working` role that is not genuinely active falls through to (7)/(8) and renders a static glyph. `ready_to_close` is read from the task-addressed `task_meta` "hera" namespace, not the hera tables.

Derived from: `internal/tui/widget/rolestatusicon.go` (`RoleStatusIcon`, `RoleStatusInputs`), `internal/tui/hera/rail.go` (`statusIcon`), `internal/tui/hera/model.go` (`RoleView.IsActive`, `RoleView.ShowsNeedsInput`, `buildRoleView` reads `ready_to_close`).

#### Scenario: Needs-input overrides ready_to_close and everything else

- **WHEN** a role shows its needs-input "(?)" signal (own or subtree rollup) AND also carries `meta:hera.ready_to_close=true`
- **THEN** the row renders the needs-input glyph, not the review/ready glyph

#### Scenario: ready_to_close overrides a stale working status

- **WHEN** a role's bound task carries `meta:hera.ready_to_close=true`, the role status is working, and the role is not genuinely active and not in needs-input
- **THEN** the row renders the review/ready glyph, not a spinner

#### Scenario: Genuine activity renders the animated spinner

- **WHEN** a role holds a live binding whose session is running and not content-idle, and it is not in needs-input
- **THEN** the row renders the active spinner's frame (animated), not a static glyph — regardless of the bound argus task's status

#### Scenario: Genuine activity outranks ready_to_close, failed, and done

- **WHEN** a role is genuinely active (per the previous scenario) AND also carries `ready_to_close`, `failed`, or `done`
- **THEN** the row renders the active spinner, not the resting glyph — the resting glyph returns once the role goes idle or the session ends (BUG-F)

#### Scenario: Stale working role-status does not animate

- **WHEN** a role's hera status is `working` but it is not genuinely active (no live binding, the session is not running, or the session is content-idle)
- **THEN** the row renders a static glyph (its resting state, live, or dimmed-unbound), not the spinner

#### Scenario: Failed renders a distinct glyph

- **WHEN** a role's hera status is `failed` and it is not in needs-input, not genuinely active, and not ready_to_close
- **THEN** the row renders a distinct red `✕`, never the `done` checkmark

#### Scenario: Blocked outranks activity

- **WHEN** a role has a status row of `blocked` (a needs-input source) and is not ready_to_close
- **THEN** the row renders the needs-input/blocked glyph (static), not the spinner

#### Scenario: Live-but-statusless role

- **WHEN** a role holds a live binding but has no status row and is not ready_to_close and not in needs-input
- **THEN** the row renders the in-review "live" glyph rather than the unbound glyph

### Requirement: Needs-input "(?)" propagates up the orchestration tree to the root (area rail)

The system SHALL surface the needs-input attention state of any role on ALL of
its ancestor coordinators, transitively up to the root coordinator. A
coordinator's rail status icon SHALL show the needs-input "(?)" indicator
(`theme.IconNeedsInput` / `theme.StyleNeedsInput`) when the coordinator role
ITSELF is in needs-input OR ANY descendant role in its orchestration subtree is
in needs-input. The descendant walk SHALL be transitive across nested and
BRIDGED sub-orchestrators (a sub-coordinator is a separate orchestrator bridged
in as a worker row) and SHALL be cycle-safe, using the same visited-set guard
and the same two descent mechanisms (`bridgeIndex` worker-bridging and
`coordBridgeChildren`) as the `BridgeSubtree` traversal that drives rail
nesting and the Ctrl+D cascade — but the rollup's OWN traversal, not
`BridgeSubtree` itself, because the rollup additionally prunes archived roles
(see below) while rendering and the cascade must NOT. The indicator SHALL
clear on an ancestor as soon as no descendant (and not the ancestor itself)
needs input.

An ARCHIVED role (`role.ArchivedAt` set, e.g. via the rail's `a` Tier-1 hide)
SHALL be excluded from the needs-input rollup counted toward its ancestors:
neither the archived role's own needs-input signal, NOR — when the archived
role is a bridging row into a nested sub-orchestrator — anything needing input
within that bridged subtree, SHALL propagate past the archived role to any
ancestor coordinator or coordinator-less orchestrator header. This applies
identically when the bridged child is reached through a worker-bridge (a
directly-archived bridging role) or when the whole child orchestrator is
itself archived (already excluded from `coordBridgeChildren`, and excluded
here for the worker-bridge path too). The exclusion applies ONLY to what
counts toward ANCESTORS — an archived role's OWN rail row SHALL continue to
show the needs-input "(?)" glyph on itself exactly as an unarchived role
would, since it is unaffected by whether IT counts toward something above it.

A live needs-input signal SHALL surface for a WORKER or COORDINATOR role,
regardless of the bound argus task's status, as long as the role's live
binding shows a current, content-aware needs-input signal (see "Needs-input
(?) CLEARS and propagates up" for the exact clearing mechanism). This holds
uniformly: a COORDINATOR routinely rolls its bound
task to complete/in_review while its session stays alive and keeps
coordinating, and may itself block on a user prompt (BUG-028); a WORKER MAY
likewise sit in `in_review` with its session still alive while the
coordinator closes it out (#707), and can genuinely ask a fresh question in
that state — it SHALL surface "(?)" then, the same as any other live role
(BUG-A). There is NO worker-specific `in_progress` gate. The earlier BUG-023
concern (a finished worker's stale marker pinning "(?)" forever) is instead
protected because a role's live binding ends the moment its session exits,
and the needs-input signal itself is content-aware rather than a stale
carry-forward (see "CLEARS" below) — so there is no stale-marker hazard once
the session is gone. The App's Hera-rail needs-input feed SHALL admit a task
that is `in_progress` OR bound to ANY hera role — coordinator OR worker —
regardless of task status; admitting a non-in_progress hera-managed task (a
MANAGED task, worker or coordinator) SHALL NOT affect the unmanaged
attention-summary count (BUG-005), which stays `in_progress`-gated for
unmanaged tasks.

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

Precedence: the needs-input rollup SHALL rank ABOVE every other role glyph,
including a role's own `ready_to_close` mark, the active spinner, `failed`,
`done`, `idle`, and `live` (BUG-A) — a descendant needing input surfaces on an
ancestor even when the ancestor is itself active, ready_to_close, idle,
working, or done, and a role's OWN needs-input signal masks its own
`ready_to_close`/active/resting glyph the same way (see "Status-icon
precedence on role rows"). Needs-input is content-aware upstream (see
"Needs-input (?) CLEARS and propagates up"), so this never falsely masks a
role merely idling at a stale done/ready_to_close summary.

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

#### Scenario: A coordinator-less header rollup clears when the worker's session resolves or exits

- **WHEN** the only needs-input worker under a coordinator-less orchestrator either resolves its prompt or its session exits — not merely because its bound task rolls to in_review
- **THEN** the orchestrator header stops rendering "(?)" on the next refresh

#### Scenario: A blocked coordinator surfaces "(?)" even when its task is complete

- **WHEN** a coordinator's bound task has rolled to complete/in_review but its session is alive and blocked on a user prompt (its task is in the needs-input set)
- **THEN** the coordinator's (collapsed) header renders the needs-input "(?)" indicator — task status is never a gate for a live role

#### Scenario: A worker whose session has exited stays cleared even though its task shows complete

- **WHEN** a worker's bound task has rolled to complete/in_review AND its session has exited (its live binding ended)
- **THEN** the worker's row and its ancestor rollup do NOT render "(?)" — a dead binding cannot contribute a needs-input signal (BUG-023 preserved)

#### Scenario: Archiving a blocked leaf worker stops it flagging its parent coordinator

- **GIVEN** a worker directly under a coordinator is in needs-input, and the coordinator currently renders "(?)"
- **WHEN** the user archives that worker's role (`a`)
- **THEN** the coordinator's rail row stops rendering "(?)" on the next refresh (assuming no other descendant needs input)

#### Scenario: Archiving a blocked leaf worker stops it flagging the root across multiple bridge levels

- **GIVEN** a leaf worker two or more bridge levels below the root is in needs-input, and every intervening sub-coordinator plus the root render "(?)"
- **WHEN** the user archives that leaf worker's role
- **THEN** every intervening sub-coordinator AND the root coordinator stop rendering "(?)" on the next refresh

#### Scenario: Archiving a nested sub-coordinator's bridging row hides its whole subtree from the parent

- **GIVEN** a nested sub-coordinator's bridging row (a role in the parent orchestrator with a structurally intact bridge into a child orchestrator) is NOT itself in needs-input, but a worker within its bridged child orchestrator IS
- **WHEN** the user archives the bridging row's role
- **THEN** the parent coordinator (and any further ancestor) stops rendering "(?)" on the next refresh, even though the blocked worker in the child orchestrator is still genuinely in needs-input

#### Scenario: Archiving a whole sub-orchestrator excludes it when reached via a worker-bridge

- **GIVEN** a child orchestrator reached from a live parent via a worker-bridge is itself archived (`archived_at` set on the orchestrator, not just a role within it) and contains a blocked worker
- **WHEN** the rollup is computed for the live parent
- **THEN** the parent does NOT render "(?)" on account of that archived child orchestrator's subtree

#### Scenario: An archived role's own row keeps showing its own needs-input glyph

- **GIVEN** a worker's role is archived while it is genuinely in needs-input
- **WHEN** the rollup is recomputed
- **THEN** the archived worker's OWN rail row still renders the needs-input "(?)" glyph on itself, even though it no longer counts toward any ancestor

### Requirement: Needs-input "(?)" CLEARS and propagates up when a descendant resolves (area rail)

The needs-input "(?)" rollup SHALL clear on every ancestor coordinator,
transitively to the root, as soon as a descendant's needs-input resolves —
mirroring the SET propagation in reverse — on the next rail refresh. The system
SHALL recompute the rollup from the current model on each refresh (each app tick
while the Hera tab is active, and after each `s`/`S` status step), so a cleared
descendant clears its ancestors with no stale `SubtreeNeedsInput` carried between
builds.

The authoritative PTY needs-input scan (`App.needsInputIDs`) SHALL be
content-aware, not a stale carry-forward: a task is a member of the set only
while it currently shows an unresolved `agent.DetectNeedsInput` signal, and it
clears once the signal resolves (the user provides input, the underlying
content changes, or the session is archived) — it does NOT linger on a
stale/already-answered prompt. The system SHALL gate a role's contribution to
the rollup on the role's LIVE BINDING, not the bound task's status: a live
role's needs-input persists for as long as its content-aware signal remains
current, even while its task has already rolled to `in_review`/`complete`
(BUG-A, #707) — task status alone is NOT a clearing condition. A role's
needs-input clears when either (a) the content-aware signal itself resolves,
or (b) the role's live binding ends (its session exits), whichever comes
first.

The role's own hera `blocked` status SHALL remain an INDEPENDENT, ungated
needs-input source (it is a deliberate "I'm blocked" assertion, honest
regardless of task status); it SHALL clear by stepping the role off `blocked`
(`s`/`S`). The gate SHALL be hera-view-local: the task list's sticky needs-input
semantics are unchanged.

Derived from: `internal/tui/hera/model.go` (`buildRoleView` surfaces
`RoleView.NeedsInput` for any live role currently in the App's content-aware
`needsInputIDs` set, independent of `task.Status`; `rollupNeedsInput`
recomputed per `BuildModel`), `internal/tui/heraactions.go` (`heraStatusStep`
→ `heraRefresh`), `internal/tui/app.go` (`SetNeedsInput` + `ScheduleRefresh`
each tick).

#### Scenario: A live worker's needs-input persists through in_review if the session is still asking

- **WHEN** a worker's task rolls to in_review while its session stays alive and is still genuinely at an unanswered prompt
- **THEN** the worker's row and every ancestor coordinator continue rendering "(?)" — the task-status transition alone does not clear it (BUG-A, #707)

#### Scenario: A worker's needs-input clears once the session resolves or exits

- **WHEN** a worker's session either receives the awaited input (its content-aware needs-input signal resolves) or exits entirely (ending its live binding)
- **THEN** the worker's own row and every ancestor coordinator stop rendering "(?)" on the next refresh

#### Scenario: Stepping a descendant off `blocked` clears the ancestor rollup

- **WHEN** a deep worker's hera status is stepped off `blocked` (and it has no live PTY needs-input)
- **THEN** every intervening sub-coordinator AND the root coordinator stop rendering "(?)" on the next refresh

### Requirement: Active agents animate a spinner glyph (area 3)

The system SHALL render a genuinely-active role's status glyph as an animated spinner frame from the active spinner (`widget.SpinnerFrame`), advancing with the wall-clock frame counter, rather than a static glyph. A role is genuinely active (`RoleView.IsActive`) when it holds a live binding AND its session is RUNNING AND NOT content-idle (`Live && SessionRunning && !SessionIdle`) — sourced from REAL session activity, NOT the hera role `working` status field, and NOT gated on the bound argus task's status (BUG-C). The hera role status is a manual/MCP-set ladder value that never reconciles down (it stays `working` after a session idles, stops, or dies), so it MUST NOT drive the spinner. A dead/stopped session is excluded via the `SessionRunning` gate (BUG-003) — a hera binding does NOT end when its session exits, so liveness alone cannot exclude a dead worker; `SessionRunning` does, since a dead session drops out of the App's running set.

The content-idle gate fixes a fullscreen (alt-screen) agent parked at its prompt (BUG-036): such an agent repaints continuously, so it never reaches the raw-byte idle set and would otherwise animate the spinner forever even though it is doing nothing. When the App's content-idle signal (the animation-stripped emulated-screen stability classification) marks the role's bound session idle, the role is NOT active and renders a static idle/live glyph (or the needs-input glyph if it is at a prompt, which already outranks the spinner). A genuinely content-ACTIVE agent — emulated content changing tick-to-tick, or showing the "working" affordance — still spins, REGARDLESS of its bound task's status, including a worker deliberately sitting in `in_review` with its session still alive and producing output (BUG-C, #707).

A role's own needs-input signal (including an operator/agent-set `blocked` assertion) takes precedence over the spinner regardless of the bound task's status (BUG-A). Genuine activity, however, now OUTRANKS the stale-able resting states below it: `ready_to_close`, `failed`, and `done` no longer take precedence over the spinner (BUG-F) — a role producing output again is more current than any of those stamps, and the resting glyph returns once the role goes idle or its session ends. Non-active states (idle, content-idle, needs-input/blocked, done, ready_to_close, failed, unbound, stopped) remain static.

Derived from: `internal/tui/widget/rolestatusicon.go` (`RoleStatusIcon`), `internal/tui/hera/rail.go` (`statusIcon`), `internal/tui/hera/model.go` (`RoleView.IsActive`, `RoleView.SessionRunning`, `RoleView.SessionIdle`), `internal/tui/widget/spinnerstate.go` (`SpinnerFrame`).

#### Scenario: Genuinely active role spins

- **WHEN** a role holds a live binding, its session is running and not content-idle, and it is not in needs-input
- **THEN** its status glyph is the active spinner's frame for the current animation frame, and the glyph differs across frames — regardless of its bound argus task's status

#### Scenario: Genuine activity outranks ready_to_close, failed, and done

- **WHEN** a role is genuinely active (live, running, not content-idle) AND also carries `ready_to_close`, `failed`, or `done`
- **THEN** its status glyph is the animated spinner, not the resting glyph (BUG-F)

#### Scenario: Stale-working stopped role is static

- **WHEN** a role's hera status is `working` but it holds no live binding, or its session is no longer running (a stopped/dead session)
- **THEN** its status glyph does not animate

#### Scenario: A live in_review role still spins if genuinely active

- **WHEN** a role holds a live binding whose bound argus task has left `in_progress` (e.g. an auto-completed coordinator or worker now `in_review`), its session is running, and it is content-active (not content-idle)
- **THEN** its status glyph still animates — the task-status transition alone does not stop the spinner (BUG-C, #707)

#### Scenario: Content-idle fullscreen role is static

- **WHEN** a role holds a live binding and a running session, but the App marks its session content-idle (parked fullscreen agent, stable emulated screen, no "working" affordance)
- **THEN** its status glyph does not animate — it renders a static idle/live glyph (or the needs-input glyph if it is at a prompt)

#### Scenario: Blocked outranks activity

- **WHEN** a role's hera status is `blocked` (or it is otherwise in needs-input)
- **THEN** its status glyph is the needs-input glyph, not the spinner

#### Scenario: Details coordinator label is honest about stale working

- **WHEN** the Details pane renders a coordinator whose hera status is `working` but which is not genuinely active
- **THEN** the label reads `live` (binding still alive) or `stopped` (binding gone), not `working`

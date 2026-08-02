## MODIFIED Requirements

### Requirement: Status-icon precedence on role rows (area 3)

The system SHALL choose a role row's status glyph by this precedence: (1) `NeedsInput` — the role's OWN needs-input signal (a PTY prompt or a `blocked` hera role status), UNLESS the role is currently `SustainedActive` (see "Needs-input '(?)' reflects only a role's own signal on every surface") — wins over EVERYTHING, including a role's own `ready_to_close` mark (BUG-A: a role genuinely blocked on a user prompt is the one actionable thing in the subtree, and must never be masked); otherwise (2) GENUINE activity (`RoleView.IsActive` — `Live && SessionRunning && !SessionIdle`, a session/content-derived signal, NOT gated on the bound argus task's status, BUG-C) renders the ACTIVE SPINNER's animated frame (see "Active agents animate a spinner glyph") — this outranks the stale-able resting states below it (BUG-F), because a role producing output again is more current than any of those stamps; otherwise (3) `ready_to_close` renders a distinct review glyph; otherwise (4) an operator/agent-set `failed` hera role status renders a distinct red `✕` (D2, `make-hera-plan-living`), never conflated with `done`; otherwise (5) a `done` hera role status renders its distinct static glyph; otherwise (6) an `idle` hera role status renders the static idle glyph; otherwise (7) binding presence (`Live`) renders a "live" glyph; otherwise (8) an unbound/dimmed glyph. The spinner is sourced from REAL session activity, never the stale `working` hera role status (BUG-003): a `working` role that is not genuinely active falls through to (7)/(8) and renders a static glyph. `ready_to_close` is read from the task-addressed `task_meta` "hera" namespace, not the hera tables.

`SustainedActive` is a STRICTER, debounced form of activity than `IsActive` (multiple consecutive ticks of demonstrated "working" content, not a single instantaneous reading — see "Needs-input '(?)' reflects only a role's own signal on every surface"). A role that is merely `IsActive` (one tick of genuine activity) but NOT YET `SustainedActive` still has its needs-input signal win per (1) unchanged — this narrowing applies ONLY once activity has been sustained long enough to be trusted, not on the first tick of renewed output.

This precedence applies identically to a coordinator-shaped row (an orchestrator header, or a bridging worker row that is itself a nested sub-coordinator): its `NeedsInput` term is its OWN signal only, never a descendant's rollup — see "Needs-input '(?)' reflects only a role's own signal on every surface."

Derived from: `internal/tui/widget/rolestatusicon.go` (`RoleStatusIcon`, `RoleStatusInputs`), `internal/tui/hera/rail.go` (`statusIcon`), `internal/tui/hera/model.go` (`RoleView.IsActive`, `RoleView.SustainedActive`, `RoleView.ShowsNeedsInput`, `buildRoleView` reads `ready_to_close`).

#### Scenario: Needs-input overrides ready_to_close and everything else

- **WHEN** a role shows its own needs-input "(?)" signal AND also carries `meta:hera.ready_to_close=true`, and the role is not `SustainedActive`
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

#### Scenario: Blocked outranks activity, but not sustained activity

- **WHEN** a role has a status row of `blocked` (a needs-input source) and is not ready_to_close, and the role is genuinely active (`IsActive`) but NOT `SustainedActive`
- **THEN** the row renders the needs-input/blocked glyph (static), not the spinner

#### Scenario: Sustained activity suppresses a blocked or content-flagged needs-input signal

- **WHEN** a role is `SustainedActive` (several consecutive ticks of demonstrated working content), regardless of whether its `NeedsInput` content flag or its self-reported `blocked` hera status is also set
- **THEN** the row renders the active spinner, not the needs-input glyph

#### Scenario: Live-but-statusless role

- **WHEN** a role holds a live binding but has no status row and is not ready_to_close and not in needs-input
- **THEN** the row renders the in-review "live" glyph rather than the unbound glyph

#### Scenario: A coordinator's own status glyph is unaffected by a blocked descendant

- **WHEN** a coordinator role is idle/working/done and is NOT itself in needs-input, but some descendant role in its orchestration subtree IS
- **THEN** the coordinator's row renders its own status glyph (idle/working/done), never the needs-input glyph

### Requirement: Active agents animate a spinner glyph (area 3)

The system SHALL render a genuinely-active role's status glyph as an animated spinner frame from the active spinner (`widget.SpinnerFrame`), advancing with the wall-clock frame counter, rather than a static glyph. A role is genuinely active (`RoleView.IsActive`) when it holds a live binding AND its session is RUNNING AND NOT content-idle (`Live && SessionRunning && !SessionIdle`) — sourced from REAL session activity, NOT the hera role `working` status field, and NOT gated on the bound argus task's status (BUG-C). The hera role status is a manual/MCP-set ladder value that never reconciles down (it stays `working` after a session idles, stops, or dies), so it MUST NOT drive the spinner. A dead/stopped session is excluded via the `SessionRunning` gate (BUG-003) — a hera binding does NOT end when its session exits, so liveness alone cannot exclude a dead worker; `SessionRunning` does, since a dead session drops out of the App's running set.

The content-idle gate fixes a fullscreen (alt-screen) agent parked at its prompt (BUG-036): such an agent repaints continuously, so it never reaches the raw-byte idle set and would otherwise animate the spinner forever even though it is doing nothing. When the App's content-idle signal (the animation-stripped emulated-screen stability classification) marks the role's bound session idle, the role is NOT active and renders a static idle/live glyph (or the needs-input glyph if it is at a prompt, which already outranks the spinner). A genuinely content-ACTIVE agent — emulated content changing tick-to-tick, or showing the "working" affordance — still spins, REGARDLESS of its bound task's status, including a worker deliberately sitting in `in_review` with its session still alive and producing output (BUG-C, #707).

A role's own needs-input signal (including an operator/agent-set `blocked` assertion) takes precedence over the spinner regardless of the bound task's status (BUG-A) — UNLESS the role has demonstrated `SustainedActive` (several consecutive ticks of the working affordance, debounced via `agent.ResumeActivityTick`; see "Needs-input '(?)' reflects only a role's own signal on every surface"), in which case the spinner wins and needs-input is suppressed. A merely-`IsActive` role (one tick, not yet sustained) does not suppress needs-input — BUG-A's original precedence is preserved until activity is sustained long enough to be trusted. Genuine activity, meanwhile, still OUTRANKS the stale-able resting states below it: `ready_to_close`, `failed`, and `done` no longer take precedence over the spinner (BUG-F) — a role producing output again is more current than any of those stamps, and the resting glyph returns once the role goes idle or its session ends. Non-active states (idle, content-idle, needs-input/blocked without sustained activity, done, ready_to_close, failed, unbound, stopped) remain static.

Derived from: `internal/tui/widget/rolestatusicon.go` (`RoleStatusIcon`), `internal/tui/hera/rail.go` (`statusIcon`), `internal/tui/hera/model.go` (`RoleView.IsActive`, `RoleView.SustainedActive`, `RoleView.SessionRunning`, `RoleView.SessionIdle`), `internal/tui/widget/spinnerstate.go` (`SpinnerFrame`).

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

#### Scenario: Blocked outranks activity until activity is sustained

- **WHEN** a role's hera status is `blocked` (or it is otherwise in needs-input), and the role is not `SustainedActive`
- **THEN** its status glyph is the needs-input glyph, not the spinner

#### Scenario: Sustained activity overrides a blocked or content-flagged needs-input signal

- **WHEN** a role's hera status is `blocked` (or it otherwise carries a needs-input content flag), but the role has demonstrated `SustainedActive` (several consecutive ticks of the working affordance)
- **THEN** its status glyph is the animated spinner, not the needs-input glyph

#### Scenario: Details coordinator label is honest about stale working

- **WHEN** the Details pane renders a coordinator whose hera status is `working` but which is not genuinely active
- **THEN** the label reads `live` (binding still alive) or `stopped` (binding gone), not `working`

### Requirement: Needs-input "(?)" reflects only a role's own signal on every surface (area rail)

The system SHALL derive the needs-input "(?)" indicator on EVERY coordinator-shaped render surface — the rail's collapsed orchestrator header, a bridging worker row that is itself a nested sub-coordinator, the Details pane's `coordinator:` status line, a Details roster row, and a plan-DAG node icon — EXCLUSIVELY from that role's OWN needs-input signal (`RoleView.needsInputOwn()`), computed identically to how a plain leaf role's own glyph is derived. A descendant role's needs-input state — however deep, and across however many bridged sub-orchestrator levels — SHALL NOT cause any ancestor's own icon to render the needs-input indicator. This holds uniformly across all five surfaces because they share one classifier (`roleStatusInputs`/`widget.RoleStatusIcon`, reading `RoleView.ShowsNeedsInput()`, which returns `needsInputOwn()` alone) — not five independent implementations that could drift.

`needsInputOwn()` is now gated on a THIRD, suppressing signal: `RoleView.SustainedActive` — whether the role's bound argus task has demonstrated several CONSECUTIVE ticks of genuine working activity (reusing `agent.ResumeActivityTick`'s existing debounced tick-counter machinery, the same one BUG-065's coordinator-relay-answer clear path already relies on — no new threshold). When `SustainedActive` is true, `needsInputOwn()` returns `false` UNCONDITIONALLY — regardless of the task's own workflow status (`in_progress`/`in_review`/`ready_to_close`) and regardless of whether the OR'd content-scan flag (`NeedsInput`) or the self-reported `blocked` hera ladder status is also set. This is an intentional narrowing of the prior "admits any hera-managed role, regardless of task status" invariant (BUG-A): `(?)` is meant to mean "genuinely stuck, no path forward without a human" — a role sustained-active is, by construction, not stuck.

`SustainedActive` is computed ONCE PER ARGUS TASK (not per role or per binding) from the same signal already used to clear the content-scan flag via `agent.NeedsInputClear`'s `resumedOf`, threaded through `HeraPage.SetSustainedActive` → `BuildModel` → `buildRoleView` exactly like `NeedsInput`/`SessionIdle`/`SessionRunning` already are. Because it is task-scoped rather than role-scoped, TWO roles bound to the SAME live argus task (a dual-bound sub-coordinator's parent-orchestrator worker hat and its own child-orchestrator coordinator hat — see `MaterializeHeraSubCoordinator`) automatically read the IDENTICAL `SustainedActive` value: a self-reported `blocked` hera status stranded on one hat is suppressed the moment the SHARED underlying session demonstrates sustained activity, regardless of which hat's binding is being rendered. No per-hat "check the other binding" logic exists or is needed.

An orchestrator with NO coordinator role (its coordinator role was deleted/nuked) SHALL NOT surface any needs-input indicator on its header, in any state — there being no "own" signal for such a header to derive from, the fallback that once rendered the rollup directly on this header is removed outright, not narrowed.

The needs-input ROLLUP COMPUTATION itself (`RoleView.SubtreeNeedsInput`, `OrchView.SubtreeNeedsInput`, populated by `rollupNeedsInput`/`orchSubtreeNeedsInput`) is UNCHANGED by this requirement: it remains transitive across bridged sub-orchestrators, cycle-safe, and excludes archived roles from counting toward an ancestor, exactly as before — it is computed FROM each role's (now sustained-active-gated) own signal, so a sustained-active role's suppressed own-signal also does not contribute to the rollup, but the rollup mechanism and its traversal are otherwise untouched. Its role is narrowed, not removed — it exists solely to gate the partial-fold-reveal mechanism (deciding which specific closed-fold descendant rows to peek through), never to drive any icon's display directly anymore. A genuinely blocked (non-sustained-active) descendant therefore remains fully visible — as its OWN row, peeked through any number of closed ancestor folds — via "Rail reveals the ancestor path to a hidden needs-input descendant through closed folds," which this requirement does not change and does not duplicate.

Derived from: `internal/tui/hera/model.go` (`RoleView.ShowsNeedsInput` returns `needsInputOwn()` alone; `RoleView.SustainedActive`; `RoleView.SubtreeNeedsInput`, `OrchView.SubtreeNeedsInput`, `rollupNeedsInput`, `orchSubtreeNeedsInput` unchanged traversal), `internal/tui/app.go` (the per-task sustained-active set exposed from `detectNeedsInputSticky`'s existing `agent.ResumeActivityTick` pass), `internal/tui/hera/page.go` (`HeraPage.SetSustainedActive`), `internal/tui/hera/rail.go` (`statusIcon`/`roleStatusInputs` read `ShowsNeedsInput`; `drawOrchRow`'s coordinator-less fallback branch removed; the reveal gates in `appendOrch`/`appendOrchWorkers`/`appendOrchRevealPath`/`appendWorkerRow`/`appendPinnedRole` unchanged), `internal/tui/hera/details.go` (`coordinator:` status line and `rosterStatusText`/`drawRosterRow`, both reading the same shared classifier, unchanged code), `internal/tui/hera/plan.go` (`planNodeIcon`, unchanged code, reads the same shared classifier).

#### Scenario: A blocked worker's own row shows "(?)"; its ancestor coordinators do not

- **WHEN** a worker two or more bridged sub-orchestrator levels below the root is blocked on a prompt, and no coordinator in the chain is itself blocked
- **THEN** the worker's own row renders "(?)", and every intervening sub-coordinator's row and the root coordinator's header render their OWN status glyphs, never "(?)"

#### Scenario: A coordinator-less orchestrator header never shows "(?)", even with a blocked descendant

- **WHEN** a collapsed orchestrator has a blocked (needs-input) worker in its subtree but no coordinator role (e.g. the coordinator was nuked)
- **THEN** the orchestrator header renders no needs-input indicator, regardless of the descendant's state

#### Scenario: A blocked descendant remains reachable via the closed-fold reveal despite no header glyph

- **WHEN** a coordinator's fold is collapsed and a descendant several levels down is blocked, and the coordinator itself is not
- **THEN** the coordinator's header shows its own status glyph (not "(?)"), while the specific blocked descendant's row is still rendered, peeked through the closed fold, exactly as the reveal mechanism already provides

#### Scenario: A coordinator's own needs-input signal still surfaces on its header regardless of descendants

- **WHEN** a coordinator role is itself blocked on a prompt (own signal) and is NOT `SustainedActive`, independent of whatever state its descendants are in
- **THEN** the coordinator's header renders the needs-input "(?)" indicator, exactly as any other role's own signal would

#### Scenario: The Details status line and roster follow the same own-signal-only rule

- **WHEN** the Details pane is showing a coordinator whose own signal is clear but which has a blocked descendant, and its roster includes a bridging worker row that is itself a nested sub-coordinator with a blocked descendant but no own signal
- **THEN** neither the `coordinator:` status line nor that roster row renders the needs-input glyph or the `"needs-input"` text label

#### Scenario: A sustained-active role never shows "(?)", regardless of task status or a stale blocked flag

- **WHEN** a role's bound argus task is `SustainedActive` (several consecutive ticks of demonstrated working content), AND the role's bound task carries `in_review`/`meta:hera.ready_to_close=true`, AND/OR the role's own hera status is a stale self-reported `blocked` value
- **THEN** the role's row renders no needs-input "(?)" indicator on any of the five coordinator-shaped surfaces — the active spinner (or another lower-precedence glyph) renders instead

#### Scenario: A dual-bound sub-coordinator's stale blocked hat is suppressed by the other hat's sustained activity

- **WHEN** an argus task holds two live hera bindings (a parent-orchestrator worker-kind role and a child-orchestrator coordinator-kind role, per `MaterializeHeraSubCoordinator`), one of those roles carries a stale self-reported `blocked` hera status, and the SHARED underlying session is `SustainedActive`
- **THEN** NEITHER role's row renders the needs-input "(?)" indicator — the shared per-task `SustainedActive` signal suppresses both, without any code needing to look up the other role's binding

#### Scenario: A genuinely idle or unresolved-blocked role with no subsequent activity still shows "(?)"

- **WHEN** a role's own needs-input signal is set (content flag or self-reported `blocked`) and the role's bound task has NOT demonstrated `SustainedActive` since
- **THEN** the role's row renders the needs-input "(?)" indicator exactly as before this change — no regression on genuine blocking cases

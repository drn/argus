## MODIFIED Requirements

### Requirement: Status-icon precedence on role rows (area 3)

The system SHALL choose a role row's status glyph by this precedence: (1) `NeedsInput` — the role's OWN needs-input signal (a PTY prompt or a `blocked` hera role status) — wins over EVERYTHING, including a role's own `ready_to_close` mark (BUG-A: a role genuinely blocked on a user prompt is the one actionable thing in the subtree, and must never be masked); otherwise (2) GENUINE activity (`RoleView.IsActive` — `Live && SessionRunning && !SessionIdle`, a session/content-derived signal, NOT gated on the bound argus task's status, BUG-C) renders the ACTIVE SPINNER's animated frame (see "Active agents animate a spinner glyph") — this outranks the stale-able resting states below it (BUG-F), because a role producing output again is more current than any of those stamps; otherwise (3) `ready_to_close` renders a distinct review glyph; otherwise (4) an operator/agent-set `failed` hera role status renders a distinct red `✕` (D2, `make-hera-plan-living`), never conflated with `done`; otherwise (5) a `done` hera role status renders its distinct static glyph; otherwise (6) an `idle` hera role status renders the static idle glyph; otherwise (7) binding presence (`Live`) renders a "live" glyph; otherwise (8) an unbound/dimmed glyph. The spinner is sourced from REAL session activity, never the stale `working` hera role status (BUG-003): a `working` role that is not genuinely active falls through to (7)/(8) and renders a static glyph. `ready_to_close` is read from the task-addressed `task_meta` "hera" namespace, not the hera tables.

This precedence applies identically to a coordinator-shaped row (an orchestrator header, or a bridging worker row that is itself a nested sub-coordinator): its `NeedsInput` term is its OWN signal only, never a descendant's rollup — see "Needs-input '(?)' reflects only a role's own signal on every surface."

Derived from: `internal/tui/widget/rolestatusicon.go` (`RoleStatusIcon`, `RoleStatusInputs`), `internal/tui/hera/rail.go` (`statusIcon`), `internal/tui/hera/model.go` (`RoleView.IsActive`, `RoleView.ShowsNeedsInput`, `buildRoleView` reads `ready_to_close`).

#### Scenario: Needs-input overrides ready_to_close and everything else

- **WHEN** a role shows its own needs-input "(?)" signal AND also carries `meta:hera.ready_to_close=true`
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

#### Scenario: A coordinator's own status glyph is unaffected by a blocked descendant

- **WHEN** a coordinator role is idle/working/done and is NOT itself in needs-input, but some descendant role in its orchestration subtree IS
- **THEN** the coordinator's row renders its own status glyph (idle/working/done), never the needs-input glyph

### Requirement: Live plan node icons are 1:1 with the rail (area 6)

A LIVE plan node's status icon (glyph AND style, including the animated spinner for a genuinely-active node) SHALL be identical to what the rail's status icon renders for the same role, computed through a SINGLE shared classifier so the two surfaces can never drift — not a parallel glyph table. The shared vocabulary: ready-to-close → review clipboard; needs-input → the needs-input glyph (so a worker blocked on a prompt is actionable from the DAG); done → `✓`; genuinely-active → the animated spinner (the plan view recomputes the frame at draw so it animates in lockstep); idle → moon-outline; live-quiet → moon-stars. Two plan-view-specific overlays the rail has no concept of: a PLANNED (never-bound) node renders the `○` circle, and a FAILED node (bound task result reports failure) renders `✕`. The header Status line uses the same resolved icon. The animated-spinner re-resolution applies ONLY when the shared classifier actually resolved to the spinner; a higher-precedence signal (notably needs-input on a genuinely-active role) resolves to its STATIC glyph and the node SHALL NOT animate, so it renders 1:1 with the rail's `?` rather than swapping in the spinner frame.

#### Scenario: A live node's icon equals the rail's

- **WHEN** a live worker role is in any status (done / working / idle / in-review / needs-input)
- **THEN** its plan node renders the same glyph and style the rail's status icon renders for that role, and a working node animates

#### Scenario: Needs-input outranks active without animating (BUG-012)

- **WHEN** a live worker role is genuinely active (`Live && SessionRunning && !SessionIdle`, independent of its bound task's status) AND the role itself also needs input (blocked on a prompt)
- **THEN** its plan node renders the static needs-input `?` glyph and style — identical to the rail row — and is NOT flagged animated, so the widget does not swap the `?` for the live spinner frame at draw

#### Scenario: Planned and failed overlays

- **WHEN** a node is a never-bound planned role, or a bound role whose task reports failure
- **THEN** the planned node renders `○` and the failed node renders `✕`

#### Scenario: A genuinely-active bridging sub-coordinator node is unaffected by a blocked descendant

- **WHEN** a plan node represents a bridging worker row that is itself a nested sub-coordinator, that role is genuinely active, and some descendant in its bridged child orchestrator needs input
- **THEN** the node renders the active spinner (animated), not the static needs-input glyph — identical to what the rail row for that same role renders

## ADDED Requirements

### Requirement: Needs-input "(?)" reflects only a role's own signal on every surface (area rail)

The system SHALL derive the needs-input "(?)" indicator on EVERY coordinator-shaped render surface — the rail's collapsed orchestrator header, a bridging worker row that is itself a nested sub-coordinator, the Details pane's `coordinator:` status line, a Details roster row, and a plan-DAG node icon — EXCLUSIVELY from that role's OWN needs-input signal (`RoleView.needsInputOwn()`: a current, content-aware PTY prompt or a self-asserted `blocked` hera status), computed identically to how a plain leaf role's own glyph is derived. A descendant role's needs-input state — however deep, and across however many bridged sub-orchestrator levels — SHALL NOT cause any ancestor's own icon to render the needs-input indicator. This holds uniformly across all five surfaces because they share one classifier (`roleStatusInputs`/`widget.RoleStatusIcon`, reading `RoleView.ShowsNeedsInput()`, which returns `needsInputOwn()` alone) — not five independent implementations that could drift.

An orchestrator with NO coordinator role (its coordinator role was deleted/nuked) SHALL NOT surface any needs-input indicator on its header, in any state — there being no "own" signal for such a header to derive from, the fallback that once rendered the rollup directly on this header is removed outright, not narrowed.

The needs-input ROLLUP COMPUTATION itself (`RoleView.SubtreeNeedsInput`, `OrchView.SubtreeNeedsInput`, populated by `rollupNeedsInput`/`orchSubtreeNeedsInput`) is UNCHANGED by this requirement: it remains transitive across bridged sub-orchestrators, cycle-safe, and excludes archived roles from counting toward an ancestor, exactly as before. Its role is narrowed, not removed — it exists solely to gate the partial-fold-reveal mechanism (deciding which specific closed-fold descendant rows to peek through), never to drive any icon's display directly anymore. A blocked descendant therefore remains fully visible — as its OWN row, peeked through any number of closed ancestor folds — via "Rail reveals the ancestor path to a hidden needs-input descendant through closed folds," which this requirement does not change and does not duplicate.

Derived from: `internal/tui/hera/model.go` (`RoleView.ShowsNeedsInput` returns `needsInputOwn()` alone; `RoleView.SubtreeNeedsInput`, `OrchView.SubtreeNeedsInput`, `rollupNeedsInput`, `orchSubtreeNeedsInput` unchanged), `internal/tui/hera/rail.go` (`statusIcon`/`roleStatusInputs` read `ShowsNeedsInput`; `drawOrchRow`'s coordinator-less fallback branch removed; the reveal gates in `appendOrch`/`appendOrchWorkers`/`appendOrchRevealPath`/`appendWorkerRow`/`appendPinnedRole` unchanged), `internal/tui/hera/details.go` (`coordinator:` status line and `rosterStatusText`/`drawRosterRow`, both reading the same shared classifier, unchanged code), `internal/tui/hera/plan.go` (`planNodeIcon`, unchanged code, reads the same shared classifier).

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

- **WHEN** a coordinator role is itself blocked on a prompt (own signal), independent of whatever state its descendants are in
- **THEN** the coordinator's header renders the needs-input "(?)" indicator, exactly as any other role's own signal would

#### Scenario: The Details status line and roster follow the same own-signal-only rule

- **WHEN** the Details pane is showing a coordinator whose own signal is clear but which has a blocked descendant, and its roster includes a bridging worker row that is itself a nested sub-coordinator with a blocked descendant but no own signal
- **THEN** neither the `coordinator:` status line nor that roster row renders the needs-input glyph or the `"needs-input"` text label

## REMOVED Requirements

### Requirement: Needs-input "(?)" propagates up the orchestration tree to the root (area rail)

**Reason**: Superseded. This requirement documented a coordinator's rail status icon (and, transitively, the Details status line, Details roster, and plan node that share its classifier) lighting up "(?)" from ANY descendant's needs-input state, plus a coordinator-less orchestrator header falling back to the same rollup directly (BUG-028). Both behaviors are removed: every coordinator-shaped icon now reflects only its own signal (see "Needs-input '(?)' reflects only a role's own signal on every surface"), and the coordinator-less fallback is deleted outright rather than narrowed. The underlying rollup COMPUTATION this requirement also documented (transitivity across bridges, cycle-safety, archived-role exclusion) is unchanged and is now scoped entirely to gating the fold-reveal mechanism, covered by "Rail reveals the ancestor path to a hidden needs-input descendant through closed folds," which this change does not touch.

**Migration**: Any code or test asserting a coordinator's (or a coordinator-less orchestrator header's) status icon lights up "(?)" purely from a descendant's rollup should expect it to NOT light up going forward — only the descendant's own row (already independently visible via the reveal mechanism) shows the glyph.

### Requirement: Needs-input "(?)" CLEARS and propagates up when a descendant resolves (area rail)

**Reason**: Superseded for the same reason as the requirement above — it documented the clearing mirror of an ancestor-icon rollup that no longer exists. With no ancestor icon ever surfacing a descendant's rollup, there is nothing left on an ancestor's icon to "clear" when the descendant resolves. A role's OWN needs-input clearing (its content-aware signal resolving, or its live binding ending) is unaffected by this change and was never what this requirement described — that behavior remains governed by `buildRoleView`'s per-role signal derivation, untouched here.

**Migration**: No caller-visible migration for a role's own needs-input clearing. Any code or test asserting an ancestor coordinator's icon clears "(?)" specifically because a descendant resolved (as opposed to the ancestor's own signal clearing) should be removed or rewritten to assert the ancestor never showed "(?)" for that reason in the first place.

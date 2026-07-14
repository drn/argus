# Hera View

## Purpose

The Hera View is the native, in-tree TUI surface for multi-agent coordination. It is the Argus TUI's always-present second tab (`internal/tui/hera`), rendering the Hera role hierarchy (orchestrators → coordinator/worker/freelance roles) read-only from the SQLite hera store and driving a small set of in-process mutations off the rail cursor. It replaced the retired standalone DAG tab.

This capability captures the CURRENT native behavior of the view across the nine comparison areas Aaron uses to diff native against the original out-of-tree Hera plugin:

1. Rail structure & nesting
2. Rail sections
3. Row rendering
4. Keybindings
5. Focus & layout
6. Coordinator/agent panes & details
7. Archive / prune / delete / teardown
8. Freelance (adopt)

(Area 9, messaging/doorbell surfacing, is captured in the `hera-messaging` capability; the coordination/tool/storage substrate is in `hera-coordination`.)

This is a faithful state-capture of what the code DOES today, not a redesign. Where native is materially less capable than the plugin spec, the requirement describes native's actual (lesser) behavior and carries a `NOTE:` flagging the gap — derived from `docs/RAIL-PARITY-ANALYSIS.md` and `docs/NATIVE-HERA-FOLLOWUPS.md` and verified against the source.
## Requirements
### Requirement: Hera is the always-present second tab

The system SHALL render the native Hera view as the second tab (`widget.TabHera`, enum slot 1), reachable by the `2` hotkey. The tab is always the native view; the legacy DAG page was deleted with `depends_on`. The `cfg.Hera.Enabled` flag SHALL NOT toggle the view — it only gates daemon-side MCP native-vs-plugin tool registration on the next daemon start.

Derived from: `internal/tui/app.go:2837` (TabHera switch arm), `internal/tui/app.go:2851` (`switchToHeraTab2`), `internal/tui/widget/header.go:18` (TabHera slot), `context/knowledge/gotchas/hera-view.md`.

#### Scenario: Second tab routes to the native Hera page

- **WHEN** the user presses `2` or otherwise switches to the second tab
- **THEN** the system routes to the `"hera"` page and renders the native Hera view, regardless of `cfg.Hera.Enabled`

#### Scenario: Remote mode degrades the page

- **WHEN** the TUI runs in `--remote` mode where no local `*db.DB` hera reader is available
- **THEN** the page is constructed with a nil reader and renders an "Hera unavailable in remote mode" banner instead of a rail, wires no session resolver, feeds no panes, and treats every mutation key as an inert no-op

### Requirement: Multi-binding fan-out is structural (area 1)

The system SHALL surface a single argus task bound under two orchestrators once under EACH orchestrator, with no per-task dedupe. This is achieved structurally: `BuildModel` walks orchestrators → their roles → each role's live binding, so a task reached through two distinct role rows appears under both.

Derived from: `internal/tui/hera/model.go:46` (Model doc), `internal/tui/hera/model.go:141` (`BuildModel`), `context/knowledge/gotchas/hera-view.md`.

#### Scenario: Task bound under two orchestrators appears twice

- **WHEN** one argus task holds a live binding under orchestrator A and another under orchestrator B
- **THEN** the rail shows that task's role once under A and once under B, each as a distinct row

### Requirement: Cursor navigation and collapse over selectable rows (area 1)

The system SHALL move the cursor only across selectable rows (orchestrator headers, roles, freelance roles, the archive expando, and the freelance fold header), skipping rules and plain section labels. After any rebuild the cursor SHALL be re-pinned to the same logical row (by role id, or negated orchestrator id) when it still exists, and clamped onto a selectable row otherwise. Collapse state (per-orchestrator, freelance, archive) SHALL survive rebuilds.

Derived from: `internal/tui/hera/rail.go:43` (`selectable`), `internal/tui/hera/rail.go:119` (`currentRef`), `internal/tui/hera/rail.go:134` (`restoreCursor`), `internal/tui/hera/rail.go:230` (`step`), `internal/tui/hera/rail.go:257` (`clampCursor`), `internal/tui/hera/rail.go:287` (`ToggleCollapse`).

#### Scenario: Down/up skip non-selectable rows

- **WHEN** the cursor steps past a rule or plain header
- **THEN** it lands on the next selectable row in that direction, or stays put if none exists

#### Scenario: Cursor survives a rebuild

- **WHEN** the model is replaced and the rail rebuilds
- **THEN** the cursor re-pins to the previously selected role/orchestrator if it still exists, else clamps onto a selectable row

#### Scenario: Collapse toggles the row under the cursor

- **WHEN** the user presses Space on an orchestrator header, the freelance fold header, or the archive expando
- **THEN** that section's collapsed flag flips, the rows rebuild, and the cursor is restored

### Requirement: Rail sections — Pinned, Active, Freelance, Archive (area 2)

The system SHALL render the rail in a fixed section order: a "Pinned" section (rendered when pinned orchestrators OR pinned non-root roles exist), the active orchestrators (no header, like the task list's active section), a "Freelance (N)" fold section (only when freelance roles exist), and an "Archive (N)" expando section at the bottom (only when archived orchestrators exist). Within the Pinned section, pinned orchestrators (with their subtrees) render first, followed by pinned non-root roles (each as a two-line breadcrumb entry — see the pinned-non-root-role requirement). A non-selectable horizontal-rule divider (the same `─` `StyleBorder` rule drawn above the Freelance and Archive sections) SHALL separate the Pinned section from the Active list, rendered ONLY when the Pinned section is present AND at least one Active entry follows it (no stray rule when nothing is pinned, and none when the Pinned section is the only content). The Freelance section defaults expanded; the Archive section defaults collapsed. When the model is entirely empty the rail renders a single "No hera orchestrators" placeholder row.

Derived from: `internal/tui/hera/rail.go` (`buildRows`), `internal/tui/hera/rail.go` (`NewRail` archive default), `internal/tui/hera/model.go` (Model sections).

#### Scenario: Pinned section appears when an orchestrator is pinned

- **WHEN** at least one orchestrator is pinned
- **THEN** a "Pinned" header renders above the active list with the pinned orchestrators (and their subtrees) under it

#### Scenario: Pinned section appears when only a non-root role is pinned

- **WHEN** no orchestrator is pinned but at least one non-root role is pinned
- **THEN** a "Pinned" header still renders, with the pinned role(s) shown as breadcrumb entries under it

#### Scenario: Divider separates the Pinned section from the Active list

- **WHEN** the rail has a Pinned section AND at least one Active orchestrator below it
- **THEN** a single non-selectable horizontal-rule divider renders between the last Pinned row and the first Active row, and the cursor skips it during `j`/`k` navigation

#### Scenario: No Pinned divider when nothing is pinned or no Active follows

- **WHEN** there is no Pinned section, OR the Pinned section is present but no Active entry follows it
- **THEN** no Pinned→Active divider renders

#### Scenario: Archive section collapsed by default

- **WHEN** archived orchestrators exist
- **THEN** an "Archive (N)" expando renders at the bottom, collapsed by default, expanding only when toggled

#### Scenario: Empty model shows a placeholder

- **WHEN** there are no orchestrators or freelance roles at all
- **THEN** the rail renders a single non-selectable "No hera orchestrators" row

### Requirement: Orchestrator and role row rendering (area 3)

The system SHALL render an orchestrator header with a fold chevron (▸ collapsed / ▾ expanded), a coordinator marker glyph, the orchestrator name, and a right-aligned live-role count `(N)` (count of roles with a live binding). It SHALL render a role row with a status glyph (see status-icon precedence) followed by the role name. Selection is indicated by a `›` marker in the gutter and the selected palette; archived placement dims the row's text style (the glyph itself never lies — only the style dims).

Derived from: `internal/tui/hera/rail.go:391` (`drawRow`), `internal/tui/hera/rail.go:437` (`drawOrchRow`), `internal/tui/hera/rail.go:470` (`drawRoleRow`), `internal/tui/hera/rail.go:498` (`liveRoleCount`).

#### Scenario: Orchestrator header shows live count

- **WHEN** an orchestrator has three roles, two of which hold live bindings
- **THEN** its header renders `(2)` right-aligned

#### Scenario: Archived row dims without changing its glyph

- **WHEN** a role is rendered in the archive section
- **THEN** its text and status glyph render in the dimmed style while the glyph identity is unchanged

### Requirement: Status-icon precedence on role rows (area 3)

The system SHALL choose a role row's status glyph by this precedence: (1) `ready_to_close` wins over everything with a distinct review glyph; otherwise (2) an operator/agent-set `blocked` or `done` hera role status renders its distinct static glyph; otherwise (3) GENUINE activity (`RoleView.IsActive` — a live binding whose bound argus task is `in_progress`) renders the ACTIVE SPINNER's animated frame (see "Active agents animate a spinner glyph"); otherwise (4) an `idle` hera role status renders the static idle glyph; otherwise (5) binding presence (`Live`) renders a "live" glyph; otherwise (6) an unbound/dimmed glyph. The spinner is sourced from REAL session activity, never the stale `working` hera role status (BUG-003): a `working` role that is not genuinely active falls through to (5)/(6) and renders a static glyph. `ready_to_close` is read from the task-addressed `task_meta` "hera" namespace, not the hera tables.

Derived from: `internal/tui/hera/rail.go` (`statusIcon`), `internal/tui/hera/model.go` (`RoleView.IsActive`, `buildRoleView` reads `ready_to_close`).

#### Scenario: ready_to_close overrides status

- **WHEN** a role's bound task carries `meta:hera.ready_to_close=true` AND the role status is working
- **THEN** the row renders the review/ready glyph, not the working spinner

#### Scenario: Genuine activity renders the animated spinner

- **WHEN** a role holds a live binding whose bound argus task is `in_progress` and is not blocked/done/ready_to_close
- **THEN** the row renders the active spinner's frame (animated), not a static glyph

#### Scenario: Stale working role-status does not animate

- **WHEN** a role's hera status is `working` but it is not genuinely active (no live binding, or its bound task is no longer `in_progress`)
- **THEN** the row renders a static glyph (live or dimmed-unbound), not the spinner

#### Scenario: Blocked outranks activity

- **WHEN** a role has a status row of `blocked` and is not ready_to_close (even while its bound task is still `in_progress`)
- **THEN** the row renders the needs-input/blocked glyph (static), not the spinner

#### Scenario: Live-but-statusless role

- **WHEN** a role holds a live binding but has no status row and is not ready_to_close
- **THEN** the row renders the in-review "live" glyph rather than the unbound glyph

### Requirement: Rail keybindings (area 4)

The system SHALL bind the following keys while the rail holds focus: `j`/`k` and Down/Up move the cursor; Left walks to the parent; Space collapses/expands the row under the cursor; `Tab`/`Backtab` and `Ctrl+Alt+Left`/`Ctrl+Alt+Right` walk the focus ladder; `Ctrl+Q` returns focus to the rail; `Enter` enters the selected role's pane (restarting a dead/suspended session first); `w` spawns a worker under the selected coordinator's orchestrator via the full new-task modal; `n` creates a new top-level coordinator via the full new-task modal; `r` renames the selected role/orchestrator; `a` HIDES the selected worker / sub-coordinator into its parent coordinator's nested archive (Tier 1 — reversible, keeps the session + worktree alive); `P` toggles pin; `s`/`S` advance/revert the selected role's hera status; `J` adopts a freelancer / re-parents a coordinator; `B` forces an immediate recycle of the selected coordinator (kills and restarts its session on the same task, seeded from its mission, plan-DAG state, and any handoff note — see `coordinator-context-management`), behind a confirmation modal, and is a no-op on a non-coordinator selection; `C` clears the selected coordinator's archive (NUKES every Tier-1 hidden item under it); `Ctrl+Z` fullscreens the focused pane; `/` filters the rail by name; `Ctrl+D` NUKES the selected role/orchestrator (Tier 2 — removes it and its whole subtree from the rail, reclaims worktrees). Every bound key SHALL appear in the help overlay's "Hera View (rail)" section.

The rail SHALL NOT bind `R` (retire) or a rail-wide `Ctrl+R` (prune) — both are removed by this redesign. All rail mutation keys SHALL be suppressed while the rail is in `/` filter INPUT mode (every keystroke is filter input). Selection-acting keys (`w`, `r`, `a`, `P`, `s`/`S`, `J`, `B`, `C`, `Ctrl+D`) are no-ops on an empty selection; `n` is selection-INDEPENDENT and fires even on an empty rail.

Derived from: `internal/tui/hera/rail.go` (rail `InputHandler`), `internal/tui/hera/page.go` (page `InputHandler` focus ladder + `handleRailMutation`), `internal/tui/heraactions.go` (handlers), `internal/tui/modal/help.go` (help overlay Hera section).

`NOTE:` `Ctrl+D` is the only key that NUKES a live selection directly (`C` nukes only the selected coordinator's already-hidden Tier-1 archive items); the rail binds no `R` (retire) or rail-wide `Ctrl+R` (prune). `Ctrl+D` never collides with the agent-view `Ctrl+R` (Claude session switcher), which runs in a different mode/widget. `B` (force recycle) acts on a live coordinator session directly (kill-and-restart) but preserves the task/worktree/branch/binding — unlike `Ctrl+D`, nothing is removed from the rail. A focused content pane forwards `C`/`a`/`Ctrl+D` to its PTY.

#### Scenario: Hide key acts on the current selection

- **WHEN** the rail is focused and the user presses `a` on a selected worker
- **THEN** the hide callback fires for that worker's `(role, orchestrator)` selection (no confirmation) and the key does not leak to navigation

#### Scenario: Retire and rail-wide prune keys are unbound

- **WHEN** the user presses `R` or `Ctrl+R` while the rail is focused
- **THEN** nothing end-of-life happens (`R` is unbound; `Ctrl+R` is not a rail-wide prune) — the redesign removed both

#### Scenario: Help overlay lists every rail key

- **WHEN** the help overlay is opened
- **THEN** its "Hera View (rail)" section lists `j`/`k`, space, Left, Tab/Ctrl+Q, Enter, `w`, `n`, `r`, `a` (hide), `P`, `s`/`S`, `J`, `B` (force recycle), `C` (clear archive), `Ctrl+Z`, `/`, and `Ctrl+D` (nuke), and does NOT list `R` or a rail-wide `Ctrl+R`

#### Scenario: Force-recycle key requires confirmation

- **WHEN** the rail is focused, a coordinator row is selected, and the user presses `B`
- **THEN** a confirmation modal appears before the recycle proceeds

#### Scenario: Force-recycle key is a no-op on a non-coordinator selection

- **WHEN** the rail is focused and a worker or freelance row is selected and the user presses `B`
- **THEN** nothing happens — no modal, no recycle

### Requirement: Three-region focus model and ladder (area 5)

The system SHALL track focus across three regions — rail, coordinator (HERA/middle) pane, and agent/details (right) region — via a focus machine that never lands focus on an absent region. `Advance`/`Retreat` step only through present regions; when a focused region disappears focus rebalances to the nearest present one (the rail is always present, so the fallback terminates). `Ctrl+Q` forces focus back to the rail. Mouse clicks focus the region under the cursor only if that region is present.

The focus machine SHALL also carry a fullscreen flag for the focused content pane. `Ctrl+Z` toggles it via the page (a no-op while the rail is focused). Fullscreen is an attribute of the *focused* pane: advancing the ladder while fullscreen keeps fullscreen on the new pane, while any transition that lands focus back on the rail (Retreat from the coordinator pane, `Ctrl+Q`/`ToRail`, or a rebalance off a disappearing pane) SHALL clear fullscreen — the rail has no fullscreen mode. Present-region flags are computed from the normal split geometry independent of fullscreen, so the ladder can still traverse the hidden pane.

Derived from: `internal/tui/hera/focus.go:34` (`FocusMachine` + fullscreen flag), `internal/tui/hera/focus.go:68` (`rebalance` clears fullscreen on rail), `internal/tui/hera/focus.go:89` (`Advance`/`Retreat`), `internal/tui/hera/focus.go` (`ToggleFullscreen`/`Fullscreen`), `internal/tui/hera/focus.go:131` (`SetRegion`), `internal/tui/hera/page.go:447` (`MouseHandler`).

#### Scenario: Advance skips an absent pane

- **WHEN** the agent region is absent (narrow terminal) and focus advances from the coordinator pane
- **THEN** focus does not move to the absent agent region (stays at the right-most present region)

#### Scenario: Focus rebalances off a disappearing region

- **WHEN** focus is on the agent region and that region becomes absent on the next layout
- **THEN** focus bumps to the coordinator pane if present, else to the rail

#### Scenario: Ctrl+Q returns to the rail

- **WHEN** any pane is focused and the user presses `Ctrl+Q`
- **THEN** focus returns to the rail

#### Scenario: Fullscreen toggles on a focused pane and survives advance

- **WHEN** the coordinator pane is focused, the user enables fullscreen, then advances the ladder to the agent region
- **THEN** fullscreen stays on for the now-focused agent region

#### Scenario: Returning to the rail clears fullscreen

- **WHEN** a content pane is fullscreen and focus returns to the rail (Retreat from the coordinator pane, `Ctrl+Q`, or a rebalance off a disappearing pane)
- **THEN** fullscreen turns off

### Requirement: Three-region layout computed in Draw (area 5)

The system SHALL lay out the view as rail | coordinator pane | agent/details region, computing rects in `Draw` (not via a tview.Flex). The rail is a fixed width (`heraRailWidth`); the remaining width splits evenly between the coordinator pane and the agent region. When the terminal is too narrow for a right area the rail takes the full width and both right regions are marked absent so focus cannot land on them. Every region paints through `widget.DrawBorderedPanel`/`FillArea` to cover its full bounding rect, and the view SHALL NOT call `screen.Sync()` for content updates.

When fullscreen is active and a content pane is focused, the system SHALL instead render the rail at its fixed width plus the single focused pane (coordinator pane, or the agent terminal / stacked details region) filling the entire remaining width; the other content region is not drawn and its recorded hit-test rect is collapsed to zero width. Present-region flags are still computed from the normal split so the focus ladder can traverse the hidden pane. Fullscreen rendering SHALL also paint full bounding rects and SHALL NOT call `screen.Sync()`.

Derived from: `internal/tui/hera/panes.go:14` (`heraRailWidth`), `internal/tui/hera/page.go:176` (`Draw` + fullscreen branch), `internal/tui/hera/page.go:208` (present-flag reconciliation), `context/knowledge/gotchas/hera-view.md` (no-Sync rule).

#### Scenario: Narrow terminal hides the right regions

- **WHEN** the terminal is narrower than the rail width plus a usable right area
- **THEN** the rail fills the width and both right regions are marked absent

#### Scenario: Fullscreen renders the rail plus a single pane

- **WHEN** fullscreen is active with the agent region focused
- **THEN** only the rail and the agent region are drawn, the agent region fills the full width to the right of the rail, and the coordinator pane's hit-test rect is zero-width

#### Scenario: Drawing the full view never calls Sync

- **WHEN** the view draws with live panes and a details region, fullscreen or not
- **THEN** `screen.Sync()` is called zero times

### Requirement: Coordinator (HERA) pane always shows the orchestrator's coordinator (area 6)

The system SHALL feed the middle (HERA) pane from the SELECTED ORCHESTRATOR's coordinator task session at all times, regardless of whether the selected role is a coordinator or a worker. The pane is a live `terminal.TerminalPane` fed from the in-process runner's ring buffer (polled on Draw), NOT via SSE or a proxy. When the selected role is itself the coordinator, the HERA pane shows that task.

Derived from: `internal/tui/hera/panes.go:30` (coord-vs-agent rule), `internal/tui/hera/panes.go:59` (`applySelection`), `internal/tui/hera/model.go:89` (`CoordTaskID`), `internal/tui/hera/panes.go:24` (`SessionResolver` seam).

`NOTE:` Native feeds panes by POLLING the daemon-owned PTY ring buffer through an in-process `SessionResolver` seam wired to `runner.Get`. The plugin's WebSocket `/view` server, per-binding SSE PTY proxy, and snapshot pre-load (`hera-view` plugin spec) have NO native equivalent and are not ported.

#### Scenario: Selecting a worker still shows its coordinator in HERA

- **WHEN** a worker role under orchestrator A is selected
- **THEN** the HERA (middle) pane shows A's coordinator task session, while the agent pane shows the worker's task

#### Scenario: Multi-binding disambiguation feeds two contexts

- **WHEN** a task is a worker in A and a coordinator in B, and the operator selects each role in turn
- **THEN** selecting the A-role feeds HERA from A's coordinator and the agent pane from the shared task; selecting the B-role feeds HERA from the shared task (B's coordinator = itself) and renders B's roster — the disambiguator is always the selected role's orchestrator, never the bare task ID

### Requirement: Agent/details region is mode-switched by the selected role (area 6)

The system SHALL render the right region as a live AGENT terminal when a worker/freelance/leaf role is selected, and as a read-only Details summary (worker roster) when a coordinator role is selected. A coordinator selection renders the agent pane unbound (no terminal).

The Details summary's `coordinator:` status line SHALL be task-aware: it combines the coordinator's hera ROLE status (preserving the BUG-003 stale-`working` honesty — a `working` role with no real activity reads `live` when its binding is alive and `stopped` when gone) with any TERMINAL bound-task signal. Terminal task states are in_review, complete, and failed (the last derived from the bound task's `TaskResult` `{"failed":true}`, mirroring `dagview.parseFailed`). When the task adds a terminal signal the line reads `"<role> · task <state>"` (e.g. `live · task complete`); an ongoing (pending / in_progress) or unbound task adds no suffix. A malformed `TaskResult` JSON blob is tolerated (no failed suffix). The line stays a single row, so `DetailsView.ContentHeight()` and the coordinator metadata block (Created / Last activity / Agent / Worktree / Repos) are unaffected.

Derived from: `internal/tui/hera/panes.go:59` (`applySelection` detailsMode), `internal/tui/hera/model.go:120` (`IsCoordinator`), `internal/tui/hera/details.go:23` (`DetailsView`), `internal/tui/hera/details.go` (`coordStatusLabel` / `coordRoleStatusLabel` / `coordTaskStatusLabel`), `internal/tui/hera/page.go:221` (Draw mode branch).

#### Scenario: Worker selection shows a terminal

- **WHEN** a worker role is selected
- **THEN** the right region shows the worker's live agent terminal

#### Scenario: Coordinator selection shows the details region

- **WHEN** a coordinator role is selected
- **THEN** the right region renders the Details roster (no terminal) stacked over the orchestration tree

#### Scenario: Coordinator status line surfaces a terminal task state

- **WHEN** the coordinator's bound task is `complete` (or `in_review`, or carries `TaskResult {"failed":true}`)
- **THEN** the `coordinator:` line appends `· task complete` (or `· task in_review`, or `· task failed`) to the role-status label

#### Scenario: Coordinator status line adds no suffix for an ongoing task

- **WHEN** the coordinator's bound task is `in_progress` (or pending, or unbound)
- **THEN** the `coordinator:` line shows only the role-status label, with no `· task …` suffix

### Requirement: PR indicator in the roster (area 6)

The system SHALL mark a roster task with a `PR` indicator when that task carries a non-empty `url` in the daemon-populated `task_meta` "pr" namespace. The indicator is best-effort and read once per refresh via `ListMetaByNamespace("pr")`; it is never fetched by the view. A `ready` mark renders for a `ready_to_close` worker, and both marks may appear together.

Derived from: `internal/tui/hera/details.go:158` (`roleMark`), `internal/tui/hera/page.go:160` (`doRefresh` reads "pr" namespace).

#### Scenario: PR mark from cached meta

- **WHEN** a roster task's "pr" meta has a non-empty url
- **THEN** its roster row shows a `PR` mark

### Requirement: PTY size alignment on bind (area 6)

The system SHALL resize a bound session to the (narrower) hera pane when binding it, by calling `ForceResyncPTY()` so a session previously sized for the full-width main agent view is resized down on the next Draw, with `SyncPanes()` issuing the Resize RPC off the main thread. Pane operations on the tview thread SHALL use only lock-free/local session methods; the blocking resize RPC runs only from the tick goroutine. The view SHALL NOT use `screen.Sync()` to paper over a size mismatch.

Derived from: `internal/tui/hera/panes.go:86` (`bindPane` ForceResyncPTY), `internal/tui/hera/panes.go:153` (`SyncPanes`), `internal/tui/hera/panes.go:172` (`forwardKey` main-thread-safe reads), `context/knowledge/gotchas/hera-view.md`.

#### Scenario: Bind resizes a full-width session down

- **WHEN** a session sized for the full-width agent view is bound into a hera pane
- **THEN** `ForceResyncPTY` arms an unconditional resize and `SyncPanes` applies it off the main thread

#### Scenario: Off-tab SyncPanes is a no-op

- **WHEN** `SyncPanes` is called while the Hera tab is not active
- **THEN** no resize fires (panes not drawn this frame have zero pending resize), so it cannot fight the main agent view's resize of the same task

### Requirement: Debounced rail refresh on the UI thread (area 6)

The system SHALL rebuild the rail model via a goroutine-free, timer-free debounced `Refresher` driven by the app tick and tab entry. `Schedule()` coalesces bursts into one rebuild per debounce window; tab entry forces an immediate flush. Rebuilds run on the tview thread because hera-store reads are mutex-guarded and fast (the "never on the UI thread" rule is about git, not DB reads). After `SetModel` the selection is re-derived and the panes rebound, so stale model pointers are refreshed.

Derived from: `internal/tui/hera/refresher.go` (`Refresher`), `internal/tui/hera/page.go:138` (`ScheduleRefresh`), `internal/tui/hera/page.go:150` (`doRefresh`), `internal/tui/hera/panes.go:59` (`applySelection` re-run).

#### Scenario: Burst of writes coalesces to one rebuild

- **WHEN** several store writes schedule refreshes within one debounce window
- **THEN** the rail rebuilds once

#### Scenario: Tab entry forces a fresh rail

- **WHEN** the Hera tab is opened
- **THEN** the refresher flushes immediately so the rail is current the instant the tab appears

### Requirement: Archive, pin, rename, delete, and status ops act on the selection (area 7)

The system SHALL apply each mutation to the SELECTED `(role, orchestrator)` from the rail cursor, never a bare task ID. Archive and pin toggles read the current row state from the store to choose direction. Pinning clears archived state (pin and archive are mutually exclusive). Rename surfaces a name-conflict error for the caller to display. Status advance/revert step the hera role status ladder (idle → working → blocked → done), clamped at the ends, and reaching `done` on a WORKER role also rolls the bound task to in_review (soft-fail). Stepping a WORKER role to any NON-`done` status (revert off `done`, or any other step) clears the bound task's `meta:hera.ready_to_close` mark via `ClearHeraReadyToClose` (the inverse of the done-roll's stamp), so the rail glyph — which checks `ready_to_close` FIRST in its precedence — reflects the new hera status instead of staying pinned to the review `✓`. The clear is soft-fail (the status update always lands) and touches meta only; the task's argus WORKFLOW status is owned by the session lifecycle and is never changed by a status step. Mutations are thin adapters over existing store methods; the spawn path is the shared `agent.SpawnHeraWorker` primitive, run off the main thread.

Derived from: `internal/tui/hera/ops.go:85` (`ArchiveToggle`), `internal/tui/hera/ops.go:118` (`PinToggle`), `internal/tui/hera/ops.go:150` (`Rename`), `internal/tui/hera/ops.go:175` (`StepStatus`), `internal/tui/hera/ops.go:66` (status ladder), `internal/db/hera.go` (`ClearHeraReadyToClose`), `context/knowledge/gotchas/hera-view.md` (M6c).

#### Scenario: Worker reaching done rolls its task to in_review

- **WHEN** `s` advances a worker role to `done`
- **THEN** the hera status is set to done AND `RollHeraWorkerToReview` rolls the bound task to in_review and stamps `ready_to_close` (the roll is soft-fail so the status update always lands)

#### Scenario: Stepping a worker out of done clears the review mark

- **WHEN** a worker role is `done` with its bound task carrying `meta:hera.ready_to_close=true` (rail glyph = review `✓`) and the user presses `S` (revert)
- **THEN** the hera status moves `done → blocked`, `ready_to_close` is cleared, and the rail glyph visibly changes to the blocked glyph (the review `✓` no longer masks the status)

#### Scenario: The clear never changes the task's workflow status

- **WHEN** a worker stepped to in_review (by an earlier done-roll) is reverted off `done`
- **THEN** only the `ready_to_close` meta flag is cleared; the bound task's argus workflow status is left unchanged (it stays in_review — owned by the session lifecycle, not the hera ladder)

#### Scenario: Status step on a coordinator-less header is a no-op

- **WHEN** `s`/`S` is pressed with the cursor on an orchestrator header that has no coordinator role (and the cursor is not on a role)
- **THEN** nothing happens (`Selection.StatusRole()` resolves to nil)

### Requirement: Conservative delete semantics for multi-binding safety (area 7)

`Ctrl+D` in the Hera rail is the NUKE (Tier 2) action. It SHALL NEVER hard-delete a DB row. It marks the row NUKED (a `nuked_at` stamp that removes it from the rail entirely — it is NOT shown in any visible archive) and reclaims only the real resources. Specifically, nuke:

- marks the hera role row(s) NUKED and ENDS their live binding (never `DeleteHeraRole`);
- marks the orchestrator row NUKED (for a coordinator/header or whole-subtree nuke), never `DeleteHeraOrchestrator`;
- ARCHIVES the argus task row (`db.SetArchived`), never `db.Delete`;
- retains the role's inbox/messages — because the role row is retained (only stamped nuked/archived), its messages stay attached as history (no message rows are deleted, no message-archive column is required, and a nuked role's inbox stays readable);
- RECLAIMS only the real resources: stops the session and removes the worktree + LOCAL and REMOTE branch.

Nuking a ROLE reclaims the worktree + archives the task ONLY if that task has exactly one live binding; a MULTI-bound task is PRESERVED — left fully alone (not archived, worktree kept). The role row is marked nuked + its binding ended either way.

Nuking a COORDINATOR / orchestrator HEADER SHALL cascade the SAME mark-nuked-and-reclaim over the full subtree rooted at the selected orchestrator (`BridgeSubtree(root)`): that orchestrator, every nested sub-coordinator, and all their agents are marked nuked + their worktrees reclaimed. A task bound live in an orchestrator OUTSIDE the subtree is PRESERVED (left fully alone).

The cascade gates behind a count-bearing confirmation modal that states how many orchestrators and agents are removed, how many worktrees + branches are reclaimed (including any internal-bridge worktree between two subtree orchestrators), and how many tasks are preserved.

The difference from the `a` HIDE key: `a` HIDES (Tier 1) — the row moves into its parent coordinator's nested archive and the worktree/session stay ALIVE, fully reversible; `Ctrl+D` NUKES (Tier 2) — the row leaves the rail entirely and its worktree/session are reclaimed, recoverable only via the DB.

Derived from: `internal/tui/heraactions.go` (`heraOpenDelete`, `heraNukeRole`, `heraReclaimAndArchiveTask`, `heraCascadeNukeFrom`, `heraDoCascadeNuke`, `heraTaskBoundOutside`), `internal/tui/hera/ops.go` (`NukeRole`, `NukeOrchestrator`), `internal/tui/hera/model.go` (`BridgeSubtree`), `internal/db/hera.go` (`NukeHeraRole`, `NukeHeraOrchestrator`), `context/knowledge/gotchas/hera-view.md`.

`NOTE:` NET zero hard deletes from any hera table — every nuked role, orchestrator, inbox, and task row is retained and retrievable via the DB. The one remaining non-hera delete (`db.SetArchived` dropping a task's queued LEGACY `task_messages`, a different table) is established archive behavior and out of scope.

#### Scenario: Nuking a sole-bound role removes it from the rail and reclaims its worktree

- **WHEN** a role is nuked and its task has exactly one live binding
- **THEN** the session is stopped, the worktree + local and remote branch are reclaimed, the role row is marked NUKED (invisible to the rail) with its binding ended, and the argus task row is ARCHIVED — none are hard-deleted

#### Scenario: Nuking a multi-bound role preserves the task

- **WHEN** a role is nuked and its task holds live bindings in more than one orchestrator
- **THEN** the role row is marked nuked + its binding ended; the task is left fully alone (not archived, worktree kept) and its other-orchestrator binding survives

#### Scenario: Nuking a coordinator cascades over the full subtree and reclaims worktrees

- **WHEN** `Ctrl+D` is pressed on a coordinator / orchestrator header and the operator confirms
- **THEN** that orchestrator, every nested sub-coordinator, and all their agents are marked NUKED (removed from the rail) — sessions stopped and each sole-bound task's worktree + local and remote branch reclaimed — with nothing hard-deleted (rows retained, inboxes readable)
- **AND** a task bound live in an orchestrator outside the subtree is preserved (left fully alone)

#### Scenario: Cascade confirm states the counts

- **WHEN** `Ctrl+D` is pressed on a coordinator / orchestrator header
- **THEN** a confirmation modal opens stating how many orchestrators and agents are removed, how many worktrees + branches are reclaimed (counting the internal-bridge worktree in a multi-level subtree), and how many tasks are preserved

### Requirement: Enter restarts a dead session then enters the pane (area 7)

The system SHALL, on `Enter` over a selected role with a bound task that has no live session, fire the reattach callback to restart the session, then advance focus into the pane. A live row just advances focus; an empty selection only advances focus.

Derived from: `internal/tui/hera/page.go:376` (Enter arm in `handleRailMutation`).

#### Scenario: Enter on a dead session reattaches

- **WHEN** Enter is pressed on a role whose task has no live session
- **THEN** the reattach callback fires and focus advances into the pane

### Requirement: Freelance roles hoisted into a top-level section (area 8)

The system SHALL hoist active freelance-kind roles into a top-level "Freelance" rail section rather than nesting them under their orchestrator. `BuildModel` skips active freelance-kind roles when filling an orchestrator's roles and appends them to `Model.Freelance`, sorted by name. The native view SHALL provide a manual adopt affordance on the `J` key (see the dedicated requirement): a freelance row is adopted as a worker under a chosen orchestrator, and a coordinator selection is re-parented under a chosen orchestrator.

Derived from: `internal/tui/hera/model.go:189` (freelance hoist), `internal/tui/hera/model.go:206` (sort), `internal/tui/hera/rail.go:174` (Freelance section), `internal/tui/hera/adopt.go` (adopt/reparent ops).

`NOTE:` Native's Freelance source is still narrower than the plugin's. The plugin derived freelancers from UNMANAGED live argus tasks grouped by repo (zero hera bindings), so its adopt guard rejected ANY live binding. Native's Freelance section reflects only roles explicitly created with kind `freelance` (via `hera_join`), which already carry their own live binding under the orchestrator they joined. Native's adopt therefore rejects only a DUPLICATE binding under the SAME chosen orchestrator (the per-`(task, orchestrator)` unique index), not any live binding, and remains faithful to the multi-binding model. The unmanaged-task freelance source itself is out of scope for the read-only hera store (a separate follow-up moves freelancers to the Tasks tab).

#### Scenario: Freelance role appears in its own section

- **WHEN** a role of kind `freelance` is active under some orchestrator
- **THEN** it renders in the top-level "Freelance (N)" section, not nested under that orchestrator

#### Scenario: Adopt affordance exists on the rail

- **WHEN** the operator selects a freelancer (or a coordinator) and presses `J`
- **THEN** the system opens an orchestrator picker and, on selection, creates the worker role + binding (adopt) or re-parents the coordinator — adoption IS a native rail operation

### Requirement: A worker-bridge sub-coordinator selection shows Details, not the agent terminal (area 6)

The system SHALL route a rail selection that is a worker-bridge sub-coordinator —
a worker ROW that bridges a child orchestrator (its `Selection.BridgeChildOrchID`
is non-zero) — as a COORDINATOR selection: it SHALL enter Details mode for the
bridged CHILD orchestrator (its roster + orchestration tree), unbind the agent
terminal, and feed the HERA (middle) coordinator pane from the child
orchestrator's coordinator task (which is the sub-coordinator's own session). A
top-level coordinator (an orchestrator header, or a coordinator-kind role) SHALL
continue to drive Details mode for its own orchestrator, and a coordinator-spawned
sub-coordinator that already renders as its own orchestrator header SHALL be
unaffected. A plain worker/leaf selection (no bridged child) SHALL continue to
render the agent terminal. Selecting ANY coordinator — top-level or worker-bridge
sub-coordinator — SHALL therefore show its Details pane.

The MUTATION context exposed by `SelectionContext` (the selected role and its
containing orchestrator) SHALL remain the PARENT worker role under its
orchestrator for a worker-bridge sub-coordinator, so mutations (notably the Ctrl+D
cascade) continue to act on the worker role and never the child orchestrator; only
the pane/details/tree ROUTING follows the child.

Derived from: `internal/tui/hera/panes.go` (`applySelection`, `detailsOrch`), `internal/tui/hera/page.go` (`rebuildDAG(root)`), `internal/tui/hera/model.go` (`Selection.BridgeChildOrchID`, `Model.OrchByID`).

#### Scenario: Selecting a worker-bridge sub-coordinator shows the child's Details

- **WHEN** the rail cursor rests on a worker row that bridges a child orchestrator (a sub-coordinator born-bound to both a parent worker role and the child's coordinator role)
- **THEN** Details mode is entered for the child orchestrator (roster + orchestration tree rooted at the child's coordinator), the agent terminal pane is unbound, and the HERA pane feeds from the sub-coordinator's own session

#### Scenario: A plain worker still shows the agent terminal

- **WHEN** the rail cursor rests on a worker that does not bridge any child orchestrator
- **THEN** the agent terminal renders the worker's bound task and Details mode is not entered

#### Scenario: A top-level coordinator still shows Details

- **WHEN** the rail cursor rests on an orchestrator header (the folded coordinator) or a coordinator-kind role
- **THEN** Details mode renders that orchestrator's roster + orchestration tree

#### Scenario: A sub-coordinator selection preserves the parent worker mutation context

- **WHEN** a worker-bridge sub-coordinator row is selected
- **THEN** `SelectionContext` still reports the parent worker role and its orchestrator, so Ctrl+D and other mutations act on the worker role, not the child orchestrator

### Requirement: Details region stacks roster over the plan DAG (area 6)

For a coordinator selection the system SHALL render BOTH the read-only `" Details "` roster (top) and the embedded `" Plan "` graph (bottom) at once with no toggle. The roster is sized to its natural content height, capped at half the region (so the graph keeps at least half), clamped to a minimum of 3 rows and to the region height; the graph fills the remainder and is skipped when fewer than 2 rows remain. The graph (the plan view) is the ONLY interactive surface — every key in the details region forwards to it (the 4-way navigation, `Enter`/`Space`, and `Esc`), and coordinator-region clicks route to it.

When the user presses Enter on a plain leaf node whose role is buried under a COLLAPSED ancestor coordinator in the rail, the system SHALL first expand the rail — uncollapsing the target role's containing orchestrator and every ancestor on its canonical parent chain to the root (the same fold-independent parentage the rail nests by, handling deeply nested sub-coordinators) — so the role's row is built, and THEN perform the join. Fold state is resolved from the full model, never from the currently-built rows, so a folded coordinator never swallows the join. The expand persists like a user collapse-toggle.

#### Scenario: Both panels render together

- **WHEN** a coordinator is selected and the region is tall enough
- **THEN** the `" Details "` roster and the `" Plan "` graph both render, with no `" DAG "` or `" Orchestration Tree "` title

#### Scenario: Enter on a plain leaf node jumps to its role within the Hera view

- **WHEN** the details region is focused and the user presses Enter on a plain leaf node (not a group, not a sub-coordinator)
- **THEN** the system selects that node's role in the Hera rail (by its bound task id) and moves focus to that role's agent pane, staying on the Hera tab (it SHALL NOT switch to the Tasks view)

#### Scenario: Enter on a leaf node whose session is dead restarts and joins it

- **WHEN** the user presses Enter on a plain leaf node whose backing agent session has exited (no live session in the runner)
- **THEN** the system restarts-and-joins that session, identically to the rail's Enter — firing the same reattach under the same liveness gate (a dead session of any role, or a live non-coordinator role, fires it; a live coordinator stays navigate-only) — so the node never merely selects without restarting

#### Scenario: Enter on a leaf under a collapsed coordinator expands the rail then joins

- **WHEN** the user presses Enter on a plain leaf node whose containing coordinator (or any ancestor coordinator) is collapsed in the rail, so the role's row is not currently built
- **THEN** the system uncollapses the target's entire ancestor coordinator chain (rebuilding and persisting the rail like a user expand), then selects that node's role and moves focus to its agent pane — the join lands regardless of fold state instead of silently doing nothing

### Requirement: Plan DAG projects blocking edges and planned nodes in-memory (area 6)

The system SHALL project the embedded graph's nodes from the rail's already-built model via `heraPlanNodes` — a pure in-memory read with no DB call at Draw time. The graph renders the orchestrator's PLAN: every planned (never-bound) and live worker role as a node, and every `hera_blocks` blocking edge between them as a dependency edge. Stage placement is computed longest-path over the blocking edges; a node's short-id (parsed from the role-name prefix) is the display label only and never drives placement. A live node's colour comes from its bound task's argus status/result (including the failed-result red `✕`); a planned (never-bound) node renders violet with the `○` glyph. When the selected orchestrator has no planned nodes and no blocking edges, the graph SHALL render the orchestrator's live worker roles as a single flat edgeless stage with a "no plan" hint, so running workers are never invisible. When no orchestrator is selected, the graph renders empty without panic.

#### Scenario: Planned and live nodes render with edges

- **WHEN** an orchestrator has planned roles and blocking edges
- **THEN** the graph shows each role as a node, planned nodes violet `○`, live nodes coloured by task status, connected by the blocking edges

#### Scenario: Stage is computed from edges, not the short-id number

- **WHEN** a node's short-id number disagrees with its computed longest-path layer (e.g. after a plan edit)
- **THEN** the node is placed by the computed layer and still labelled with its short-id

#### Scenario: No plan authored renders live roles flat

- **WHEN** the selected orchestrator has no planned nodes and no blocking edges but has live workers
- **THEN** the graph renders those workers as one flat edgeless stage with a "no plan" hint, not an empty placeholder

#### Scenario: No orchestrator selected yields an empty graph

- **WHEN** no orchestrator is selected
- **THEN** the plan graph renders empty without panic

### Requirement: Short-id node labels (area 6)

The system SHALL label each plan node with its short-id — the prefix of the role name up to the first `-` (e.g. `2c-fact-checker` → `2c`), where the leading digits are the stage and the trailing letter(s) are the parallel member. When a role name has no parseable short-id prefix the label SHALL fall back to the truncated role name. The short-id is presentation only; it never affects layout or grouping correctness.

#### Scenario: Short-id parsed from the name prefix

- **WHEN** a role is named `2c-fact-checker`
- **THEN** its node is labelled `2c`

#### Scenario: Unparseable name falls back to the truncated name

- **WHEN** a role name has no short-id prefix
- **THEN** the node is labelled with the truncated role name and still placed by its edges

### Requirement: Parallel groups auto-collapse (area 6)

The system SHALL collapse a maximal set of nodes in the same computed stage that share the same blocker set and have no edges among themselves into a single range box labelled `[first–last]` (ids sorted), rendering `[first–last +N]` when membership is non-contiguous (N = the count of ids beyond the two endpoints of the span). A collapsed group box SHALL show aggregate counts of its members by state (e.g. `3 ✓ · 2 <spinner> · 1 ○`, with the rail-1:1 glyphs and per-state colour defined under "Collapsed group count segments are 1:1 with the rail"). A stage whose nodes do not form a clean group renders them as individual chips.

#### Scenario: Same-stage siblings collapse into a range box

- **WHEN** three same-stage nodes `2a`,`2b`,`2c` share a blocker set and have no edges among themselves
- **THEN** they render as one box `[2a–2c]` with aggregate state counts

#### Scenario: Non-contiguous group shows the span and a count

- **WHEN** a group's members are `2a`,`2b`,`2f` (non-contiguous)
- **THEN** the box renders `[2a–2f +1]`

### Requirement: Collapsed group box format and feed indicator (area 6)

A collapsed parallel group box SHALL render as two lines (matching the design):

- **Top line:** the bare range label `[first–last]` followed by a feed indicator — `→ <short-id>` when every out-of-group edge from the group's members points to ONE downstream node (full feed), or `↘` when only some members feed downstream (partial). No feed indicator when the group feeds nothing. The range label SHALL NOT carry a trailing bare count (the old `[2a–2c] 3 ○`, which read as "blocks 3a", is removed).
- **Sub line:** the group's common role token followed by the per-state aggregate counts, joined by ` · ` (e.g. `research · 1 ✓ · 2 <spinner>`); the counts alone when there is no common token.

On fan-out, every member with an out-of-group edge SHALL carry a `↘` on its box (not only a single feeder).

#### Scenario: Full-feed group shows the arrow target

- **WHEN** all members of group `[2d–2f]` feed the single downstream `3a`
- **THEN** the collapsed box top line is `[2d–2f] → 3a` and its sub line is `<token> · <counts>`

#### Scenario: Partially-feeding group shows the partial marker

- **WHEN** only `2b` of group `[2a–2c]` blocks the downstream `3a`
- **THEN** the collapsed box top line is `[2a–2c] ↘` (no arrow target) and, fanned out, `2b` carries a `↘`

### Requirement: Collapsed group count segments are 1:1 with the rail (area 6)

Each per-state count segment on a collapsed group's sub line SHALL use the rail's glyph vocabulary, NOT the compact chip set (`◔`/`⟳`). The glyph SHALL be resolved through the SAME shared classifier the rail's status icon and the live plan node use — a `RoleStatusInputs` synthesised from the segment's state (done → `Done`; working → `Active`; in_review → `ready-to-close`/review clipboard; pending/idle → `Idle`/moon) so the count can never drift from the rail; the two plan-only overlays the rail has no concept of (planned, failed) keep their `○`/`✕` glyphs. Each `<count> <glyph>` segment SHALL be coloured in that state's colour — the SAME per-state colour the node box uses (done green, working amber, in_review cyan, planned violet, failed red, pending/idle dim) — while the ` · ` separators and the common role token stay dim. (in_review is cyan, NOT green: green is reserved for done, distinct from the rail's ready-to-close green for this overlay-only count.) The working segment's spinner SHALL animate, re-resolving the frame from the wall-clock spinner frame at draw (the same mechanism the per-node working spinner uses), so a collapsed group hiding active work conveys motion.

#### Scenario: Mixed collapsed group renders rail glyphs, per-state colour, and an animated spinner

- **WHEN** a collapsed group contains a done, a working, and an in_review member
- **THEN** its count line renders `<n> ✓` in green, `<n> <spinner>` in amber with the live spinner frame (animated, not a static `⟳`), and `<n> 󰂼` (the review clipboard) in cyan (not `◔`, not green), with dim ` · ` separators — and the compact `◔`/`⟳` glyphs never appear

### Requirement: Four-way plan navigation with group fan-out (area 6)

The plan view SHALL support a cursor over `(stage, slot, member)`: `↑`/`↓` change the stage and collapse any fanned-out group on the way; `←`/`→` move within a stage between slots (nodes and collapsed groups). `Space` SHALL be a PURE fan-out/collapse toggle on a group slot — fanning out a collapsed group and collapsing a fanned one regardless of which member the cursor is on — and SHALL NEVER navigate (on a lone-node slot it is a no-op; opening a leaf is `Enter`'s job). `Enter` on a COLLAPSED group SHALL fan it out; on an interior MEMBER of a fanned-out group, `Enter` SHALL navigate to that member — firing the same leaf action a plain node fires (open/jump, or drill in when the member is a sub-coordinator) — and SHALL NOT collapse the group (collapsing is `Space`'s or `Esc`'s job). `Enter` on a fanned enclosure with no member under the cursor SHALL collapse it. Inside a fanned-out group, `←`/`→` walk between members; stepping off either edge exits and collapses the group and moves to the adjacent slot (or clamps at the stage edge).

#### Scenario: Up/down changes stage and collapses

- **WHEN** a group is fanned out and the user presses `↓`
- **THEN** the cursor moves to the next stage and the group collapses

#### Scenario: Enter fans out a group

- **WHEN** the cursor is on a collapsed group and the user presses `Enter`
- **THEN** the group fans out to show its members and the cursor lands on the first member

#### Scenario: Enter on a fanned-out group member navigates to it (does not collapse)

- **WHEN** a group is fanned out, the cursor is on one of its members, and the user presses `Enter`
- **THEN** the system fires that member's leaf action (jump to / open the member, or drill in when it is a sub-coordinator) and the group stays fanned out — it does NOT collapse

#### Scenario: Space on a fanned-out group member collapses and never navigates

- **WHEN** a group is fanned out, the cursor is on one of its members, and the user presses `Space`
- **THEN** the group collapses and no leaf action fires — `Space` is a pure toggle, not a navigation

#### Scenario: Stepping off a group edge exits and collapses

- **WHEN** the cursor is on the last member of a fanned-out group and the user presses `→`
- **THEN** the group collapses and the cursor moves to the next slot in the stage

### Requirement: Plan master-detail header (area 6)

Above the plan diagram the system SHALL render a fixed-height header describing the current selection: for a node, its name, a **status** (the node's state — planned for a never-bound role, else the live worker's state — with the state glyph), a description (the first line of the role's delivery prompt), and what it feeds; for a collapsed group, the group range and title, its members, and its downstream target. The header height is budgeted exactly so the diagram fills the remainder without truncation drift.

#### Scenario: Header shows the selected node

- **WHEN** the cursor is on a node
- **THEN** the header shows that node's name, status, description, and feeds

#### Scenario: Node header status reflects state

- **WHEN** the cursor is on a never-bound planned node
- **THEN** the header status line reads "planned"; for a live working node it reads "working"

#### Scenario: Header shows the selected group

- **WHEN** the cursor is on a collapsed group
- **THEN** the header shows the group range/title, its members, and its downstream target

### Requirement: Sub-coordinator drill-in (area 6)

When the cursor is on a node whose bound task is the coordinator of a child orchestrator (a sub-coordinator, discovered via the rail's in-memory bridge), `Enter` SHALL push that child orchestrator onto a navigation stack and re-project the plan DAG for the child. The header title SHALL reflect the currently-displayed orchestrator. Drill-in is navigation between independently-projected per-orchestrator DAGs; it SHALL NOT draw a cross-orchestrator edge. A sub-coordinator node SHALL carry a visible drillable marker so the gesture is discoverable. (Drilling back out is governed by the `Esc` back-out requirement below.)

#### Scenario: Enter drills into a sub-coordinator

- **WHEN** the cursor is on a sub-coordinator node and the user presses `Enter`
- **THEN** the diagram swaps to that child orchestrator's plan DAG and the header title names the child

### Requirement: Esc backs out one level and never jumps to the rail (area 6)

`Esc` in the plan view SHALL back out of the plan view's OWN state by one level, in a fixed priority order, and SHALL be CONSUMED by the widget in every case (it SHALL NOT propagate to the page or the rail):

1. when the cursor is on a fanned-out group, collapse it (un-fan; cursor returns to the collapsed slot);
2. otherwise, when drilled into a sub-coordinator, pop back to the parent orchestrator's plan DAG;
3. otherwise (root, nothing fanned), it is a consumed no-op.

The operator leaves the plan pane via the focus ladder (`Ctrl+Q` / `Tab`), never via `Esc`.

#### Scenario: Esc collapses a fanned group first

- **WHEN** the cursor is on a fanned-out group and the user presses `Esc`
- **THEN** the group collapses, the cursor returns to the collapsed slot, and the drill stack is unchanged

#### Scenario: Esc pops back to the parent when nothing is fanned

- **WHEN** the view is showing a drilled-in child plan DAG with nothing fanned and the user presses `Esc`
- **THEN** the diagram returns to the parent orchestrator's plan DAG

#### Scenario: Esc at the root is a consumed no-op

- **WHEN** the cursor is at the root with nothing fanned and the user presses `Esc`
- **THEN** nothing changes and focus stays in the plan pane (Esc does not reach the rail)

### Requirement: Live plan node icons are 1:1 with the rail (area 6)

A LIVE plan node's status icon (glyph AND style, including the animated spinner for a genuinely-active node) SHALL be identical to what the rail's status icon renders for the same role, computed through a SINGLE shared classifier so the two surfaces can never drift — not a parallel glyph table. The shared vocabulary: ready-to-close → review clipboard; needs-input → the needs-input glyph (so a worker blocked on a prompt is actionable from the DAG); done → `✓`; genuinely-active → the animated spinner (the plan view recomputes the frame at draw so it animates in lockstep); idle → moon-outline; live-quiet → moon-stars. Two plan-view-specific overlays the rail has no concept of: a PLANNED (never-bound) node renders the `○` circle, and a FAILED node (bound task result reports failure) renders `✕`. The header Status line uses the same resolved icon. The animated-spinner re-resolution applies ONLY when the shared classifier actually resolved to the spinner; a higher-precedence signal (notably needs-input on a still-active in_progress role) resolves to its STATIC glyph and the node SHALL NOT animate, so it renders 1:1 with the rail's `?` rather than swapping in the spinner frame.

#### Scenario: A live node's icon equals the rail's

- **WHEN** a live worker role is in any status (done / working / idle / in-review / needs-input)
- **THEN** its plan node renders the same glyph and style the rail's status icon renders for that role, and a working node animates

#### Scenario: Needs-input outranks active without animating (BUG-012)

- **WHEN** a live worker role's bound task is in_progress (genuinely active) AND the role also needs input (blocked on a prompt, or a descendant in its subtree does)
- **THEN** its plan node renders the static needs-input `?` glyph and style — identical to the rail row — and is NOT flagged animated, so the widget does not swap the `?` for the live spinner frame at draw

#### Scenario: Planned and failed overlays

- **WHEN** a node is a never-bound planned role, or a bound role whose task reports failure
- **THEN** the planned node renders `○` and the failed node renders `✕`

### Requirement: Plan nodes render as boxes with a double-line border selection cue (area 6)

The plan diagram SHALL render each UNSELECTED node as a padded single rounded box (`╭─╮ / │ glyph short-id │ / ╰─╯`) and the SELECTED node (the box under the cursor) as a DOUBLE-LINE border box (`╔═╗ / ║ glyph short-id ║ / ╚═╝`) in its OWN state colour — bold when the widget owns focus, plain weight when it does not (the focused distinction is weight, not hue). Both selected and unselected boxes draw the border in the node's state colour, and the content (glyph and label) always keeps its state colour; there SHALL be no dedicated selection colour and no background fill. Selection is conveyed purely by border weight (double vs single), so it survives any state colour — a selected DONE (green) node shows a green double border, distinguishable from a green single-rounded done node. A collapsed group under the cursor SHALL keep its dashed identity but render with a HEAVY dashed border (`┏╍╍┓ / ╏ … ╏ / ┗╍╍┛`) instead of the light dashed border, so selection reads without losing the collapsed/expandable signal. Each stage's box row SHALL be centered horizontally within the diagram region and the whole block centered vertically when it fits.

A dedicated selection colour is deliberately NOT used: green would collide with the green DONE state. A background fill is deliberately NOT used: a terminal background is whole-cell, but the border glyph sits mid-cell, so a fill paints gray around the border line and escapes the visual box.

#### Scenario: The cursor's box renders a double-line border with no fill

- **WHEN** the cursor is on a plan node
- **THEN** that node's box renders a double-line border in its state colour (bold when focused) with state-coloured content and no selection background fill, while a non-cursor box renders a single rounded border in its state colour

#### Scenario: A selected done node is distinguishable from an unselected done node

- **WHEN** the cursor is on a done (green) node and another done node is not selected
- **THEN** the selected node renders a green DOUBLE-line border and the unselected node renders a green single rounded border — distinguishable by border weight despite sharing the green state colour

#### Scenario: Cursor member box renders a double-line border in a fanned group

- **WHEN** the cursor is on a member of a fanned-out group
- **THEN** that member's box renders a double-line border in its state colour (no fill) and the other member boxes render single rounded borders in their state colour

### Requirement: Fanned group visually expands into member boxes (area 6)

When a parallel group is fanned out, the diagram SHALL render its members as individual node boxes inside a SOLID rounded enclosure (matching the design), laid out horizontally; the enclosure SHALL carry the members' common role token rendered vertically down its left inner edge and a `▲` collapse affordance at its top-right. The row's centering SHALL account for the wider expanded width. Every member with an out-of-group (downstream) edge SHALL carry a `↘` on its box. A collapsed group SHALL still render as a dashed range box.

#### Scenario: Fanning a group expands the diagram into member boxes

- **WHEN** the cursor is on a collapsed group and the user presses `Enter`
- **THEN** the diagram replaces the range box with one box per member inside a solid rounded enclosure carrying a `▲` collapse affordance, and the range-box label is no longer drawn for that stage

#### Scenario: A collapsed group stays a dashed range box

- **WHEN** a group is not fanned out
- **THEN** the diagram renders it as the collapsed dashed range box

### Requirement: Plan diagram has a footer hint and scrolls to the cursor (area 6)

The plan diagram SHALL render a dim footer hint bar on its bottom row (`↑↓ stage · ←→ within · Enter fan · Esc back`). Because boxes are taller than single-line chips, when the stacked block exceeds the diagram region the view SHALL scroll vertically so the cursor's stage box is fully visible; when the block fits, no scroll is applied.

The diagram SHALL also maintain a HORIZONTAL viewport that follows the cursor. When the widest stage block overflows the diagram width, the view SHALL scroll horizontally so the SELECTED node's box (its border included) is fully within the visible x-range — on every cursor change (`←→` within a stage, `↑↓` between stages, group fan-out, sub-coordinator drill-in, and the refresh re-anchor). When every stage fits the width, no horizontal scroll is applied and each stage stays centered. The selected node's box positions are reused from the dagview-derived stage layout (no relayout-to-fit, no wrapping, no shrinking). When a stage's sibling content is hidden past the left or right pane edge, a dim `‹` / `›` edge indicator SHALL be drawn at that edge on the stage's row. All painting SHALL be clipped to the diagram region (no `screen.Sync`); the ensure-visible logic keeps the selected node whole.

#### Scenario: Footer hint renders

- **WHEN** the plan diagram is drawn
- **THEN** a dim nav-legend footer row is present

#### Scenario: The diagram scrolls to keep the selected node visible

- **WHEN** the plan has more stages than fit the region and the cursor is on the last stage
- **THEN** the last stage's box is rendered within the region and the first stage has scrolled out of view

#### Scenario: A wide stage scrolls horizontally to the selected node

- **WHEN** a stage has more sibling nodes than fit the pane width and the cursor is moved with `←→` onto a node past the right edge
- **THEN** the selected node's box is rendered fully within the diagram region (scrolled into view), and a node off the opposite edge is not painted

#### Scenario: Edge indicators reflect off-screen content

- **WHEN** a stage's sibling nodes extend past the right pane edge
- **THEN** a dim `›` indicator is drawn at the right edge of that stage's row; a `‹` indicator is drawn at the left edge once the view has scrolled right; and when every stage fits the width no edge indicator is drawn

#### Scenario: Scrolling back reveals the left node

- **WHEN** the cursor is scrolled to a right-edge node in a wide stage and then moved back left with `←` to the first node
- **THEN** the first node's box is rendered fully within the diagram region

### Requirement: Plan cursor and fan-out survive a model refresh (area 6)

The Hera view re-projects the selected coordinator's plan on every refresh tick. A re-projection of the SAME orchestrator's plan SHALL preserve the operator's cursor position and fanned-group state: when the projected structure is unchanged the cursor and fan-out are untouched; when the structure changed (a cascade step materialized a node, a state flipped) the cursor SHALL re-anchor to the same node (or collapsed group, by its member set) when it still exists and clamp into the new layout when it vanished, and every still-present fanned group SHALL stay fanned. A genuine selection change (a different coordinator) or a drill-in push/pop SHALL still reset the cursor.

#### Scenario: Refresh preserves the cursor and fanned group

- **WHEN** the operator has moved the plan cursor and fanned out a group, and a refresh tick re-projects the same coordinator's plan
- **THEN** the cursor stays where the operator put it and the fanned group stays fanned

#### Scenario: Selecting a different coordinator resets the cursor

- **WHEN** the operator selects a different coordinator
- **THEN** the plan cursor resets to the first stage

### Requirement: Coordinator Details renders rich coordinator metadata (area 6)

For a coordinator selection the system SHALL render, in the read-only `" Details "` roster panel and in addition to the coordinator status line and the Agents roster, a metadata block describing the coordinator's group: **Created** (the orchestrator's creation time), **Last activity** (the maximum over the orchestrator's creation time, each role's creation time, each role's live-binding start time, and each role's status-update time), **Agent** (the coordinator's bound argus task name, omitted when the coordinator is unbound), **Worktree** (the coordinator's live-binding worktree path, omitted when absent, shortened to its trailing `project/task` components when it overflows the available width), and **Repos in scope** (the distinct argus projects across the orchestrator's roster roles, sorted, rendered as a `(none)` line when empty). The system SHALL also render a reserved **Summary** field showing an `(auto-generated overview coming soon)` placeholder after the roster.

Every field SHALL be derived from the already-built rail model projection (no Draw-time I/O), so the Details pane never disagrees with the rail and stays safe on the tview main thread. Time fields SHALL render an en-dash placeholder when zero. The existing roster and the embedded `" Orchestration Tree "` graph SHALL remain unchanged — the metadata block is purely additive.

Derived from: `internal/tui/hera/details.go` (`deriveCoordMeta`, `Draw`, `ContentHeight`), `internal/tui/hera/model.go` (`OrchView`/`RoleView` projection fields, `BuildModel`).

#### Scenario: Created and Last activity render
- **WHEN** a coordinator with a live binding and at least one role status update is selected
- **THEN** the Details pane shows a `Created:` line with the orchestrator's creation time and a `Last activity:` line equal to the most recent of the orchestrator/role creation times, the live-binding start, and the role-status update

#### Scenario: Repos in scope are distinct and sorted
- **WHEN** a coordinator's roster roles span argus projects `b`, `a`, and `a`
- **THEN** the `Repos in scope:` block lists `a` and `b` once each, in sorted order

#### Scenario: Agent and Worktree come from the coordinator role
- **WHEN** the selected coordinator's role is bound to an argus task with a name and a worktree path
- **THEN** the Details pane shows an `Agent:` line with that task name and a `Worktree:` line with that worktree path (shortened when it overflows the pane)

#### Scenario: Unbound coordinator omits Agent and Worktree
- **WHEN** the selected coordinator has no live binding
- **THEN** the `Agent:` and `Worktree:` lines are omitted while Created, Last activity, Repos in scope, the roster, and the Summary placeholder still render

#### Scenario: Summary placeholder is reserved
- **WHEN** any coordinator is selected
- **THEN** the Details pane ends with a `Summary:` field showing `(auto-generated overview coming soon)`

#### Scenario: ContentHeight matches the rendered row budget
- **WHEN** the Details region sizes the roster panel via `ContentHeight()`
- **THEN** the returned height equals the exact number of rows `Draw` emits for the current selection, including the metadata block and the Summary placeholder

### Requirement: Global key shortcuts are focus-gated on the Hera tab (area 5)

While the Hera tab is active AND focus is inside a content pane (the coordinator pane or the agent/details region — i.e. NOT the rail), the global key handler SHALL NOT consume the shortcuts it otherwise intercepts in the task-list mode: `q` (quit), `1`/`2`/`3` (tab switch), `?` (help), `Ctrl+C` (quit), and `Ctrl+L` (screen Sync). These keys SHALL instead fall through to `HeraPage`, which forwards them to the focused pane's PTY, because a focused pane is a live terminal. While the RAIL holds focus these globals SHALL continue to apply (the rail is not a content pane), so the operator escapes a pane with `Ctrl+Q` to use them again. This mirrors how the agent view (`modeAgent`) surrenders the same keys to its PTY.

Derived from: `internal/tui/app.go` (`App.heraPaneFocused`), `internal/tui/app.go` (`handleGlobalKey` rune-switch guard + `Ctrl+C`/`Ctrl+L` guards).

#### Scenario: q is typed into a focused pane, not a quit

- **WHEN** the Hera tab is active, a content pane holds focus, and the user presses `q`
- **THEN** argus does not quit — the key falls through to the focused pane's PTY

#### Scenario: Ctrl+C interrupts the focused agent instead of quitting

- **WHEN** the Hera tab is active, a content pane holds focus, and the user presses `Ctrl+C`
- **THEN** argus does not quit — `Ctrl+C` is delivered to the focused pane's PTY (interrupt the agent)

#### Scenario: tab-switch and help digits reach the pane

- **WHEN** the Hera tab is active, a content pane holds focus, and the user presses `1`, `2`, `3`, or `?`
- **THEN** the tab does not switch and help does not open — each key falls through to the focused pane's PTY

#### Scenario: rail focus keeps the globals

- **WHEN** the Hera tab is active and the RAIL holds focus
- **THEN** `q` quits, `1`/`2`/`3` switch tabs, and `?` opens help (the rail is not a content pane, so the globals are not gated)

### Requirement: `J` adopts a freelancer or re-parents a coordinator

While the RAIL is focused, pressing `J` SHALL act on the current selection:

- A FREELANCER selection (a `freelance`-kind role row carrying a live argus task) SHALL open a target picker listing the active (non-archived) orchestrators.
- A COORDINATOR selection SHALL open a target picker. A coordinator selection is any of: a `coordinator`-kind role row; an orchestrator header whose orchestrator has a coordinator role and is not archived; OR a non-archived `worker`-kind role row that BRIDGES a child orchestrator (a nested sub-coordinator — its `Selection.BridgeChildOrchID` is non-zero, the SAME field the `Ctrl+D` cascade reads). For the worker-bridge case the picker SHALL target the bridged CHILD orchestrator, not the parent worker role. The picker SHALL include, as a sentinel row at the TOP, a "Detach (make top-level)" entry that detaches the coordinator to top-level, followed by the OTHER active orchestrators (excluding the coordinator's own orchestrator) as re-parent targets.
- Any other selection (a PLAIN managed worker role with no bridged child, an empty selection, an archived row) SHALL surface visible feedback that only a freelancer or coordinator can be adopted, and SHALL NOT create or change any role or binding (never a silent no-op).

The picker SHALL be a themed, focusable, dismissable modal in which typed characters narrow the list by case-insensitive substring on the orchestrator name, `Enter` selects the highlighted orchestrator, and `Esc` cancels without change. The picker SHALL name the row being adopted in its title. For a FREELANCER, when no eligible target orchestrator exists, pressing `J` SHALL surface visible feedback that a coordinator must be created first and SHALL NOT open the picker or create any role or binding. For a COORDINATOR the picker SHALL always open (the detach sentinel is always offered), so there is no "no eligible target" feedback path for a coordinator.

`J` SHALL be RAIL-focus-only. In a COORD or AGENT pane the `J` rune SHALL forward to the bound task's PTY like any other character; the lowercase `j` navigation key SHALL be unaffected. The adopt/reparent/detach role+binding writes are cheap local SQLite mutations and run synchronously on the tview event loop, consistent with the other rail mutations (rename/archive/pin/status/delete); they do NOT touch a worktree or session, so they never perceptibly block the loop. (This differs from worker SPAWN, which creates a worktree + PTY session and is therefore dispatched off-thread.)

#### Adopt (freelancer → worker)

Selecting an orchestrator for a freelancer SHALL adopt the freelancer's argus task into it by creating, server-side and without any agent action, through the SAME transactional DAO `hera_join`'s attach-mode and the born-bound spawn use (`CreateHeraRoleWithBinding`, not a duplicate implementation), so a binding-insert failure (e.g. a worktree-orchestrator uniqueness collision) rolls the freshly-created worker role back — no orphan role:

- a `worker` role under the chosen orchestrator whose name defaults to the freelancer's name and is de-collided (a numeric suffix appended) when an active role of that name already exists; the role SHALL record the freelancer's argus repo as its `argus_project`; and
- a live binding from the freelancer's argus task to that role, recording the freelancer's argus-task worktree path.

The freelancer's argus task SHALL be best-effort stamped `meta:hera.role=worker` for parity with `hera_join`; a transient failure to stamp SHALL NOT undo or fail the binding. The adopt SHALL be REJECTED with visible feedback, creating no role or binding, when: the freelance row has no argus task id; or the task already holds a live binding under the chosen orchestrator (a duplicate).

#### Re-parent (coordinator → sub-coordinator)

Selecting a parent orchestrator for a coordinator SHALL re-parent it by creating a `worker` role under the chosen parent bound to the coordinator's coordinator argus task — the multi-binding the orchestration tree renders as a nested sub-coordinator. The coordinator's whole subtree moves with it (the subtree derives from the coordinator, which is untouched). The coordinator argus task + worktree SHALL be resolved from the coordinator role's LATEST binding (live, else most-recent ended) so a dormant coordinator can still be re-parented. The coordinator may be selected either as a top-level coordinator (role row or orchestrator header) OR as an already-nested sub-coordinator (a worker-bridge row); both resolve the same CHILD orchestrator id and route the same re-parent op, so a nested sub-coordinator can be moved between parents.

The re-parent SHALL be REJECTED with visible feedback when the chosen parent IS the coordinator's own orchestrator, or is a descendant of it (a cycle), or the coordinator has no coordinator role / no binding to re-parent.

**Teardown invariant (BUG-026):** before creating the new link, the re-parent SHALL end EVERY prior parent-link of the coordinator's task by ROLE id — both LIVE and ENDED. Live parent-link bindings SHALL be ended with reason `reparented`; then every distinct parent-link role (any role other than the coordinator's own coordinator role, reached through any binding of that task) SHALL be deleted so its bindings cascade away. This guarantees that repeated re-parents never pile up de-collided duplicate link roles (`name`, `name-2`, `name-3`, …); exactly one clean link remains. The teardown is the SAME single-source operation that detach (below) runs without recreating a link.

#### Detach (coordinator → top-level)

Selecting the "Detach (make top-level)" sentinel for a coordinator SHALL un-nest it back to a root orchestrator with no parent, WITHOUT creating any new link. Detach resolves the coordinator's argus task + coord role from the coordinator role's LATEST binding (the same resolution re-parent uses), then runs ONLY the teardown invariant above (end every live parent-link binding with reason `detached`, then delete every distinct parent-link role so its bindings cascade) — it recreates NO link. The coordinator's own coordinator role and its coordinator binding are NEVER touched, so the coordinator and its whole subtree survive intact, now at top-level. Detach SHALL be reachable for an already-nested sub-coordinator: such a coordinator is selected as a worker-bridge row, whose `Selection.BridgeChildOrchID` resolves the CHILD orchestrator to detach.

Detach SHALL be IDEMPOTENT: a coordinator that is already top-level (no parent-link roles) SHALL be a clean no-op (no error, no role or binding changed). Detach SHALL be REJECTED with visible feedback only when the orchestrator no longer exists, or the coordinator has no coordinator role / no binding to resolve its task.

#### Scenario: `J` on a freelancer creates a worker binding under the chosen coordinator

- **WHEN** the operator selects a freelancer, presses `J`, and picks an orchestrator
- **THEN** a `worker` role and a live binding from the freelancer's argus task to that role MUST be created under the chosen orchestrator

#### Scenario: The default role name is de-collided

- **WHEN** the freelancer's name matches an existing active role name under the chosen orchestrator
- **THEN** the adopted role MUST be created under a de-collided name (a numeric suffix appended) rather than failing or colliding

#### Scenario: An already-bound task is not adopted again under the same orchestrator

- **WHEN** the freelancer's argus task already has a live binding under the chosen orchestrator
- **THEN** the adopt MUST be rejected with visible feedback and MUST NOT create a second binding under that orchestrator

#### Scenario: `J` on a freelancer with no argus task id surfaces feedback

- **WHEN** the operator presses `J` on a freelance row that carries no live argus task
- **THEN** the view MUST surface visible feedback and MUST NOT create any role or binding

#### Scenario: `J` re-parents a coordinator under the chosen parent

- **WHEN** the operator selects a coordinator (role row or orchestrator header), presses `J`, and picks a different orchestrator (not the detach sentinel)
- **THEN** a `worker` role under the chosen parent bound to the coordinator's coordinator argus task MUST be created, nesting the coordinator's subtree under the parent

#### Scenario: `J` re-parents an already-nested sub-coordinator selected as its bridge row

- **WHEN** the operator selects a sub-coordinator that is currently nested under a parent (rendered as a worker-bridge row whose `Selection.BridgeChildOrchID` is the child orchestrator), presses `J`, and picks a different orchestrator
- **THEN** the CHILD orchestrator (not the parent worker role) MUST be re-parented under the chosen orchestrator via the same re-parent op (its prior parent links torn down, one clean link to the new parent created)

#### Scenario: Re-parenting ends all prior parent-links by role id

- **WHEN** a coordinator that is already nested under some parent (with a live link, and a leftover ended link role from a prior move) is re-parented under a new parent
- **THEN** the prior live link binding MUST be ended with reason `reparented` AND every prior parent-link role MUST be deleted, so exactly one clean link to the new parent remains (no de-collided duplicate link roles accumulate)

#### Scenario: Re-parent rejects a self or descendant target (cycle)

- **WHEN** the operator tries to re-parent a coordinator under itself or under one of its own sub-coordinators
- **THEN** the re-parent MUST be rejected with visible feedback and MUST NOT create any role or binding

#### Scenario: `J` detaches a nested coordinator to top-level

- **WHEN** the operator selects a coordinator that is currently nested under a parent (a parent-link role + live binding), presses `J`, and picks the "Detach (make top-level)" sentinel
- **THEN** every parent-link binding of the coordinator's task MUST be ended (reason `detached`) and every distinct parent-link role MUST be deleted, so the coordinator holds no live parent link and is top-level again; the coordinator's own coordinator role and binding MUST be untouched

#### Scenario: `J` detaches an already-nested sub-coordinator selected as its bridge row

- **WHEN** the operator selects an already-nested sub-coordinator — rendered as a headerless worker-bridge row whose `Selection.BridgeChildOrchID` is the child orchestrator — presses `J`, and picks the "Detach (make top-level)" sentinel
- **THEN** the CHILD orchestrator MUST be detached to top-level (its parent links torn down) — the detach path MUST be reachable for a worker-bridge selection, not only for a coordinator-header or coordinator-role selection

#### Scenario: Detaching an already-top-level coordinator is an idempotent no-op

- **WHEN** the operator picks "Detach (make top-level)" for a coordinator that is already top-level (no parent-link roles)
- **THEN** the detach MUST be a clean no-op — no error, no role or binding changed — and the coordinator's own coordinator role and binding remain intact

#### Scenario: A coordinator picker always offers detach

- **WHEN** the operator presses `J` on a valid coordinator, even when no OTHER active orchestrator exists to re-parent under
- **THEN** the picker MUST still open with the "Detach (make top-level)" sentinel available (the coordinator path has no "no eligible target" feedback)

#### Scenario: `J` on a non-adoptable row surfaces feedback

- **WHEN** the operator presses `J` while a PLAIN managed worker role (no bridged child), an empty selection, or an archived row is selected
- **THEN** the view MUST surface visible feedback and MUST NOT create any role or binding

#### Scenario: No eligible target orchestrator surfaces feedback (freelancer)

- **WHEN** the operator presses `J` on a valid freelancer but no eligible (non-archived) target orchestrator exists
- **THEN** the view MUST surface visible feedback that a coordinator must be created first and MUST NOT open the picker or create any role or binding

#### Scenario: `J` in a pane forwards to the PTY

- **WHEN** focus is in a COORD or AGENT pane and the operator types `J`
- **THEN** the `J` rune MUST be forwarded to the bound task's PTY and MUST NOT open the picker

### Requirement: The rail supports a `/` substring name filter

While the RAIL is focused, pressing `/` SHALL enter a rail search INPUT mode. While in input mode, typed characters SHALL build a filter query and the rail SHALL narrow to the rows whose name matches the query by case-insensitive substring, across coordinators, agents (workers and sub-coordinators), and freelancers. Whitespace-separated query terms SHALL each match the row name (AND semantics — every term must be a substring for the row to match); an empty or all-whitespace query SHALL match every row (no narrowing until a real term is typed).

`/` SHALL be RAIL-focus-only: when focus is in a COORD or AGENT pane the `/` rune SHALL be forwarded to the bound task's PTY and MUST NOT enter filter mode (the focus-gating contract).

While a filter is active (the query is non-empty) the rail SHALL remain ancestry-preserving and legible against the nested tree:

- An orchestrator (a root coordinator OR a nested sub-orchestrator) whose name matches, OR which has any descendant role or sub-orchestrator whose name matches, SHALL remain visible so a matching agent always keeps its parent coordinator header. A bridging worker row SHALL remain visible when it bridges a visible sub-orchestrator.
- Collapsed nodes (orchestrators, per-coordinator Archive expandos) and the Freelance / bottom-Archive sections SHALL auto-expand while a filter is active so matching rows are never hidden behind a fold. The persisted fold state MUST be left unchanged and restored when the filter is cleared.
- A section header (`Pinned`, `Freelance (N)`, the bottom `Archive (N)`) or a separator rule SHALL render only when it has at least one visible row beneath it, so the operator never lands on an empty section.
- A coordinator/orchestrator heading row (an orchestrator header, or a worker-bridge/pinned-breadcrumb row standing in for a nested coordinator) that is visible ONLY because a descendant matches — its own name, or its folded-in coordinator's name, does NOT itself match the query — is an ANCESTRY-ONLY heading: it SHALL NOT be a valid cursor target (arrow navigation and first-match auto-select skip it entirely) and SHALL render visually dimmed so it is obvious it cannot be selected. A heading whose own name (or folded-in coordinator's name) DOES match the query remains a normal, selectable, non-dimmed row.

The FIRST real match (a selectable row that is not an ancestry-only heading, not a structural fold header) in the narrowed rows SHALL auto-select live on every query change (typing or backspacing), so the operator sees the top candidate highlighted without needing to navigate to it. Up/Down SHALL move the cursor within the narrowed set while remaining in search input mode (so typing/backspacing continues to work), landing only on rows that are themselves a match.

`Esc` while in input mode SHALL exit search and restore the full, unfiltered rail (clearing the query). `Enter` while in input mode SHALL resolve against the CURRENTLY selected row (the auto-selected first match, or wherever Up/Down moved it) — reattaching/entering that row's pane exactly as a normal (non-filtering) Enter would — and SHALL THEN fully clear the filter (query reset, input mode off, full unfiltered rail restored) in the SAME keystroke. There is no intermediate "accepted but still narrowed" resting state: Enter always both selects and clears, never merely one or the other.

The active query SHALL be shown unobtrusively: a `/ <query>` input line at the top of the rail while typing, and the active query reflected in the rail border title while typing.

While in input mode the rail's mutation keys (`w`/`r`/`a`/`s`/`S`/`P`/`Ctrl+D`) SHALL NOT fire, and the global rune shortcuts (`1`/`2`/`3` tab-switch, `q` quit, `?` help) SHALL NOT fire; those keystrokes are filter input instead. `Enter` is the one exception — it is intercepted and handled as select-and-clear (above) rather than falling through as filter input.

Derived from: `internal/tui/hera/rail.go` (filter state, filter-aware `buildRows`, ancestry-only heading detection, first-match auto-select, `/ <query>` line, dynamic title), `internal/tui/hera/page.go` (`handleRailMutation`'s Enter-while-filtering branch: select then `Rail.ClearFilter()`), `internal/tui/app.go` (global rune-shortcut guard mirroring `a.tasklist.Filtering()`).

This Hera-rail filter is intentionally Hera-rail-scoped: the Tasks-tab (`internal/tui/taskview`) `/` filter keeps its own independent two-step (type → Enter locks → navigate → Enter selects) convention and is NOT changed by this requirement.

#### Scenario: `/` narrows the rail to matching rows

- **WHEN** the operator presses `/` while the rail is focused and types a query
- **THEN** the rail MUST show only rows whose name matches the query (case-insensitive substring, every whitespace-separated term), hiding non-matching coordinators, agents, and freelancers

#### Scenario: A matching nested agent keeps its parent coordinator visible

- **WHEN** a filter matches an agent (or sub-orchestrator) whose name does not match its parent coordinator's name
- **THEN** the parent coordinator header (and any intermediate bridging worker rows) MUST remain visible and expanded so the matching row is shown nested under it, rendered as a dimmed, non-selectable ancestry-only heading

#### Scenario: A collapsed node containing a match auto-expands

- **WHEN** a filter matches a role nested under an orchestrator the operator had collapsed
- **THEN** that orchestrator MUST render expanded while the filter is active, AND its persisted collapsed state MUST be restored unchanged once the filter is cleared

#### Scenario: Empty sections are pruned

- **WHEN** a filter is active and no Freelance (or Archive) member matches
- **THEN** the Freelance (or Archive) section header and its separator rule MUST NOT render

#### Scenario: The first real match auto-selects while typing

- **WHEN** the operator types (or backspaces) a query that narrows the rail
- **THEN** the cursor MUST move onto the first real match in the narrowed rows (never an ancestry-only heading or a structural fold header) without any further keypress

#### Scenario: An ancestry-only coordinator heading is skipped by navigation and auto-select

- **WHEN** a filter matches only a descendant of a coordinator/orchestrator heading, so the heading itself is shown purely for ancestry context
- **THEN** that heading row MUST render dimmed, MUST NOT be reachable by Up/Down arrow navigation, and MUST NOT be chosen by first-match auto-select

#### Scenario: A coordinator heading whose own (or folded-in coordinator's) name matches is a real, selectable match

- **WHEN** the query matches an orchestrator's own name, or the name of its folded-in coordinator role
- **THEN** that orchestrator's header row MUST be treated as a real match — selectable, not dimmed, and eligible for first-match auto-select

#### Scenario: Enter selects the current match, jumps into it, and clears the filter in one step

- **WHEN** the operator presses `Enter` while in search input mode, with the cursor resting on a real match (auto-selected or arrow-navigated)
- **THEN** the rail MUST reattach/enter that row's pane exactly as a normal Enter would, AND the filter MUST fully clear (query reset, input mode off) in the SAME keystroke — no second Enter is required

#### Scenario: Esc restores the full rail

- **WHEN** the operator presses `Esc` while in search input mode
- **THEN** the filter MUST clear, input mode MUST exit, and the rail MUST render every row it showed before the filter

#### Scenario: Mutation and global keys are filter input while typing

- **WHEN** the operator is in search input mode and types a character that is otherwise a rail mutation key (`a`, `w`, `P`, …) or a global shortcut (`1`, `2`, `q`, `?`)
- **THEN** that character MUST be appended to the filter query and MUST NOT trigger the mutation, switch tabs, quit, or open help

#### Scenario: `/` in a pane forwards to the PTY

- **WHEN** focus is in a COORD or AGENT pane and the operator types `/`
- **THEN** the `/` rune MUST be forwarded to the bound task's PTY and MUST NOT enter rail filter mode

### Requirement: The rail persists its fold and selection state across restarts

The system SHALL persist the Hera rail's UI state to the argus daemon database and
restore it when the rail is reconstructed, so the operator's view survives an argus /
TUI restart and a daemon restart or crash. The persisted state SHALL include:

- the set of COLLAPSED orchestrators (per-node fold state in the post-nesting tree),
- the set of OPEN per-coordinator `Archive (N)` expandos,
- the Freelance-section and bottom-Archive-section fold booleans, and
- the current SELECTION (the rail's stable row identity — role id, or the negated
  orchestrator id for a coordinator header).

Persistence SHALL use the existing `config` key-value table under a single key
(`hera.rail_view_state`), serialized as JSON — NO new table and NO schema migration.
Only non-default fold entries SHALL be serialized (orchestrators default expanded,
expandos default closed), so a saved value can always round-trip correctly.

**First-run (no saved state):** When the stored blob is absent or empty, the rail
SHALL start FULLY COLLAPSED on the first non-empty model build — all orchestrators
collapsed, Archive section collapsed (existing default). This one-shot seed fires
only once; after the first model build the live cursor and toggle state take over.
Subsequent model rebuilds SHALL NOT re-collapse orchestrators the operator expanded.

A malformed blob (present but not parseable as JSON) keeps the expanded default
(NOT the first-run collapse) — malformed data is treated as a corruption, not a
first launch. A load error also keeps defaults, logged but never fatal.

Restore SHALL apply the fold maps and section booleans immediately on construction
(they key off stable database ids, valid before any model is loaded) and SHALL apply
the selection after the first model build, reusing the rail's existing cursor-restore
machinery. The selection restore SHALL be ONE-SHOT: once applied (or once the operator
moves the cursor), later rebuilds keep the live cursor, not the stale persisted ref.

Saving SHALL occur on every fold toggle and selection move. In remote mode (no local
database) the persistence seam SHALL be absent and the rail SHALL operate normally
without persisting. Transient FILTER state (the `/` query and input mode) is NOT part
of the persisted state, and a filter-driven rebuild SHALL NOT trigger a save.

Derived from: `internal/tui/hera/rail.go` (`RailStateStore` seam, `railViewState`,
load-on-store-set, one-shot pending-selection restore, save-on-change), `internal/db/hera_rail_state.go`
(`LoadRailState`/`SaveRailState` over the `config` table), `internal/tui/app.go`
(local-only `*db.DB` wiring).

#### Scenario: Fold and selection survive a restart

- **WHEN** the operator collapses some orchestrators, opens a per-coordinator Archive expando, folds the Freelance section, and selects a row, then argus restarts and reopens the Hera tab
- **THEN** the rail MUST restore exactly those collapsed orchestrators, that open expando, the Freelance fold, and the selected row

#### Scenario: First run starts fully collapsed

- **WHEN** no persisted rail state exists (absent or empty stored blob) and the first model with orchestrators is loaded
- **THEN** the rail MUST start fully collapsed — all orchestrators collapsed, Archive section collapsed
- **AND** the fully-collapsed state MUST be one-shot: subsequent model rebuilds MUST keep the live fold state, not re-collapse

#### Scenario: Malformed value falls back to defaults

- **WHEN** the stored value is present but not valid JSON (malformed or truncated)
- **THEN** the rail MUST fall back to its defaults (orchestrators expanded, expandos closed, Freelance expanded, Archive collapsed) without error
- **AND** the first-run collapse MUST NOT fire (malformed data is treated as prior state, not first launch)

#### Scenario: Selection restore is one-shot

- **WHEN** the persisted selection is applied on the first model build and the operator then moves the cursor
- **THEN** a subsequent model rebuild MUST keep the live cursor and MUST NOT snap back to the persisted selection

#### Scenario: Remote mode operates without persistence

- **WHEN** the rail runs in remote mode (no local database seam)
- **THEN** the rail MUST function normally and MUST NOT attempt to load or save rail state

#### Scenario: Filter changes are not persisted

- **WHEN** the operator activates or clears the `/` name filter (which rebuilds the rail)
- **THEN** no rail-state save MUST be triggered by the filter change (filter state is transient)

### Requirement: `w` and `n` use the full new-task modal (area 4)

The system SHALL open the SAME modal as the new-argus-task popup (project / branch / backend / model / prompt / optional name, with project and skill autocomplete) for both the rail `w` (spawn worker) and `n` (new coordinator) keys. The project field SHALL default to the selected coordinator's project for `w`, and to the current selection's coordinator project (else the last-selected Tasks-tab project) for `n`. The modal SHALL return to the Hera tab on submit or cancel (not the Tasks tab). On submit:

- `w` spawns a born-bound worker under the selected coordinator's orchestrator via the shared `agent.SpawnHeraWorker` primitive, carrying the form's project, branch, backend, model, and prompt. When the optional name is non-blank, it SHALL name the worker task/role (overriding the prompt-derived name); when blank, the worker name derives from the prompt as before.
- `n` creates a NEW top-level orchestrator + coordinator role bound to a freshly created argus task via the shared `agent.SpawnHeraCoordinator` primitive. When the optional name is non-blank, it SHALL name BOTH the new orchestrator and the coordinator task (overriding the prompt-derived, de-collided orchestrator name); when blank, the orchestrator name is derived from the prompt and de-collided as before. The coordinator role is named `coord` in both cases.

The worker/coordinator spawn runs off the tview main thread (it creates a worktree + session) and refreshes the rail on completion. A spawn with no live coordinator (for `w`) surfaces visible feedback and does nothing.

Derived from: `internal/tui/heraactions.go` (`heraSpawnWorker`, `heraNewCoordinator`), `internal/agent/hera_spawn.go` (`SpawnHeraWorker`, `SpawnHeraCoordinator`), `internal/tui/newtaskform.go`.

#### Scenario: `w` spawns a worker from the full modal

- **WHEN** a coordinator is selected and the user presses `w`, fills the modal, and submits
- **THEN** a born-bound worker is spawned under that coordinator's orchestrator with the form's project/branch/backend/model/prompt, and the rail refreshes

#### Scenario: `n` creates a new root coordinator

- **WHEN** the user presses `n`, fills the modal, and submits
- **THEN** a new top-level orchestrator + `coord` coordinator role is created, bound to a freshly created argus task — not nested under the current selection

#### Scenario: `w` with no live coordinator is feedback-only

- **WHEN** the user presses `w` on a selection whose orchestrator has no live coordinator
- **THEN** the status bar shows a "no live coordinator" error and no worker is spawned

#### Scenario: `w` with an explicit name names the worker

- **WHEN** the user presses `w`, enters a non-blank name in the modal, fills the rest, and submits
- **THEN** the spawned worker's task/role name is the entered name rather than a prompt-derived name

#### Scenario: `n` with an explicit name names the orchestrator and coordinator task

- **WHEN** the user presses `n`, enters a non-blank name in the modal, fills the rest, and submits
- **THEN** the new orchestrator's name (the rail label) AND the coordinator task's name are the entered name rather than a prompt-derived name, and the coordinator role is still named `coord`

#### Scenario: blank name derives from the prompt as before

- **WHEN** the user presses `w` or `n`, leaves the name field blank, fills the rest, and submits
- **THEN** the worker / orchestrator name is derived from the prompt exactly as it was before the optional name field existed

### Requirement: Cmd+Up / Cmd+Down move rail selection without changing pane focus

The Hera view SHALL intercept `KeyUp` / `KeyDown` events carrying the `ModCtrl|ModAlt` modifier (the mod-7 encoding iTerm2 maps `Cmd+↑` / `Cmd+↓` onto) in `HeraPage.InputHandler` BEFORE forwarding any key to a focused content pane.

When intercepted the handler SHALL call `rail.CursorUp()` or `rail.CursorDown()` and return, consuming the event so the mod-7 escape sequence never reaches the focused pane's PTY.

The `FocusMachine` state (rail / coordinator pane / agent pane) SHALL remain unchanged.

The binding SHALL appear in the Hera rail section of the help overlay (`?`) and in the README Reference keybinding table.

#### Scenario: Cmd+Down from coordinator pane moves rail cursor

- **WHEN** the Hera tab is active and focus is on the coordinator pane
- **WHEN** the user presses `Cmd+Down` (KeyDown + ModCtrl|ModAlt)
- **THEN** the rail cursor advances to the next selectable row
- **THEN** the focus machine state remains FocusCoord

#### Scenario: Cmd+Up from agent pane moves rail cursor

- **WHEN** the Hera tab is active and focus is on the agent pane
- **WHEN** the user presses `Cmd+Up` (KeyUp + ModCtrl|ModAlt)
- **THEN** the rail cursor retreats to the previous selectable row
- **THEN** the focus machine state remains FocusAgent

#### Scenario: Keystroke is not forwarded to the PTY

- **WHEN** the user presses `Cmd+Down` while focused on a content pane
- **THEN** the mod-7 byte sequence `\x1b[1;7B` is NOT written to the pane session's input

### Requirement: A present-but-dead pane session is re-resolved, not just a nil one (area 6)

The system SHALL, on every tick reconcile and on every keystroke into a focused
pane, treat a pane that holds a session whose `Alive()` is false the same as an
unbound pane: it SHALL re-resolve the live session via the `SessionResolver`
seam and swap in a fresh handle, so a pane whose stream the daemon tore down
(StreamLost relay / daemon bounce) WHILE the agent process is still alive becomes
interactive again WITHOUT a full TUI restart. A dead handle SHALL be replaced
ONLY by a genuinely live handle whose pointer differs from the dead one; when the
resolver yields nothing (the process is really gone, so on-disk log replay backs
the pane) or the SAME not-yet-evicted handle (the client cache has not evicted
it; re-resolve on a later tick), the pane SHALL be left untouched so the emulator
is not needlessly reset. A live, present session SHALL be left alone (this never
restarts or navigates a live coordinator — that remains the `Enter`-reattach
path's responsibility).

Every genuine swap onto a different session handle (this reconcile
replacement, and `bindPane`'s task-changed rebind) SHALL reset the pane's
VT/replay state (`ResetVT`) before attaching the new handle, so no emulator
state, cached replay content, or scroll anchor left over from the outgoing
session can survive into the incoming session's render — the same
`SetTaskID→ResetVT→SetSession` order the main (non-Hera) agent view already
follows on every task/session transition.

Derived from: `internal/tui/hera/panes.go` (`reconcileOne` dead-handle branch, `bindPane`, `reconcileSessions`, `paneBinding`), `internal/daemon/client/client.go` (`Get` re-dials on a cache-miss when the daemon reports the process alive).

#### Scenario: A dead pane session is replaced on the next tick

- **WHEN** a pane holds a session that has gone `!Alive()` and the provider can resolve a fresh live handle for the same task
- **THEN** the next reconcile swaps the fresh handle in (re-armed PTY resize) and the pane is interactive again

#### Scenario: A dead session with no live replacement is retained

- **WHEN** a pane holds a `!Alive()` session and the provider returns nil (process gone) or the same dead handle (cache not yet evicted)
- **THEN** the pane keeps its current handle (its buffered output still backs log replay) and is not reset

#### Scenario: A same-task session swap (e.g. `recycle_coord`) leaves no stale render state

- **WHEN** a pane's bound task is recycled — the session dies and a fresh, distinct live handle for the SAME task ID is resolved on a later reconcile
- **THEN** the swap resets the pane's VT/replay state before attaching the fresh handle, so the fresh session's render cannot show any cell or replay content carried over from the outgoing session

### Requirement: A dropped pane keystroke is logged, not silently swallowed (area 6)

The system SHALL, when a keystroke is forwarded to a focused pane whose session
is nil or `!Alive()`, emit a uxlog line (rather than dropping silently), attempt
an immediate re-resolve of the pane's session, and retry the write on the
re-resolved handle so the keystroke is not lost when a fresh live session is
available; if no live session can be resolved, it SHALL log that the keystroke
was dropped.

Derived from: `internal/tui/hera/panes.go` (`forwardKey` re-resolve + uxlog).

#### Scenario: Keystroke into a dead pane re-resolves and is delivered

- **WHEN** a key is forwarded to a pane whose bound session went `!Alive()` and a fresh live handle is resolvable
- **THEN** the pane re-resolves the fresh handle and the keystroke is written to it

#### Scenario: Keystroke into a pane with no live session is dropped with a log line

- **WHEN** a key is forwarded to a pane with no live session and none can be resolved
- **THEN** the keystroke is dropped, a uxlog line records the drop, and nothing is written to the dead handle

### Requirement: Bottom bar shows focus-aware hints while the Hera tab is active

The system SHALL render different hotkey hint sets in the bottom status bar
depending on which Hera region holds keyboard focus:

- **Rail focused** (default, `heraFocus == 0`): rail nav + mutation keys — `j/k nav`, `SP fold`, `/ filter`, `Tab pane`, `w spawn`, `n coord`, `s/S status`, `R retire`, `C prune`, `^r prune-all`, `^d del`, `? help`, `q quit`.
- **Coordinator or agent pane focused** (`heraFocus == 1` or `2`): pane keys — `^Q rail`, `Tab pane`, `^Z fullscreen`, `Cmd+↑↓ rail nav`, `Sh+↑↓ scroll`, `? help`. `q` and `1/2/3` are intentionally omitted: when a pane is focused those keys reach the PTY, not argus globals.

Hints update on the same frame as a focus change (keyboard or mouse). Other
tabs are unaffected. Key names match `modal/help.go` "Hera View (rail)"
section exactly — no undocumented keys are surfaced.

Derived from: `internal/tui/widget/statusbar.go` (`SetHeraFocus`, `heraFocus`
field, `Draw()` TabHera case), `internal/tui/hera/page.go` (`OnFocusChange`,
`notifyFocusChange()`, defer in `InputHandler`, click notify in `MouseHandler`),
`internal/tui/app.go` (`heraPage.OnFocusChange` wiring, `switchToHeraTab2`
reset).

#### Scenario: Rail-focused hints include spawn and filter keys

- **GIVEN** the Hera tab is active and the rail holds focus
- **WHEN** the bottom bar renders
- **THEN** the hint row includes `spawn`, `filter`, `fold`, and `retire` — the mutation keys absent from the prior static hint set

#### Scenario: Pane-focused hints include rail and scroll keys

- **GIVEN** the Hera tab is active and a coordinator or agent pane holds focus
- **WHEN** the bottom bar renders
- **THEN** the hint row includes `rail` (Ctrl+Q) and `scroll` — and does NOT include `spawn` or `filter`

#### Scenario: Hints update on Tab keypress

- **GIVEN** the Hera tab is active with the rail focused
- **WHEN** the user presses Tab to advance to the coordinator pane
- **THEN** the statusbar `heraFocus` field is set to 1 (FocusCoord) on the same frame, reflecting the new focus region

#### Scenario: Tab entry resets to rail hints

- **GIVEN** the operator has a pane focused on a previous Hera tab visit
- **WHEN** they switch away and return to the Hera tab
- **THEN** the statusbar resets to `heraFocus == 0` (rail hints) because the focus machine always starts on the rail

### Requirement: Left arrow moves rail cursor to parent coordinator when rail is focused

The Hera view SHALL intercept `KeyLeft` in `HeraPage.InputHandler` ONLY when `focus.State() == FocusRail`. When intercepted, the handler SHALL call `rail.CursorToParent()` and return, consuming the event.

When a content pane is focused (`FocusCoord` or `FocusAgent`), `KeyLeft` SHALL NOT be intercepted — it SHALL pass through to `forwardKey` and reach the pane's PTY unchanged.

#### `Rail.CursorToParent()` algorithm

Starting from the current cursor row, walk backwards through the flattened row list. Stop at the first row that satisfies ALL of:

1. `row.depth < currentRow.depth` (strictly smaller depth)
2. `row.kind == rrOrch` OR (`row.kind == rrRole` AND `row.collOrchID > 0`)

Call `setCursor(i)` on the matching row. If no such row exists (cursor is at root, or no qualifying ancestor), the method is a no-op.

The `FocusMachine` state SHALL remain unchanged in all cases.

The binding SHALL appear in the Hera rail section of the help overlay (`?`) and in the README Reference keybinding table.

#### Scenario: Left from worker moves cursor to parent orchestrator header

- **GIVEN** the Hera rail is focused and the cursor is on a worker row (depth > 0)
- **WHEN** the user presses `←`
- **THEN** the cursor moves to the nearest ancestor `rrOrch` or bridging `rrRole` row with smaller depth
- **THEN** the `FocusMachine` state remains `FocusRail`

#### Scenario: Left from root row is a no-op

- **GIVEN** the Hera rail is focused and the cursor is on a row with depth 0 and no qualifying ancestor above it
- **WHEN** the user presses `←`
- **THEN** the cursor does not move
- **THEN** the `FocusMachine` state remains `FocusRail`

#### Scenario: Left from pane-focused state passes through to PTY

- **GIVEN** the Hera tab is active and a content pane (coordinator or agent) is focused
- **WHEN** the user presses `←`
- **THEN** the rail cursor does NOT move
- **THEN** the `FocusMachine` state does NOT change
- **THEN** the key is forwarded to the pane's PTY via `forwardKey`

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

### Requirement: Two resting states — hide (Tier 1) and nuke (Tier 2) (area 7)

The Hera rail SHALL offer exactly two end-of-life resting states for a role or orchestrator, both of which retain every DB row (the bedrock rule: a hera row is never hard-deleted):

- **HIDDEN (Tier 1)** — reached by `a`. The row is `archived_at`-stamped on the hera ROLE only (NOT `nuked_at`, and NOT `db.SetArchived` on the argus task — HIDE is rail-only, so the worker keeps running and still shows in the Tasks tab) and renders inside its PARENT coordinator's nested "Archive (N)" expando. Hiding a bridging sub-coordinator collapses its WHOLE subtree into that expando (structure retained — the sub-coord's agents nest beneath it inside the expando), never leaking a descendant to a top-level root. The worktree and session stay ALIVE (no detach). It is a reversible toggle (un-hide restores it exactly to the rail) and is NOT confirmed. `a` applies to a WORKER or a sub-coordinator only; on a top-level coordinator (no parent to nest under) it surfaces feedback and is a no-op.
- **NUKED (Tier 2)** — reached by `Ctrl+D` (any worker or coordinator, including top-level) or `C` (the selected coordinator's hidden descendants). The row is `nuked_at`-stamped and is REMOVED from the rail entirely — it appears in no visible archive. Its worktree + local/remote branch are reclaimed from disk and its session stopped; its DB rows (role + orchestrator + inbox + argus task) are retained. Recovery is via the DB only (re-spin a fresh worktree); a nuked role's inbox stays readable.

`C` SHALL be scoped to the SELECTED coordinator's archive — it nukes every Tier-1 hidden item under that coordinator (equivalent to `Ctrl+D` on each), never a global sweep, and is confirmed with a count. When the selected coordinator's archive is empty it surfaces "nothing to clear" and opens no confirm.

Derived from: `internal/tui/heraactions.go` (`heraHide`, `heraClearArchive`), `internal/tui/hera/ops.go` (`Hide`/`Unhide` toggle, `NukeRole`/`NukeOrchestrator`), `internal/tui/hera/model.go` (BuildModel skips db rows with `nuked_at` set), `internal/db/hera.go` (`nuked_at`).

#### Scenario: Hide keeps the session and worktree alive (rail-only)

- **WHEN** the user presses `a` on a live worker
- **THEN** the worker's hera role is archived and renders in its parent coordinator's nested archive expando, while its session, worktree, and argus task row are all left untouched (the task still appears in the Tasks tab), and pressing `a` again un-hides it exactly

#### Scenario: Hiding a sub-coordinator collapses its subtree into the parent's archive

- **WHEN** the user presses `a` on a bridging sub-coordinator that has its own nested agents
- **THEN** the sub-coordinator and its whole subtree fold into the parent coordinator's "Archive (N)" expando — its agents render nested beneath it inside the expando when it is opened, are hidden when it is collapsed, and are never hoisted to a top-level root in either fold state

#### Scenario: Hide on a top-level coordinator is feedback-only

- **WHEN** the user presses `a` on a top-level coordinator / orchestrator header
- **THEN** the status bar shows a "hide applies to workers and sub-coordinators" message and nothing is changed (a top-level coordinator has no parent archive to nest under)

#### Scenario: Nuked rows are invisible to the rail

- **WHEN** a role or orchestrator is marked `nuked_at`
- **THEN** BuildModel omits it from every rail section (it is not shown in any archive); its DB row, inbox, and argus task are still retrievable from the DB

#### Scenario: Clear-this-coordinator's-archive nukes the hidden descendants

- **WHEN** the user presses `C` on a coordinator that has Tier-1 hidden descendants and confirms
- **THEN** each hidden descendant is NUKED (worktree + branch reclaimed unless bound live elsewhere, role marked nuked, sole-bound task archived) and the confirm modal showed the count; a coordinator with an empty archive shows "nothing to clear" and opens no confirm

### Requirement: Copy agent-staged clipboard from the focused pane (ctrl+y) (area 6)

The system SHALL bind `ctrl+y`, while a TERMINAL pane is focused (the coordinator
pane, or a worker/leaf agent pane in terminal mode — NOT the rail and NOT the
coordinator details/tree region), to copy the agent-staged clipboard payload for
THAT pane's bound argus task to the OS clipboard. Because the Hera view shows
several tasks at once, the payload SHALL be resolved from the FOCUSED pane's task
(`FocusedTerminalTaskID`), never a single global active task.

The interception SHALL be unconditional whenever a terminal pane is focused:
`ctrl+y` SHALL always be stolen from the PTY, regardless of whether a payload
is currently staged for the focused pane's task — it SHALL NEVER fall through
to the pane's PTY. When a payload is staged, the system SHALL clear the
daemon-side slot and flash a "Copied" notice, reusing the existing
`clipboardAccessor` (`ClipboardGet`/`ClipboardClear`) and `copyToClipboard` — no
second clipboard path is introduced. When the runner is not daemon-backed
(in-process fallback) or nothing is staged, `ctrl+y` SHALL flash a notice
indicating there is nothing to copy instead of copying, and SHALL still consume
the key. In remote mode the page is inert, so `ctrl+y` does nothing.

The per-tick staged-ness hint state (`clipReady`) no longer gates the
interception — it drives only the discoverability affordance described below.

When the focused terminal pane's task has a staged payload, the system SHALL
surface a discoverability affordance by appending a `(ctrl+y copy)` marker to
that pane's border title (the Hera-view analogue of the main agent view's header
hint), refreshed each tick for the single focused pane's task. The affordance
SHALL appear on at most the focused terminal pane and SHALL disappear when focus
leaves a terminal pane or nothing is staged.

Derived from: `internal/tui/hera/page.go` (`InputHandler` `ctrl+y` trap,
`OnCopyClipboard`, `clipReady`/`SetClipboardHint`, `Draw` border-title hint),
`internal/tui/hera/panes.go` (`FocusedTerminalTaskID`), `internal/tui/clipboard.go`
(`copyStagedClipboardForHeraPane`, `flashNotice`), `internal/tui/app.go`
(`OnCopyClipboard` wiring + `refreshHeraClipboardHint` tick),
`internal/tui/modal/help.go:70` (help overlay Hera section).

#### Scenario: Copy a staged payload from a focused worker pane

- **WHEN** a worker terminal pane is focused, a payload is staged for that pane's task, and the user presses `ctrl+y`
- **THEN** the staged payload is written to the OS clipboard, the daemon-side slot is cleared, a "Copied" notice flashes, and the key is consumed (not forwarded to the PTY)

#### Scenario: Copy is scoped to the focused pane's task

- **WHEN** the coordinator pane is focused and a payload is staged for the coordinator's task
- **THEN** `ctrl+y` copies the COORDINATOR task's payload (resolved from the focused pane), not any worker pane's payload

#### Scenario: ctrl+y is intercepted with a notice when nothing is staged

- **WHEN** a terminal pane is focused but no payload is staged for its task and the user presses `ctrl+y`
- **THEN** no copy occurs, a "nothing to copy" notice flashes, and the keystroke is consumed rather than forwarded to the pane's PTY

#### Scenario: ctrl+y on the rail or coordinator details is an inert no-op

- **WHEN** the rail or the coordinator details/tree region is focused and the user presses `ctrl+y`
- **THEN** nothing is copied (there is no terminal pane and no bound task to copy from)

#### Scenario: The focused pane advertises a staged payload

- **WHEN** the focused terminal pane's task has a staged payload
- **THEN** that pane's border title shows a `(ctrl+y copy)` affordance, which clears when focus leaves the pane or the payload is consumed/expires

#### Scenario: Help overlay lists the ctrl+y copy key

- **WHEN** the help overlay is opened
- **THEN** its "Hera View (rail)" section lists `ctrl+y` for copying staged text

### Requirement: Needs-input summary box above the rail (area 5)

The system SHALL draw a fixed one-line bordered "Needs input" summary box at the top of the rail column whenever one or more needs-input tasks have no presence in the Hera model, and SHALL reduce the rail's drawn height by the box height while it is shown. The box reports the count of such tasks as `"N tasks need input"` (or `"1 task needs input"` for a count of one). When no such task needs input the box has zero height and the rail occupies the full column.

The counted set is the needs-input set pushed by the App (`SetNeedsInput`) MINUS every argus task the Hera model knows: each role's live and structural binding (`TaskID` and `BridgeTaskID`) across the Pinned, Active, and Archived orchestrator sections and the Freelance section. Coordinators, managed workers (including those whose subtree row is folded — their cue already bubbles up via the subtree rollup), Hera freelance-roles, and tasks bound to ARCHIVED roles (their `BridgeTaskID` survives the binding ending) are therefore never counted; only tasks invisible from the Hera tab are.

The App SHALL feed the box only needs-input tasks that are currently `in_progress`. The needs-input scan is sticky (a finished task idling at its final prompt keeps the marker in its log tail), and both the task list (which shows `(?)` only on an in_progress task) and the per-role rollup gate on in_progress; the box applies the SAME gate so it never tallies a finished/unmanaged task that shows no `(?)` anywhere.

The box is a passive heads-up: it has no keybinding, no focus, and no click-to-jump. Geometry is computed in `Draw` (no tview.Flex, no `screen.Sync()`); the box and the rail each paint their full bounding rect through `widget.DrawBorderedPanel`. The text is left-padded one cell from the border. On a terminal too short to keep the rail usable the box yields and is not drawn. The box is never drawn in remote mode (the page short-circuits to its unavailable banner first).

Derived from: `internal/tui/widget/attentionsummary.go` (the widget + left padding), `internal/tui/hera/page.go` (`Draw` geometry + count), `internal/tui/app.go` (`needsInputInProgress` gate → `SetNeedsInput` feed), `internal/tui/hera/model.go` (managed-task-id walk over role `TaskID`/`BridgeTaskID`), `context/knowledge/gotchas/hera-view.md` (no-Sync / full-rect rules).

#### Scenario: An unmanaged needs-input task is summarised

- **WHEN** a task in the `(?)` needs-input state has no role binding anywhere in the Hera model
- **THEN** the box is drawn at the top of the rail column and the rail is shrunk by the box height

#### Scenario: Count text pluralises

- **WHEN** exactly one unmanaged task needs input
- **THEN** the box reads `"1 task needs input"`, and reads `"N tasks need input"` when N is greater than one

#### Scenario: Managed, folded, and freelance tasks are excluded

- **WHEN** the only needs-input tasks are a coordinator, a managed worker whose subtree row is folded, and a Hera freelance-role
- **THEN** the count is zero and the box is not drawn, because each is already represented in the rail

#### Scenario: A finished (non in_progress) unmanaged task is not counted

- **WHEN** the sticky needs-input set still contains an unmanaged task that has finished (its status is no longer in_progress)
- **THEN** the count excludes it and the box does not render — matching the task list, which shows `(?)` only while in_progress

#### Scenario: A task bound to an archived role is not counted

- **WHEN** a needs-input task is bound to an archived Hera role whose live binding has ended
- **THEN** the count excludes it, because the role's structural binding (`BridgeTaskID`) keeps Hera presence after the binding ends

#### Scenario: No unmanaged task needs input

- **WHEN** every needs-input task is known to the Hera model (or none needs input)
- **THEN** the box has zero height and the rail occupies the full column

#### Scenario: Box is never drawn in remote mode

- **WHEN** the Hera page is in remote mode
- **THEN** the summary box is not drawn (the page renders only its unavailable banner)

### Requirement: Enter in a focused pane revives its dead/suspended session (area 6)

The system SHALL, when `Enter` is pressed while a focused content pane (the
coordinator or agent terminal) has no live session attached — the state behind
the `Session not running - press Enter to start` overlay (session nil or
`!Alive()`) — fire that pane's reattach callback to revive the pane's bound task,
performing exactly what the overlay promises. The reattach SHALL route through the
SAME `OnReattach` → `App.heraReattach` path the rail's `Enter` uses (a dead session
is restarted via `startSession`; a live coordinator stays navigate-only). The
agent pane SHALL target the selected worker's task; the coordinator pane SHALL
target the orchestrator's coordinator task it is showing.

For EVERY other case the pane SHALL leave key handling unchanged: a non-`Enter`
key, or `Enter` while the session IS alive, SHALL fall through so live PTY input
reaches the agent. The reattach callback SHALL be nil-guarded so a consumer that
does not wire it (the task page, which mounts the same widget) keeps the pane
inert — its live-session input continues to flow through its own routing
(`handleAgentKey`), never through the pane's `InputHandler`.

This introduces NO new keybinding — `Enter`-to-revive is already the rail's
documented behavior; this extends it to the focused pane, so the help overlay and
README key tables are unchanged.

Derived from: `internal/tui/terminal/terminalpane.go` (`OnReattach` field, `InputHandler` Enter-when-not-alive gate), `internal/tui/hera/panes.go` (`forwardKey` dead-branch routes to the pane `InputHandler`, `bindPane` wires `OnReattach`, `reattachPane` per-pane target), `internal/tui/heraactions.go` (`heraReattach`).

#### Scenario: Enter in a focused dead-session pane revives it

- **WHEN** a content pane is focused, its session is nil or `!Alive()` (the "Session not running" overlay is showing), and the user presses `Enter`
- **THEN** the pane's reattach callback fires and the App revives the pane's bound task (a dead session restarts via `startSession`)

#### Scenario: Enter in a focused live pane reaches the agent, not the revive path

- **WHEN** a content pane is focused, its session is alive, and the user presses `Enter`
- **THEN** the reattach callback does NOT fire and `Enter` is forwarded to the agent PTY

#### Scenario: A non-Enter key in a focused dead-session pane does not revive

- **WHEN** a content pane is focused with no live session and a key other than `Enter` is pressed
- **THEN** the reattach callback does NOT fire (the key is handled exactly as before)

#### Scenario: The task page keeps the pane inert

- **WHEN** the same `TerminalPane` widget is mounted by the task page, which does not wire `OnReattach`, and `Enter` is pressed with no live session
- **THEN** the nil-guarded callback makes the pane a no-op, unchanged from prior behavior

### Requirement: Per-role pin state is projected into the read model (area 2)

The system SHALL project each hera role's pin state into the read model so the rail can render a pinned non-root role. `buildRoleView` SHALL set `RoleView.Pinned` true when the role's `hera_roles.pinned_at` is set and false otherwise. The existing `P` mutation path (`Ops.PinToggle` → `db.PinHeraRole` / `UnpinHeraRole`) is unchanged — it already stamps/clears `pinned_at` for a selected role; this requirement only adds the missing read projection that made the pin a silent no-op.

Derived from: `internal/tui/hera/model.go` (`buildRoleView`), `internal/db/hera.go` (`PinHeraRole`/`UnpinHeraRole`), `internal/tui/hera/ops.go` (`PinToggle`).

#### Scenario: Pinned role projects a true flag

- **WHEN** a role's `hera_roles.pinned_at` is set
- **THEN** its `RoleView.Pinned` is true

#### Scenario: Unpinned role projects a false flag

- **WHEN** a role's `hera_roles.pinned_at` is NULL
- **THEN** its `RoleView.Pinned` is false

### Requirement: Pinned non-root roles render as a two-line breadcrumb entry (area 2/3)

The system SHALL float a pinned non-root role OUT of its parent subtree and render it in the Pinned section as a two-line entry: line 1 is a SELECTABLE breadcrumb row showing the role's status glyph (dimmed) followed by its lineage trail, and line 2 is a NON-SELECTABLE continuation row showing the role's name. The lineage trail SHALL be the orchestrator-name chain from the root down to and including the role's own orchestrator (`role.OrchID`), derived from the rail's `canonicalParents` nesting so the trail matches how the rail nests the subtree, joined with `" › "`. An over-wide trail SHALL be left-truncated with a leading `…` so the nearest parent stays visible (rune-aware). A pinned role whose own orchestrator is itself pinned SHALL NOT float (it stays nested under the pinned orchestrator). A pinned role's orchestrator that cannot be resolved in the model SHALL NOT be floated (skipped and logged) rather than rendered without lineage.

Derived from: `internal/tui/hera/rail.go` (`collectPinnedRoles`, `appendOrchWorkers` float-skip, `drawPinnedBreadcrumbRow`, the breadcrumb continuation draw), `internal/tui/hera/model.go` (`canonicalParents`, `OrchByID`), `anutron/hera` `internal/view/rail_list.go` (BUG-025 prior art).

#### Scenario: Pinned leaf role floats with a breadcrumb

- **WHEN** a worker role under an unpinned orchestrator is pinned
- **THEN** it renders in the Pinned section as a selectable breadcrumb line (dimmed glyph + lineage trail) immediately followed by a non-selectable name line, and does NOT also render nested under its orchestrator in the active tree

#### Scenario: Over-wide lineage trail is left-truncated

- **WHEN** a pinned role's lineage trail is wider than the rail
- **THEN** the trail is left-truncated with a leading `…`, keeping the nearest parent (rightmost text) visible

#### Scenario: Lineage trail spans the full canonical chain

- **WHEN** a pinned role's orchestrator is a sub-coordinator nested under a root
- **THEN** the breadcrumb trail shows the full chain `root › sub ›` derived from `canonicalParents`

#### Scenario: Role under a pinned orchestrator stays nested

- **WHEN** a role is pinned and its own orchestrator is also pinned
- **THEN** the role renders nested under the pinned orchestrator (it does NOT float to a standalone breadcrumb entry)

#### Scenario: Unresolvable parent is not floated

- **WHEN** a pinned role's orchestrator cannot be resolved in the model
- **THEN** the role is skipped from the Pinned block (logged), never rendered without lineage

### Requirement: A pinned sub-coordinator hoists its whole subtree (area 1/2)

The system SHALL, when the pinned non-root role is a bridging sub-coordinator (a worker row carrying a child orchestrator), hoist the role AND its whole nested subtree into the Pinned section: the breadcrumb + name entry renders, and the child orchestrator's subtree renders beneath it. The hoisted child orchestrator SHALL be marked placed so it renders exactly once — neither double-rendered in the active tree nor leaked to a top-level root by the safety sweep — in both collapsed and expanded fold states. The pinned sub-coordinator's breadcrumb row SHALL carry the child orchestrator id so Space folds the hoisted subtree and `Ctrl+D` cascades the whole nested sub-team (the same conservative cascade as when nested).

Derived from: `internal/tui/hera/rail.go` (`collectPinnedRoles`, `appendOrch`/`appendOrchWorkers` hoist + `placed` marking, `structuralReach`), `internal/tui/hera/model.go` (`Selection.BridgeChildOrchID`), `anutron/hera` `internal/view/rail_list.go` (BUG-021 prior art).

#### Scenario: Pinned sub-coordinator brings its subtree

- **WHEN** a bridging sub-coordinator role is pinned
- **THEN** it floats to the Pinned section with its child orchestrator's subtree rendered beneath it

#### Scenario: Hoisted subtree renders exactly once

- **WHEN** a pinned sub-coordinator's subtree is hoisted into the Pinned section
- **THEN** the child orchestrator does not also render in the active tree, in both collapsed and expanded fold states

#### Scenario: Folding and cascading a hoisted sub-coordinator

- **WHEN** the cursor is on a pinned sub-coordinator's breadcrumb line
- **THEN** Space folds its hoisted subtree and `Ctrl+D` cascades the whole nested sub-team

### Requirement: Cursor anchors on the breadcrumb line of a pinned entry (area 1)

The system SHALL anchor the rail cursor on the SELECTABLE breadcrumb line (line 1) of a two-line pinned entry; the continuation name line (line 2) is non-selectable and is skipped by cursor navigation. The cursor identity for a breadcrumb line SHALL be the role id, so after a rebuild the cursor re-pins onto the same pinned role's breadcrumb line via the existing selection-ref mechanism, and the continuation line never shadows that identity. A `P` press on a floated pinned role's breadcrumb line SHALL unpin it (returning it to its parent subtree).

Derived from: `internal/tui/hera/rail.go` (`selectable`, `currentRef`, `restoreCursor`, `SelectByTaskID`, `step`), `internal/tui/hera/ops.go` (`PinToggle`).

#### Scenario: Navigation skips the continuation line

- **WHEN** the cursor steps with `j`/`k` past a two-line pinned entry
- **THEN** it lands on the breadcrumb line, never on the non-selectable continuation name line

#### Scenario: Cursor re-pins onto a pinned breadcrumb after a rebuild

- **WHEN** the model is replaced while the cursor is on a pinned role's breadcrumb line
- **THEN** the cursor re-pins to that pinned role's breadcrumb line by role id

#### Scenario: Unpin from the breadcrumb line

- **WHEN** `P` is pressed on a floated pinned role's breadcrumb line
- **THEN** the role is unpinned and returns to its parent subtree on the next rebuild

### Requirement: Rail nests sub-orchestrators under their bridging worker row (area 1)

The system SHALL build the rail as a nested tree of display rows from the read-only model. Each root orchestrator (one with no bridging parent in the rendered set) renders at depth 0; an expanded orchestrator's directly-bound roles render at `depth+1`; and a sub-orchestrator renders indented beneath the row that bridges it, recursively. There are two nesting shapes:

- **Worker-bridged child:** when a parent WORKER row's bridge task equals the child's coordinator bridge task, the child's workers nest one level beneath that worker row (the worker row IS the child's coordinator surrogate — no separate child header).
- **Coordinator-spawned child:** when the parent's COORDINATOR is also the child's coordinator (the shared-coordinator shape — there is no worker row to host it), the child nests as its OWN collapsible sub-orchestrator header directly under the parent, at the worker depth, recursively.

Nesting consumes the corrected subtree (see "Multi-binding bridge keys off the latest binding"). A visited-orchestrator (`placed`) set guards cycles so each orchestrator is placed at most once. An archived sub-orchestrator reached through a bridge renders dimmed in place (it is NOT dropped from its parent's subtree, distinct from the bottom Archive section which lists archived ROOT orchestrators).

An archived WORKER that bridges a not-yet-placed child renders in place (its row dimmed) rather than being hoisted into the per-coordinator Archive expando, so its child sub-team still nests — a done sub-coordinator must not strand its live subtree at the top level. Only archived LEAF workers (bridging no unplaced child) fold into the expando. The archived bridging worker's ROW dims, but its child subtree dims only from inherited dim or the child's own archived state (an active child under an archived bridging worker stays normal).

A PINNED orchestrator is ALWAYS a top-level root (rendered in the Pinned section), even when a worker bridges it — user pin intent wins over nesting. After the root pass, a safety sweep places any active orchestrator left unplaced by a pure bridge cycle as a top-level root, so a cycle-orphaned orchestrator never vanishes from the rail.

Derived from: `internal/tui/hera/rail.go` (`buildRows`, `appendOrch`, `appendOrchWorkers`, `appendWorkerRow`, `workerBridgeChild`), `internal/tui/hera/model.go` (`coordBridgeChildren`), `docs/OLD-RAIL-SNAPSHOT.md` (target layout: 6 roots / 19 nested).

#### Scenario: Sub-orchestrator nests under its bridging worker

- **WHEN** worker task `T` under orchestrator P is also the coordinator (bridge task) of child orchestrator C
- **THEN** C renders indented beneath P's worker row for `T`, and C is not also rendered as a top-level root

#### Scenario: Cycle is placed once

- **WHEN** two orchestrators bridge each other (a cycle)
- **THEN** the visited-set places each orchestrator exactly once and the rail build terminates

#### Scenario: Pure-cycle orphan still surfaces as a root

- **WHEN** every orchestrator in a bridge cycle is consumed (bridged by another) so none qualifies as a root
- **THEN** the safety sweep renders the unplaced orchestrators as top-level roots — nothing vanishes from the rail

#### Scenario: Pinned orchestrator stays top-level when bridged

- **WHEN** a pinned orchestrator's coordinator task is also a worker under another orchestrator (it is bridged)
- **THEN** it still renders in the Pinned section at depth 0, not nested under the bridging worker

#### Scenario: Archived bridge renders dimmed in place

- **WHEN** a sub-orchestrator reached through a bridge is archived
- **THEN** it renders nested under its parent in the dimmed style rather than being dropped or hoisted to the bottom Archive section

#### Scenario: Coordinator-spawned sub-team nests as a sub-header

- **WHEN** the parent's coordinator is also the child's coordinator (no worker row bridges the child)
- **THEN** the child renders as its own collapsible sub-orchestrator header indented under the parent, foldable with Space, and not as a top-level root

#### Scenario: Archived bridging worker nests its live child in place

- **WHEN** a parent worker is archived but still bridges a not-yet-placed (live) child orchestrator
- **THEN** the worker row renders in place dimmed (not hoisted into the Archive expando) and its child nests beneath it, with the live child subtree rendered in the normal (non-dimmed) style

#### Scenario: Archived leaf worker folds into the expando

- **WHEN** an archived worker bridges no unplaced child orchestrator
- **THEN** it still folds into the per-coordinator `Archive (N)` expando rather than rendering in place

### Requirement: Multi-binding bridge keys off the latest binding with a teardown guard (area 1)

The system SHALL determine the parent→child bridge from each role's LATEST binding regardless of liveness, not the live binding alone. The in-memory rail/tree bridge SHALL match `db.SubtreeOrchIDs` exactly: a parent orchestrator P nests a child orchestrator C when C's coordinator's latest-binding task ALSO has a non-teardown latest binding under P through ANY of P's roles — a WORKER role (a spawned worker that became a sub-coordinator) OR P's own COORDINATOR role (the coordinator-spawned sub-team that `hera_new_orchestrator` creates, where one coordinator agent runs both P and C). The earlier in-memory bridge honoured only worker roles, so coordinator-spawned sub-teams rendered flat as extra top-level roots; matching `SubtreeOrchIDs`' ANY-parent-side-binding join closes that gap.

When P and C share the SAME coordinator bridge task (so `SubtreeOrchIDs` would symmetrically include each from the other — an A↔B cycle), the rail breaks the symmetry deterministically: the orchestrator whose coordinator role has the LOWER role id is the parent (it was created first), and the later one is the spawned sub-team that nests under it.

An ended binding still bridges UNLESS its `end_reason` is an operator-teardown reason (`reparented` or `user_deleted`); every other end reason (`argus_deleted`, `task_missing`, normal session end) leaves the structural link intact. The parent-side role's ARCHIVED state does NOT break the bridge (`SubtreeOrchIDs` has no archived-role filter on the parent side) — an archived worker that bridges a live child still nests it (see the rail-nesting requirement). This rule is applied identically by the DB-side `SubtreeOrchIDs` (TLDR roll-up) and the in-memory `workerTaskSet`/`heraTreeNodes`/`coordBridgeParentOf` (rail + Details tree). Archived CHILD orchestrators are still pruned as descendants in `SubtreeOrchIDs` and in the coordinator-bridge path.

Derived from: `internal/db/hera_subtree.go` (`heraSubtreeOrchIDs`), `internal/db/hera.go` (`ListHeraLatestBindings`, teardown-reason constants), `internal/tui/hera/tree.go` (`workerTaskSet`, `heraTreeNodes`), `internal/tui/hera/model.go` (`RoleView.BridgeTaskID`/`LinkEndReason`, `OrchView.CoordBridgeTaskID`, `coordBridgeParentOf`, `coordBridgeChildren`, `consumedSet`).

#### Scenario: Ended-but-not-torn-down bridge still nests

- **WHEN** a coordinator's binding has ended for a non-teardown reason (its task completed) and a parent worker's latest binding points at that coordinator's task
- **THEN** the child orchestrator still bridges and nests under the parent

#### Scenario: Torn-down link does not nest

- **WHEN** a parent worker's latest binding ended with reason `reparented` or `user_deleted`
- **THEN** that worker does NOT bridge the child orchestrator (the link is stale)

#### Scenario: Coordinator-spawned sub-team nests under the parent

- **WHEN** one coordinator agent's task is the coordinator of BOTH orchestrator P and orchestrator C (P's coordinator role id is lower than C's)
- **THEN** C nests under P as a sub-orchestrator and is NOT also rendered as a top-level root

#### Scenario: Shared-coordinator cycle is broken by earliest role id

- **WHEN** orchestrators P and C share a coordinator bridge task (a symmetric A↔B link)
- **THEN** only the orchestrator with the lower coordinator role id is a root and the other nests under it (never both as co-roots, never a hang)

### Requirement: Coordinator folds into the orchestrator header (area 3)

The system SHALL NOT render an orchestrator's coordinator-kind role as its own child row. `appendOrch` SHALL skip the coordinator role when listing children, and the orchestrator header row SHALL carry the coordinator's status glyph (the header IS the coordinator). A worker-less orchestrator therefore renders header-only.

Derived from: `internal/tui/hera/rail.go` (`appendOrch` coordinator skip, `drawOrchRow` coordinator glyph).

#### Scenario: No redundant coord child row

- **WHEN** an orchestrator with a coordinator role and one worker is expanded
- **THEN** the rail shows the orchestrator header plus the single worker row, with no separate `coord` child row

#### Scenario: Header carries the coordinator status glyph

- **WHEN** the orchestrator's coordinator role has a status
- **THEN** the orchestrator header row renders that coordinator's status glyph

### Requirement: Per-coordinator archive expando (area 2)

The system SHALL render an orchestrator's archived roles in a per-coordinator `Archive (N)` expando under that orchestrator's active agents, collapsed by default. This is distinct from the bottom Archive section, which lists archived ROOT orchestrators. The expando appears only when the orchestrator has at least one archived role and toggles with Space like other collapsible rows.

Derived from: `internal/tui/hera/rail.go` (per-orchestrator archive expando in `appendOrch`).

#### Scenario: Archived roles fold under their coordinator

- **WHEN** an orchestrator has active workers and archived workers
- **THEN** an `Archive (N)` expando renders under the active workers, collapsed by default, listing the archived roles dimmed when expanded

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

### Requirement: PR indicator on rail role rows (area 3)

The system SHALL render a `PR` indicator on a managed (non-coordinator) rail role row when that role's bound task carries a non-empty `url` in the daemon-populated `task_meta` "pr" namespace. The indicator is best-effort, read once per refresh via `ListMetaByNamespace("pr")` and threaded into the rail; it is never fetched by the view. It reuses the same cached `prMeta` the Details roster reads.

Derived from: `internal/tui/hera/rail.go` (PR cell in `drawRoleRow`), `internal/tui/hera/page.go` (`doRefresh` reads "pr", passes `prMeta` to the rail).

#### Scenario: PR mark on a managed rail row

- **WHEN** a managed role's bound task has a non-empty "pr" url
- **THEN** its rail row renders a `PR` indicator

### Requirement: Plan view archetype and model readout with missing-profile warning

The plan/DAG view SHALL display, for each node, the node's selected archetype and the model/effort
applied to it, so the operator can see what tiering each unit of work received. The view SHALL also
surface a warning decoration on a node or project that points at a missing or invalid profile, matching
the runtime fail-open behavior (the agent runs on the CLI default, and the operator is told why).

#### Scenario: Node shows archetype and applied model

- **WHEN** a plan node has archetype `code_slice` resolving to model `sonnet`
- **THEN** the node's rendering shows the `code_slice` archetype and the applied `sonnet` model

#### Scenario: Missing profile warned

- **WHEN** a project points at a profile name that is absent or fails validation
- **THEN** the plan/DAG view shows a warning indicating the profile is missing or invalid

### Requirement: Plan-DAG node description shows the mission's first lines (area 6)

The plan view SHALL, in the coordinator Details region's embedded `" Plan "`
graph, render a selected node's description as the first N (N ≈ 3) NON-EMPTY lines
of the role's stored prompt, wrapped/truncated to the detail-pane width, rather
than only the single first line. The header MAY grow to accommodate the additional
description rows, and the coordinator Details region SHALL keep laying out the
roster-over-graph split without overflow.

The render SHALL be POLICY-AGNOSTIC: it SHALL NOT strip, skip, or pattern-match
any organization/security policy text, and SHALL NOT assume any particular line
is boilerplate. When the role's stored prompt is empty the header SHALL render the
existing `"(no description)"` placeholder.

Derived from: `internal/tui/hera/plan.go` (`heraPlanNodesWithBridge`,
`Node.Description`), `internal/tui/planview/planview.go` (`nodeHeaderLines`).

#### Scenario: A multi-line mission shows several lines

- **WHEN** a plan node's stored prompt has multiple non-empty lines and the node is selected
- **THEN** the detail header renders the first few (≈3) lines of the prompt, wrapped to the pane, not just the first line

#### Scenario: The description is rendered verbatim, no policy stripping

- **WHEN** a plan node's stored prompt begins with organization/security policy text
- **THEN** the detail header renders those opening lines as-is, without stripping or skipping them (the fix for polluted prompts is upstream prompt hygiene, not view-side stripping)

#### Scenario: Empty prompt still shows the placeholder

- **WHEN** a plan node's stored prompt is empty
- **THEN** the detail header renders `"(no description)"`


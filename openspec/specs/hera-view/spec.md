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

### Requirement: Rail structure is a flat, single-level orchestrator list (area 1)

The system SHALL build the rail as a flattened list of display rows from the read-only model. Every active orchestrator is appended at depth 0 via `appendOrch(o, 0, false)`; an expanded orchestrator's roles are appended one level deeper (`depth+1`). The rail SHALL NOT nest sub-orchestrators under their parent-link worker rows.

Derived from: `internal/tui/hera/rail.go:152` (`buildRows`), `internal/tui/hera/rail.go:206` (`appendOrch`).

`NOTE:` This is a degradation from the plugin, which nested sub-orchestrators under their parent in the rail itself (the plugin's `resolveSubCoordinators` rewrote the flat list into a nested tree). Native renders cross-orchestrator nesting ONLY in the Details-pane orchestration tree (see area 6), never in the rail. Per `docs/RAIL-PARITY-ANALYSIS.md` (Gap #3) and `docs/NATIVE-HERA-FOLLOWUPS.md` ("rail is flat"), this is the largest visible parity gap: an operator with many orchestrators sees N flat rows where the plugin showed a handful of trees.

#### Scenario: Active orchestrators render at depth 0

- **WHEN** the model holds multiple active orchestrators
- **THEN** each orchestrator header renders at depth 0 and no orchestrator is indented beneath another

#### Scenario: Expanded orchestrator shows its own roles only

- **WHEN** an orchestrator is expanded
- **THEN** its directly-bound roles render indented under it, but child orchestrators (sub-coordinators bridged by a shared task) do NOT appear nested in the rail

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

The system SHALL render the rail in a fixed section order: a "Pinned" section (only when pinned orchestrators exist), the active orchestrators (no header, like the task list's active section), a "Freelance (N)" fold section (only when freelance roles exist), and an "Archive (N)" expando section at the bottom (only when archived orchestrators exist). The Freelance section defaults expanded; the Archive section defaults collapsed. When the model is entirely empty the rail renders a single "No hera orchestrators" placeholder row.

Derived from: `internal/tui/hera/rail.go:152` (`buildRows`), `internal/tui/hera/rail.go:88` (`NewRail` archive default), `internal/tui/hera/model.go:54` (Model sections).

#### Scenario: Pinned section appears only when populated

- **WHEN** at least one orchestrator is pinned
- **THEN** a "Pinned" header renders above the active list with the pinned orchestrators under it

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

The system SHALL choose a role row's status glyph by this precedence: (1) `ready_to_close` wins over everything with a distinct review glyph; otherwise (2) the hera role status when present — working, blocked, done, or idle each map to a distinct glyph/style; otherwise (3) binding presence (`Live`) renders a "live" glyph; otherwise (4) an unbound/dimmed glyph. `ready_to_close` is read from the task-addressed `task_meta` "hera" namespace, not the hera tables.

Derived from: `internal/tui/hera/rail.go:514` (`statusIcon`), `internal/tui/hera/model.go:214` (`buildRoleView` reads `ready_to_close`).

#### Scenario: ready_to_close overrides status

- **WHEN** a role's bound task carries `meta:hera.ready_to_close=true` AND the role status is working
- **THEN** the row renders the review/ready glyph, not the working glyph

#### Scenario: Hera role status drives the glyph

- **WHEN** a role has a status row of `blocked` and is not ready_to_close
- **THEN** the row renders the needs-input/blocked glyph

#### Scenario: Live-but-statusless role

- **WHEN** a role holds a live binding but has no status row and is not ready_to_close
- **THEN** the row renders the in-review "live" glyph rather than the unbound glyph

### Requirement: Rail keybindings (area 4)

The system SHALL bind the following keys while the rail holds focus: `j`/`k` and Down/Up move the cursor; Space collapses/expands the row under the cursor; `Tab`/`Backtab` and `Ctrl+Alt+Left`/`Ctrl+Alt+Right` walk the focus ladder; `Ctrl+Q` returns focus to the rail; `Enter` enters the selected role's pane (restarting a dead session first); `w` spawns a worker under the selected coordinator's orchestrator; `r` renames the selected role/orchestrator; `a` toggles archive; `P` toggles pin; `s`/`S` advance/revert the selected role's hera status; `Ctrl+D` deletes the selected role/orchestrator. Every bound key SHALL appear in the help overlay's "Hera View (rail)" section.

Derived from: `internal/tui/hera/rail.go:548` (rail `InputHandler`), `internal/tui/hera/page.go:288` (page `InputHandler` focus ladder), `internal/tui/hera/page.go:371` (`handleRailMutation`), `internal/tui/modal/help.go:70` (help overlay Hera section).

`NOTE:` Native deliberately OMITS several plugin rail keys: `n` (new orchestrator — canonical path is the `hera_new_orchestrator` MCP tool), `J` (adopt/reparent freelancer), `/` (rail name filter), `Ctrl+R` (hera-prune — Tasks-tab-only in native), `l` (toggle archived visibility), and `Ctrl+Z` (fullscreen pane). Plain Left/Right are unused by the rail (free for future horizontal nav). Per `docs/NATIVE-HERA-FOLLOWUPS.md`, these are known parity gaps; `Cmd+↑/↓` rail-selection-while-pane-focused collides at the byte level with agent-view task navigation and remains an unresolved rebinding decision.

#### Scenario: Mutation key acts on the current selection

- **WHEN** the rail is focused and the user presses `a` on a selected role
- **THEN** the archive-toggle callback fires for that role's `(role, orchestrator)` selection and the key does not leak to navigation

#### Scenario: Omitted plugin key is inert

- **WHEN** the user presses `/`, `n`, `J`, or `l` while the rail is focused
- **THEN** nothing happens (no filter, no new-orchestrator, no adopt, no archive-visibility toggle) because native binds none of them

#### Scenario: Help overlay lists every rail key

- **WHEN** the help overlay is opened
- **THEN** its "Hera View (rail)" section lists `j`/`k`, space, Tab/Ctrl+Q, Enter, `w`, `r`, `a`, `P`, `s`/`S`, and `Ctrl+D`

### Requirement: Three-region focus model and ladder (area 5)

The system SHALL track focus across three regions — rail, coordinator (HERA/middle) pane, and agent/details (right) region — via a focus machine that never lands focus on an absent region. `Advance`/`Retreat` step only through present regions; when a focused region disappears focus rebalances to the nearest present one (the rail is always present, so the fallback terminates). `Ctrl+Q` forces focus back to the rail. Mouse clicks focus the region under the cursor only if that region is present.

Derived from: `internal/tui/hera/focus.go:34` (`FocusMachine`), `internal/tui/hera/focus.go:68` (`rebalance`), `internal/tui/hera/focus.go:89` (`Advance`/`Retreat`), `internal/tui/hera/focus.go:131` (`SetRegion`), `internal/tui/hera/page.go:447` (`MouseHandler`).

#### Scenario: Advance skips an absent pane

- **WHEN** the agent region is absent (narrow terminal) and focus advances from the coordinator pane
- **THEN** focus does not move to the absent agent region (stays at the right-most present region)

#### Scenario: Focus rebalances off a disappearing region

- **WHEN** focus is on the agent region and that region becomes absent on the next layout
- **THEN** focus bumps to the coordinator pane if present, else to the rail

#### Scenario: Ctrl+Q returns to the rail

- **WHEN** any pane is focused and the user presses `Ctrl+Q`
- **THEN** focus returns to the rail

### Requirement: Three-region layout computed in Draw (area 5)

The system SHALL lay out the view as rail | coordinator pane | agent/details region, computing rects in `Draw` (not via a tview.Flex). The rail is a fixed width (`heraRailWidth`); the remaining width splits evenly between the coordinator pane and the agent region. When the terminal is too narrow for a right area the rail takes the full width and both right regions are marked absent so focus cannot land on them. Every region paints through `widget.DrawBorderedPanel`/`FillArea` to cover its full bounding rect, and the view SHALL NOT call `screen.Sync()` for content updates.

Derived from: `internal/tui/hera/panes.go:14` (`heraRailWidth`), `internal/tui/hera/page.go:176` (`Draw`), `internal/tui/hera/page.go:208` (present-flag reconciliation), `context/knowledge/gotchas/hera-view.md` (no-Sync rule).

#### Scenario: Narrow terminal hides the right regions

- **WHEN** the terminal is narrower than the rail width plus a usable right area
- **THEN** the rail fills the width and both right regions are marked absent

#### Scenario: Drawing the full view never calls Sync

- **WHEN** the view draws with live panes and a details region
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

Derived from: `internal/tui/hera/panes.go:59` (`applySelection` detailsMode), `internal/tui/hera/model.go:120` (`IsCoordinator`), `internal/tui/hera/details.go:23` (`DetailsView`), `internal/tui/hera/page.go:221` (Draw mode branch).

#### Scenario: Worker selection shows a terminal

- **WHEN** a worker role is selected
- **THEN** the right region shows the worker's live agent terminal

#### Scenario: Coordinator selection shows the details region

- **WHEN** a coordinator role is selected
- **THEN** the right region renders the Details roster (no terminal) stacked over the orchestration tree

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

The system SHALL apply each mutation to the SELECTED `(role, orchestrator)` from the rail cursor, never a bare task ID. Archive and pin toggles read the current row state from the store to choose direction. Pinning clears archived state (pin and archive are mutually exclusive). Rename surfaces a name-conflict error for the caller to display. Status advance/revert step the hera role status ladder (idle → working → blocked → done), clamped at the ends, and reaching `done` on a WORKER role also rolls the bound task to in_review (soft-fail). Mutations are thin adapters over existing store methods; the spawn path is the shared `agent.SpawnHeraWorker` primitive, run off the main thread.

Derived from: `internal/tui/hera/ops.go:85` (`ArchiveToggle`), `internal/tui/hera/ops.go:118` (`PinToggle`), `internal/tui/hera/ops.go:150` (`Rename`), `internal/tui/hera/ops.go:168` (`StepStatus`), `internal/tui/hera/ops.go:66` (status ladder), `context/knowledge/gotchas/hera-view.md` (M6c).

#### Scenario: Archive toggle reads current state

- **WHEN** the selected role is currently archived and the user presses `a`
- **THEN** the op unarchives it (direction is read from the store row, not the possibly-stale model flag)

#### Scenario: Worker reaching done rolls its task to in_review

- **WHEN** `s` advances a worker role to `done`
- **THEN** the hera status is set to done AND `RollHeraWorkerToReview` rolls the bound task to in_review (the roll is soft-fail so the status update always lands)

#### Scenario: Status step on an orchestrator header is a no-op

- **WHEN** `s`/`S` is pressed with the cursor on an orchestrator header (no role)
- **THEN** nothing happens (only roles carry status)

### Requirement: Conservative delete semantics for multi-binding safety (area 7)

The system SHALL destroy the underlying argus task + worktree when deleting a ROLE ONLY if that task has exactly one live binding; a multi-bound task is PRESERVED (deleting it would end another orchestrator's binding to the same task). Deleting an ORCHESTRATOR cascades its hera rows (roles + bindings + status) but PRESERVES every underlying argus task, so one keystroke can never wipe multiple live worktrees. Orphaned tasks are deleted individually from the Tasks tab. Delete (always) and archive-of-a-live-row gate behind a confirmation modal; rename and spawn-prompt use a text-input modal.

Derived from: `internal/tui/hera/ops.go:206` (`DeleteRole`), `internal/tui/hera/ops.go:216` (`DeleteOrchestrator`), `context/knowledge/gotchas/hera-view.md` (M6c delete semantics), `internal/tui/hera/page.go:374` (Ctrl+D → OnDelete).

`NOTE:` This is a documented divergence from the plugin's `DeleteRole`, which destroyed the task more aggressively. Native trades plugin parity for multi-binding safety. Native also has NO rail prune verb (the plugin's `Ctrl+R` "prune all done coords + agents" is Tasks-tab-only in native) — see `docs/NATIVE-HERA-FOLLOWUPS.md`.

#### Scenario: Deleting a sole-bound role destroys the task

- **WHEN** a role is deleted and its task has exactly one live binding
- **THEN** the role row is removed and the underlying argus task + worktree are destroyed

#### Scenario: Deleting a multi-bound role preserves the task

- **WHEN** a role is deleted and its task holds live bindings in more than one orchestrator
- **THEN** only that role's row is removed; the task and its other-orchestrator binding survive

#### Scenario: Deleting an orchestrator preserves its tasks

- **WHEN** an orchestrator is deleted
- **THEN** its roles, bindings, and status rows cascade away but every underlying argus task remains as an unbound task

### Requirement: Enter restarts a dead session then enters the pane (area 7)

The system SHALL, on `Enter` over a selected role with a bound task that has no live session, fire the reattach callback to restart the session, then advance focus into the pane. A live row just advances focus; an empty selection only advances focus.

Derived from: `internal/tui/hera/page.go:376` (Enter arm in `handleRailMutation`).

#### Scenario: Enter on a dead session reattaches

- **WHEN** Enter is pressed on a role whose task has no live session
- **THEN** the reattach callback fires and focus advances into the pane

### Requirement: Freelance roles hoisted into a top-level section (area 8)

The system SHALL hoist active freelance-kind roles into a top-level "Freelance" rail section rather than nesting them under their orchestrator. `BuildModel` skips active freelance-kind roles when filling an orchestrator's roles and appends them to `Model.Freelance`, sorted by name. The native view SHALL NOT provide a manual adopt/reparent affordance.

Derived from: `internal/tui/hera/model.go:189` (freelance hoist), `internal/tui/hera/model.go:206` (sort), `internal/tui/hera/rail.go:174` (Freelance section).

`NOTE:` This is a reduced interpretation of the plugin's Freelance concept. The plugin derived freelance entries from UNMANAGED live argus tasks and grouped them by repo, and offered a `J` key to adopt a freelancer into a chosen coordinator. Native's read-only hera store has no unmanaged-task data source, so the Freelance section reflects only roles explicitly created with kind `freelance` (via `hera_join`), and the `J` adopt key is omitted. The former auto-adopt watcher (Milestone 4 rule D4) was retired with the `depends_on` DAG — born-bound workers create their bindings transactionally at spawn time, so there is no link to adopt across. Only the daemon-startup binding reconciliation survives (see `hera-coordination`). Per `docs/NATIVE-HERA-FOLLOWUPS.md`, restoring an adopt picker is an open follow-up.

#### Scenario: Freelance role appears in its own section

- **WHEN** a role of kind `freelance` is active under some orchestrator
- **THEN** it renders in the top-level "Freelance (N)" section, not nested under that orchestrator

#### Scenario: No adopt affordance

- **WHEN** the operator looks for a way to adopt/reparent a freelancer from the rail
- **THEN** there is none (`J` is unbound); adoption is not a native rail operation

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

#### Scenario: Both panels render together

- **WHEN** a coordinator is selected and the region is tall enough
- **THEN** the `" Details "` roster and the `" Plan "` graph both render, with no `" DAG "` or `" Orchestration Tree "` title

#### Scenario: Enter on a plain leaf node jumps to its role within the Hera view

- **WHEN** the details region is focused and the user presses Enter on a plain leaf node (not a group, not a sub-coordinator)
- **THEN** the system selects that node's role in the Hera rail (by its bound task id) and moves focus to that role's agent pane, staying on the Hera tab (it SHALL NOT switch to the Tasks view)

#### Scenario: Enter on a leaf node whose session is dead restarts and joins it

- **WHEN** the user presses Enter on a plain leaf node whose backing agent session has exited (no live session in the runner)
- **THEN** the system restarts-and-joins that session, identically to the rail's Enter — firing the same reattach under the same liveness gate (a dead session of any role, or a live non-coordinator role, fires it; a live coordinator stays navigate-only) — so the node never merely selects without restarting

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

The plan diagram SHALL render a dim footer hint bar on its bottom row (`↑↓ stage · ←→ within · Enter fan · Esc back`). Because boxes are taller than single-line chips, when the stacked block exceeds the diagram region the view SHALL scroll vertically so the cursor's stage box is fully visible; when the block fits, no scroll is applied. All painting SHALL be clipped to the diagram region (no `screen.Sync`).

#### Scenario: Footer hint renders

- **WHEN** the plan diagram is drawn
- **THEN** a dim nav-legend footer row is present

#### Scenario: The diagram scrolls to keep the selected node visible

- **WHEN** the plan has more stages than fit the region and the cursor is on the last stage
- **THEN** the last stage's box is rendered within the region and the first stage has scrolled out of view

### Requirement: Plan cursor and fan-out survive a model refresh (area 6)

The Hera view re-projects the selected coordinator's plan on every refresh tick. A re-projection of the SAME orchestrator's plan SHALL preserve the operator's cursor position and fanned-group state: when the projected structure is unchanged the cursor and fan-out are untouched; when the structure changed (a cascade step materialized a node, a state flipped) the cursor SHALL re-anchor to the same node (or collapsed group, by its member set) when it still exists and clamp into the new layout when it vanished, and every still-present fanned group SHALL stay fanned. A genuine selection change (a different coordinator) or a drill-in push/pop SHALL still reset the cursor.

#### Scenario: Refresh preserves the cursor and fanned group

- **WHEN** the operator has moved the plan cursor and fanned out a group, and a refresh tick re-projects the same coordinator's plan
- **THEN** the cursor stays where the operator put it and the fanned group stays fanned

#### Scenario: Selecting a different coordinator resets the cursor

- **WHEN** the operator selects a different coordinator
- **THEN** the plan cursor resets to the first stage


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

### Requirement: Details region stacks roster over the orchestration tree (area 6)

For a coordinator selection the system SHALL render BOTH the read-only `" Details "` roster (top) and the embedded `" Orchestration Tree "` graph (bottom) at once with no toggle. The roster is sized to its natural content height, capped at half the region (so the tree keeps at least half), clamped to a minimum of 3 rows and to the region height; the tree fills the remainder and is skipped when fewer than 2 rows remain. The tree is the ONLY interactive surface — every key in the details region forwards to it (j/k/arrow cursor nav + Enter), and coordinator-region clicks route to it.

Derived from: `internal/tui/hera/page.go:238` (`drawDetailsRegion`), `internal/tui/hera/page.go:343` (`handleDetailsKey`), `internal/tui/hera/details.go:44` (`ContentHeight`).

#### Scenario: Both panels render together

- **WHEN** a coordinator is selected and the region is tall enough
- **THEN** the `" Details "` roster and the `" Orchestration Tree "` graph both render, with no `" DAG "` title

#### Scenario: Enter on a tree node opens its agent view

- **WHEN** the details region is focused and the user presses Enter on a tree node
- **THEN** the wired `OnEnter` callback fires to jump to that node's agent view

### Requirement: Orchestration tree projects the role hierarchy in-memory (area 6)

The system SHALL project the embedded graph's nodes from the rail's already-built model via `heraTreeNodes` — a pure in-memory read with no DB call and no provider seam. The graph renders the role hierarchy (coordinator → workers → sub-coordinators), NOT the retired `depends_on` edges. Each worker gets a synthetic edge to its orchestrator's coordinator; a sub-coordinator collapses to one node keyed by task ID, carrying both a parent edge (its worker role under the parent) and child edges (its own workers). The subtree is discovered by multi-binding BFS: orchestrator C is a child of P when C's live coordinator task is bound as a non-coordinator worker under P. Archived orchestrators are pruned as descendants; the coordinator root takes no self-edge (cycle-safe). Node colour comes from the bound task's argus status/result.

Derived from: `internal/tui/hera/tree.go:24` (`heraTreeNodes`), `internal/tui/hera/tree.go:108` (`workerTaskSet`), `internal/tui/hera/page.go:352` (`rebuildDAG`), `internal/tui/hera/model.go:26` (`TaskStatus`/`TaskResult`).

#### Scenario: Coordinator + workers renders a real tree

- **WHEN** a coordinator with bound workers is selected
- **THEN** the tree shows each worker as a child node of the coordinator root

#### Scenario: Sub-coordinator bridges two subtrees

- **WHEN** a worker task under P is also the coordinator of child orchestrator C
- **THEN** that task collapses to one node holding both its parent edge (under P) and its child edges (C's workers)

#### Scenario: No orchestrator selected yields an empty graph

- **WHEN** no orchestrator is selected
- **THEN** the tree renders empty without panic

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


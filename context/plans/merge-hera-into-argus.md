# Merge Hera into Argus as a Native Capability

**Status:** REVIEWED — all 6 open questions decided with human (2026-06-12); ready to execute as a stacked-PR sequence.
**Goal:** Fold the Hera coordinator (`github.com/anutron/hera`) into Argus as a first-class, native capability — eliminating Hera's separate daemon and its re-implementation of Argus internals (`github.com/anutron/argus-sdk`). The current second-tab **DAG view** becomes the **Hera view** (rail + coordinator/HERA pane + Details/AGENT pane), and the existing Sugiyama-lite DAG renderer is **folded into** that view's Details pane so a selected coordinator shows its real dependency graph. A Settings toggle enables/disables Hera; when disabled the second tab falls back to the legacy DAG-only renderer.

> This is a planning document. No implementation. Milestones are sized for a **stacked-PR sequence**, each gated by `make pre-pr` (build → vet → fmt-check → lint-pr → vuln → test-cover-gate, 88% filtered floor).

---

## The two findings that reframe everything

Early research overturned two assumptions in the original framing. The plan is built around the corrected picture.

### Finding 1 — `argus-sdk` is a TUI rendering toolkit, not a REST/socket client

`argus-sdk@v0.0.10` contains **zero** HTTP client, **zero** JSON-RPC/socket subscriber, and **zero** mirrored domain models (no `Task`/`Project`/`Status`). It is five small `tcell`/`tview` packages, and its own README states four were copied out of `drn/argus internal/tui/` and one out of `anutron/hera internal/view/screen/`:

| SDK package | Argus-internal twin | De-SDK verdict |
| --- | --- | --- |
| `keyenc` | `internal/tui/keyenc` (same code) | **ELIMINATE** — import swap |
| `theme` | `internal/tui/theme` (same code) | **ELIMINATE** — import swap |
| `widget` | `internal/tui/widget` (superset) | **ELIMINATE** — import swap |
| `terminalpane` | `internal/tui/terminal` (richer: has OSC-8 + previewvt; SDK is a deliberate subset) | **ELIMINATE** — import swap (note pkg-name `terminalpane`→`terminal`; confirm `DecodeSGRMouse` exists or lift it) |
| `pluginview` | **No twin.** Host side is `internal/tui/views/connector.go`; this is the *plugin side* `tcell.Screen`-over-WebSocket | **RELOCATE or DELETE** — hinges on the in-process-vs-subprocess decision (see below) |

**Consequence:** de-SDK-ing the rendering layer is mostly mechanical. The single architectural decision that determines whether `pluginview` is relocated (4 eliminate + 1 relocate) or deleted (5 eliminate) is **whether native Hera renders in-process against the host's real `tcell.Screen`, or remains a separate process bridged over a WebSocket TTY.**

### Finding 2 — Hera's "idle-gated message bus" is *already Argus*

Hera's reliable delivery is literally implemented on top of Argus. The hera plugin contract (`POST /api/tasks/{id}/notify`) and Argus's native `task_message_send` nudge **both terminate in the same per-daemon `notify.Notifier`** (`internal/notify/service.go`). Same idle+focus gate, same `\x15`(Ctrl+U)→text→CR submission, same dedup-by-`(taskID, deliveryID)`, same 5-min deadline, same idleWatcher `Reconcile`. There is **no second idle-gating implementation to reconcile.**

So almost everything Hera *feels* like it owns is roles draped over primitives Argus already owns:

| Hera capability | Argus-native equivalent (today) | Recommendation |
| --- | --- | --- |
| Idle-gated message bus (inject→auto-submit) | `notify.Notifier` + `task_messages` + `task_message_send`/`task_inbox`/`task_ask`/`ack` | **ELIMINATE-USE-ARGUS** delivery primitive; **MERGE** envelope layer (keep Argus caps/reply/ack; fold in Hera's `tldr` subject field) |
| **Roles-as-identity** (coordinator/worker/freelance + bindings) | **None.** Closest is `task_meta` (Hera already mirrors `hera.thread_status` there) | **FOLD-IN-HERA** — the keystone net-new concept everything else hangs off |
| Auto-adopt / born-bound spawn | `task_create`/`CreateAndStart`/`HeadlessCreateTask` (transactional worktree+session+auto-submit), `depends_on`, depswatcher, `(name,project)` idempotency, `ARGUS_TASK_ID` export | **MERGE** — keep Argus spawn engine, add the role-binding insert as one more transactional step |
| Tree / TLDR roll-up of descendants | Inputs exist (events ring w/ monotonic cursor, `depends_on` reverse-index, `task_set_result`) but no aggregating subtree-since-cursor read | **MERGE** — build native subtree roll-up on existing primitives + per-role cursor + TLDR field |
| DAG dependency tracking | **Fully native & richer** — `DependsOn`, `task_link`/`unlink`/`deps`/`halt_downstream`, `orch.Link`/`FindCycle`, Sugiyama widget (`internal/tui/dagview`), web parity | **ELIMINATE-USE-ARGUS** — capability flows Argus→Hera (this is the fold-in) |
| `/notify` HTTP contract | `internal/api/notify.go` → same `notify.Notifier` | **ELIMINATE-USE-ARGUS** for native path (call Notifier in-process like `runnerNudger`); keep endpoint for out-of-tree plugins |

**The keystone:** introduce a native **roles / bindings** model. Named addressing, default-route-to-coordinator, born-bound spawn, and subtree roll-up all hang off it. Everything else is reuse.

---

## THE pivotal decision: in-process vs. subprocess

This single choice cascades into the SDK disposition, the view implementation, the daemon wiring, and the recovery story. ✅ **DECIDED: in-process native Hera** (host-darren, 2026-06-12). (render the Hera view directly in the Argus TUI against the host `tcell.Screen`; run the coordination layer inside the Argus daemon). Rationale:

- Deletes `pluginview` (5th SDK package) and the entire WebSocket-TTY bridge / connector round-trip for Hera.
- Collapses Hera's `POST /notify` HTTP hop into a direct `notify.Notifier` call (the `runnerNudger` pattern).
- Fixes the recovery gap Aaron flagged (subs detaching on Argus restart): native sessions already survive daemon restarts via the existing daemon-owned PTY model; no separate Hera daemon to fall out of sync.
- The plugin substrate (`connector.go`, `pluginview` wire contract) **stays in place for iris and plannotator** — **confirmed external** (zero iris/plannotator/hera source in the Argus tree); only Hera stops registering as a plugin.

Trade-off (both workers confirm): in-process means porting Hera's `internal/view` rendering/rail/`ops` logic into the Argus TUI's widget/focus model rather than its own tview app, and replacing its `proxy/` SSE fan-out with in-process runner feeds. This is the largest single PR (milestone 6) and the main coverage risk — but it deletes more code than it adds (the entire WebSocket transport, proxy, and `ArgusStateCache` polling vanish).

---

## Per-package disposition — HERA codebase

> From `hera-repo-dig`. Critical correction: **`internal/argus` is NOT the SDK** — it is Hera's hand-rolled typed REST/SSE/socket client to the Argus daemon (`:7743` + Unix socket). It is the **single biggest rewrite target**: in-process, every HTTP/SSE round-trip collapses to a direct call to Argus internals. The argus-sdk is imported **only** in `internal/view` (21 hits, all rendering).

| Hera package | Responsibility | Disposition | Rewire target |
| --- | --- | --- | --- |
| `internal/argus` (27) | Hera's own typed HTTP client + SSE stream + PTY-output stream + Unix-socket `Ports`/`Ping` + plugin/settings/view registration + link-state machine + restart watcher/recovery | **ELIMINATED → NEEDS-REWORK** (the substrate boundary) | REST→direct `db.DB`/runner/events-ring calls; SSE→`events.Ring.Subscribe`; socket Ports/Watcher/Recover→**deleted**; `NotifyTask`→`notify.Notifier` in-process; registration→deleted. A few typed structs may survive as adapters. |
| `internal/config` (3) | Hera runtime config, scope token, `~/.hera/` paths/intervals | **ELIMINATED (mostly)** | Surviving knobs (`AutoInjectEnabled`, `NotifyDeadlineMs`, `IdleDebounce`, `ReconcileInterval`) fold into Argus config/settings |
| `internal/daemon` (9) | Wires subsystems into a runnable hera process; `toolDefinitions()` (9 schemas); bounce-recovery; watcher | **ELIMINATED as a process** | Wiring sequence folds into Argus daemon bootstrap as a "hera subsystem" init; tool schemas migrate verbatim; bounce-recovery deleted (no separate process) |
| `internal/db` (15) | SQLite state: orchestrators, roles, bindings, messages (tldr, delivery_mode), role_status, event_cursor, tree_read_cursors, config; 10 versioned migrations | **FOLDS-IN NATIVELY** (core model Argus lacks) | New tables in `data.sql` (recommended over a sidecar — see State Migration); migrations port directly; `event_cursor` unneeded if subscribing to the in-process ring |
| `internal/events` (7) | SSE subscriber + **AdoptHandler** (auto-adopt) + **ResyncHandler** + **PeriodicReconciler** | Subscriber **ELIMINATED**; Adopt/Resync **logic FOLDS-IN (NEEDS-REWORK)** | Replace SSE client with in-process `events.Ring` subscription; handlers call Argus task APIs directly. Strict adoption rule **D4** is unique IP — preserve exactly |
| `internal/log` (1) | Placeholder, no exported API | **ELIMINATED** | Argus `uxlog` + slog redirects |
| `internal/mcp` (17) | **The 9 `hera_*` tools** + registrar heartbeat + callback HTTP listener (`:7744`) + shared-secret auth + caller-role resolver + link gate | Tool handlers **FOLD-IN NATIVELY (top priority)**; registrar/listener/auth/gate **ELIMINATED** | Handlers rewired: `CreateTask`→`CreateAndStart`, `PutTaskMeta`→task_meta, `NotifyTask`→notify, `TaskForCwd`→worktree lookup |
| `internal/settings` (2) | Registers Hera settings section over REST + heartbeat | **ELIMINATED** | Two knobs become a native Argus settings section directly (the REST-callback auth bug is moot in-process) |
| `internal/view` (44 + `ops/` + `proxy/`) | Three-region rail + coordinator/HERA pane + agent/details pane; full keyset; freelance/archive/pinned sections; PTY proxy fan-out; OSC scrub; viewport guard; `ops/` mutation service (~25 ops); `proxy/` SSE→ring fan-out | Rendering + rail + ops **logic FOLDS-IN (heavy NEEDS-REWORK)**; WebSocket transport + `proxy/` + `ArgusStateCache` polling **ELIMINATED** | Swap 5 SDK imports for Argus internals; feed panes from Argus runner; `ops/` rewires to Argus task lifecycle; **DAG widget folds in here** |
| `cmd/hera` (7) | Cobra CLI `start`/`stop`/`status`/`list`/`resume` + PID lifecycle | **ELIMINATED** | `list`/`status` *could* become Argus CLI subcommands if desired; rest deleted |
| `cmd/hera-view-client` (1) | Debug WebSocket client | **ELIMINATED** | Tests the transport that goes away |

**SDK coupling (the only de-SDK work):** 21 imports, all in `internal/view` — `theme` (ColorTitle×40, StyleDimmed×28…), `terminalpane` (New×27 + `DecodeSGRMouse`), `widget` (DrawText×25), `keyenc` (Encode×1), and `pluginview` (Conn×5 — **deleted outright** in-process). Every symbol has an internal Argus counterpart since the SDK was extracted *from* Argus.

### Hera invariants to preserve (hard-won — port faithfully)

- **Multi-binding (migration 0004) — the key impedance mismatch.** One Argus task can be worker in orchestrator A *and* coordinator in B at once. Uniqueness is per-`(task, orchestrator)`, not per-task; role-side stays one-live-binding-per-role. The `orchestrator` param on every tool disambiguates. **Argus's model currently assumes 1 task = 1 thing — this must be designed around.**
- **Auto-adopt strict rule (D4):** adopt a `link.created` child only if parent has *exactly one* coordinator binding AND child has `meta:hera.role=worker`. Archived parents filtered out.
- **Archive is non-destructive; only delete ends bindings.** `task.archived`→no-op (resumable); `task.deleted`→end all live bindings. Pin/archive mutually exclusive.
- **Reliable delivery already delegated to Argus** (migration 0009 dropped nudge/doorbell columns) — don't re-port the doorbell loop; 2s idle debounce is locked.
- **`meta:hera.role` / `meta:hera.thread_status` mirroring is best-effort** — keep soft-fail semantics (failure must not undo local state).
- **FK-cascade migration hazards (0007, 0010):** table rebuilds need connection-level `PRAGMA foreign_keys=OFF` (not inside a txn) or a parent delete cascades and wipes child bindings; `tree_read_cursors` needed `ON DELETE CASCADE` (BUG-034). Port migrations faithfully.
- **OSC filter + viewport guard** patch SDK-terminalpane gaps + a `13×8` initial-viewport race (BUG-049) — may be unnecessary against Argus's own terminalpane; verify, don't blindly port.
- **Two deferred debts to fix during merge:** (a) role+binding insert is 2 non-transactional execs (orphan risk) — adopt Argus's transactional `CreateAndStart` LIFO-cleanup; (b) `EventCursor` advances regardless of handler success (missed adoptions) — moot if consuming the in-process ring with retry.
- **Open view-layer bugs (BUG-052–056)** mostly evaporate once rendering is in-process; **BUG-050** (roll finished workers → `in_review`) is a coordination-policy decision to settle during merge.

---

## Argus internals — attachment seams

> From `argus-internals-dig`. **Confirmed: there is zero iris/plannotator/hera source in the Argus tree — all three are external plugins on a scope-agnostic substrate.** Making Hera native means Hera *stops registering as a plugin*; the substrate stays untouched so iris/plannotator keep working.

**Big correction: `hera_*` tools are PROXIED, not native, today.** The Hera daemon POSTs to `POST /api/mcp/tools` (scope=`hera`), persisted in `plugin_mcp_tools`; Argus's MCP server merges them into `tools/list` and dispatches `tools/call` by HTTP-POSTing to Hera's `callback_url`. The native pattern is a package-level `[]Tool` def slice + an `*Enabled()` gate + a `switch params.Name` arm (exactly how `taskToolDefs`/`messagingToolDefs` work).

- **Second tab / DAG view** — `widget/header.go` (fixed enum `TabTasks/TabDAG/TabSettings`), `app.go` `buildUI` (~L546 `pages.AddPage`) + `switchTab` (~L2466), `dagpage.go`, `dagview/{widget,layout,render}.go`. **Tab swap (Option 1, lowest blast radius):** keep the enum slot, build a `HeraPage` (`tview.Flex`: rail + coordinator pane + details pane), `AddPage("hera", …)`, route `switchTab`'s second arm to `"hera"`, relabel `TabDAG`→`TabHera`. **Keep the `"dag"` page registered** for the disabled-fallback. The DAG widget is a self-contained `tview.Box` with `SetRect`/`Draw`/`SetNodes` — **reusable as a sub-pane as-is** (already proven by `DAGPage`); only caveat is its hardcoded `" DAG "` bordered-panel title (retitle/suppress when embedded).
- **Plugin substrate (DO NOT WEAKEN)** — `internal/tui/plugin_views.go`, `internal/tui/views/{registry,connector}.go`, `internal/api/{plugin_views,plugin_settings}.go`, `internal/db/{plugin_views,plugin_sections,mcp_tools}.go`. Full-surrender keyset (double-Ctrl+Q failsafe), reconnect/backoff (`pluginReconnectLoop`, 250ms→2s), resize reconciliation (`reconcilePluginViewSize`, gated `connReady && laidOut`), terminalpane reuse. Native Hera is a **new in-process tview surface, NOT a terminalpane+WebSocket mount** — the two coexist. Risk: if Hera previously registered a hotkey `plugin_view`, ensure native tab + residual plugin-view don't both bind.
- **MCP registration** — `internal/mcp/{server,registry,protocol}.go`, `internal/db/mcp_tools.go`, `internal/api/mcp_tools.go`, daemon wiring (`daemon.go` L545/603/663). **Native plan:** add `heraToolDefs` (9 schemas, ported verbatim from Hera's `daemon.toolDefinitions()`), add `s.heraEnabled()` (gate on a `SetHeraManager`-wired manager, mirroring `messagingEnabled()`), append under that gate in `handleToolsList`, add 9 `case "hera_…":` arms in `handleToolsCall`. **Keep the plugin `default:` proxy arm** (iris/plannotator). **Dup-tool guard (critical):** when native Hera is on, the external Hera daemon must stop registering `hera_*` plugin tools OR Argus must filter `hera`-scope rows from the plugin list — else `tools/list` shows duplicates (native dispatch wins, but the list is dirty).
- **Daemon** — `daemon.go` (`Serve` wiring ~L466-690), `rpc.go`, `bounce.go` (restart/resume), `nudge.go`, `notify.Notifier`, events ring + SSE. **Seam:** build a `heraManager` in `Serve` alongside `pluginRegistry`, gated on `cfg.Hera.Enabled`, injected into MCP (`SetHeraManager`) and the API server (REST parity). Reuse the **existing `task_messages`+`nudge`+`notify.Notifier` bus** for delivery; model the **auto-adopt watcher on `depswatcher.Watcher`** (tick loop gated by `d.done`); **born-bound spawn = `HeadlessCreateTask` + a binding-row write with its own LIFO compensating cleanup**; hook `bounce.go`'s session-resume persistence so bindings survive a daemon bounce.
- **DB schema** — single `createTables()` in `schema.go` (`CREATE TABLE IF NOT EXISTS` + idempotent `ALTER` + `CREATE INDEX`; **no versioned migration runner** — Hera's 10 migrations collapse to drop-in `CREATE TABLE`s). New `hera_orchestrators`, `hera_roles`, `hera_bindings` tables; for the message store see the decision below. Follow conventions: TEXT/UUID or AUTOINCREMENT PK, `created_at TEXT`, composite UNIQUE for upsert-by-key, **app-level cascade cleanup** (hook `SetArchived`/`Delete` like `task_meta`/`task_messages` — NOT FK `ON DELETE CASCADE`, so soft-archive scoping works).
- **Settings** — `settings.go` (fixed `settingsCategory` enum + `settingsRowKind`), `db/config.go` (KV `config` table), `config/config.go` (struct). Add `HeraConfig{Enabled bool}` to `config.Config`, read `kv["hera.enabled"]` in `db/config.go` (one line, mirrors `kb.enabled` ~L108), add an `srHera` toggle row, gate the daemon's `heraManager` wiring on it. `switchTab`'s second arm routes `cfg.Hera.Enabled` → `"hera"` else `"dag"`. Note: `config.toml` overlay can override `hera.enabled` (toml wins) — document it; a live toggle while *on* the tab needs a re-route refresh.

### The message-store decision (workers diverge — recommendation)

Worker A found Hera keeps its own role-addressed `messages` table (with `tldr`, `delivery_mode`); Worker B suggests reusing Argus's task-addressed `task_messages`. **Recommendation: a dedicated `hera_messages` table for role-addressing, but reuse `notify.Notifier` for delivery.** Hera messages are addressed to *roles* (orchestrator-scoped names with default-route-to-coordinator), carry a `tldr` subject and `delivery_mode`, and feed the subtree roll-up cursor — overloading the task-addressed `task_messages` with role semantics is messier than a clean table. The *delivery engine* is unconditionally shared (Finding 2). This keeps the role layer self-contained while inheriting Argus's idle-gated, ack-cancelling, deadline-bounded pane delivery for free.

---

## DAG-fold design

The existing Sugiyama-lite DAG widget (`internal/tui/dagview/`) is **not discarded** — it becomes a render mode of the Hera view's Details/right pane. The widget is a self-contained `tview.Box` (`SetRect`/`Draw`/`SetNodes` + `OnEnter`/`OnLink`/`OnUnlink`/`OnHalt`/`OnClick` callbacks); embedding it in a sub-rect of the Details `Flex` is already proven by `DAGPage`. Wiring:

- When a **coordinator** role is selected in the rail, the Details pane calls `dagview.New()` against a sub-rect and renders that orchestrator's dependency subgraph from Argus's `depends_on` edges (`dagNodesFromTasks` projection, archived-drop + orphan-filter). TUI/web DAG parity is preserved.
- When a **worker/leaf** is selected, the Details pane shows the AGENT terminal (fed from Argus's runner in-process, replacing Hera's `proxy/` SSE fan-out).
- **Caveats:** the widget hardcodes a `" DAG "` bordered-panel title — retitle or suppress the border when embedded. `OnBranchChange` must stay **log-only** (no `Sync()` — CLAUDE.md UX-rendering rules); preserve `maybeNotifyBranchChange` for cursor-highlight ghost prevention. The link/unlink pickers are **WIP stubs** in the DAG page today (`SetNotice` only) — the native Hera view is the natural place to finally implement them.

This fills the gap Aaron flagged: Hera gains real DAG dependency tracking by adopting Argus's `depends_on` edges rather than its query-time orchestrator *tree* (`SubtreeOrchIDs` BFS). The tree (TLDR roll-up) and the DAG (dependency edges) are **orthogonal and complementary** — the rail shows the tree; the Details pane shows the DAG.

---

## State migration

Per Argus policy ("no legacy migration code; write a one-off script"): a standalone migration reads `~/.hera/state.sqlite` (orchestrators, roles, bindings, messages, role_status, tree_read_cursors, config) and writes into Argus's new hera tables. Single user, run-once, not shipped in-tree.

**Schema landing — recommendation: new tables in Argus `data.sql`** (not a sidecar `hera.sqlite`), because the role layer must reference Argus `tasks`/`links` rows transactionally (born-bound spawn inserts a binding inside the `CreateAndStart` txn; FK-cascade migration hazards 0007/0010 require single-connection control). A sidecar would reintroduce a cross-store boundary — the very coupling this merge eliminates. Hera's 10 migrations port into Argus's seed-on-first-run path; `event_cursor` is dropped (the in-process ring needs no cross-restart SSE cursor). Tables: `orchestrators`, `roles`, `bindings` (per-`(task,orchestrator)` live-uniqueness partial indexes), `messages` (with `tldr`, `delivery_mode`), `role_status`, `tree_read_cursors`.

---

## Settings & default behavior

**Recommendation: default ENABLED.** When disabled, the second tab renders the legacy DAG-only view (no rail, no PTYs) so the tab is coherent in both states. Toggle is an `srHera` row in `internal/tui/settings.go`, persisted as `hera.enabled` in the `config` KV table and read into `cfg.Hera.Enabled` (exact precedent: `kb.enabled`/`api.enabled`). Both the `"hera"` and `"dag"` pages stay registered in `buildUI`; `switchTab`'s second arm picks one by the flag and relabels the tab dynamically. Pressure-test: enabled-by-default is right because (a) the merge's whole point is making Hera first-class, and (b) the legacy fallback guarantees the tab degrades gracefully. Caveat: `config.toml` overlay can override `hera.enabled` (toml wins); a live toggle while *on* the tab needs a re-route refresh.

---

## Milestones (stacked PRs)

> Sequenced so nothing breaks mid-migration; iris/plannotator plugin substrate untouched throughout. Each PR is independently shippable and `make pre-pr`-green. Note the build happens **inside the Argus repo** — Hera code is ported in, not imported; there is no intermediate "Argus depends on Hera" state.

1. **Roles/bindings/orchestrators schema + store** — drop `hera_orchestrators`/`hera_roles`/`hera_bindings` into `createTables()`; role/binding/orchestrator CRUD in `internal/db` with `SetArchived`/`Delete` cascade hooks; port the **multi-binding** per-`(task,orchestrator)` uniqueness and the FK-cascade-safe delete semantics. Net-new, no UI, fully unit-tested. *(Foundation — everything hangs off this.)*
2. **`hera_messages` + bus wiring** — dedicated role-addressed message table; `hera_send`/`inbox`/`mark_read` back-ended by the existing `notify.Notifier` (no new delivery engine). Reuse messaging caps/ack-cancel/deadline. *(Depends on 1.)*
3. **Native `hera_*` MCP tools** — `heraToolDefs` (9 schemas verbatim) + `s.heraEnabled()` gate + 9 `switch` arms; `CallerContext` resolves caller role via cwd→task→binding. Keep the plugin `default:` proxy arm. **Ship the dup-tool guard** (filter `hera`-scope from plugin list when native is on). *(Depends on 1, 2.)*
4. **Born-bound spawn + auto-adopt watcher + finish policy** — `hera_spawn_worker` = `HeadlessCreateTask` + transactional binding write (LIFO compensating cleanup); auto-adopt watcher modeled on `depswatcher` consuming the in-process events ring, preserving rule **D4**. **On worker-session finish: auto-roll the task to `in_review` and stamp a `meta:hera.ready_to_close` indicator** (rendered as a distinct rail + task-list mark) — BUG-050 resolution. *(Depends on 1, 3.)*
5. **Subtree TLDR roll-up** — `tldr`/`delivery_mode` on `hera_messages` + per-role cursor (`tree_read_cursors`) + subtree-since read over `SubtreeOrchIDs` BFS on the events ring (replaces SSE cursor). *(Depends on 2, 4.)*
6. **Native Hera view (the big one)** — `HeraPage` (rail + coordinator pane + Details pane); port `internal/view` rendering/rail/`ops` logic, swapping the 5 SDK imports for `internal/tui/{theme,widget,terminal,keyenc}` and **deleting** `pluginview`/`proxy`; feed panes from the runner in-process. **Carry full multi-binding through the rail/task-list/agent-view** (one task may appear under multiple orchestrators; ops disambiguate by `orchestrator`). SimulationScreen smoke tests for tab entry/exit, focus ladder, rail nav, **and a multi-binding task rendered under two orchestrators**. *(Depends on 3, 4; coverage-heavy — largest PR.)*
7. **Fold DAG widget into Details pane** — embed `dagview.Widget` for coordinator selection; implement the WIP link/unlink pickers. *(Depends on 6.)*
8. **Settings toggle + legacy fallback** — `cfg.Hera.Enabled`, `srHera` row, `switchTab` routes `"hera"`/`"dag"`, dynamic tab relabel; daemon gates `heraManager` on it. *(Depends on 6.)*
9. **One-off `~/.hera/state.sqlite` → `data.sql` migration script** + cutover: stop the external Hera daemon registering as a plugin, retire `cmd/hera`. *(Last — flips the default.)*

---

## Risks & cutover order

- **iris / plannotator must keep working** — **confirmed external** (zero source in Argus tree); they ride the scope-agnostic substrate. Only Hera stops registering as a plugin; do NOT remove/weaken `plugin_views`/`connector`/`registry`/reconnect code.
- **Dup-tool window** — between shipping native `hera_*` (PR 3) and the external Hera daemon ceasing registration (PR 9), both could appear in `tools/list`. The PR-3 scope-filter guard closes this; until cutover, run with the external Hera daemon stopped.
- **Coverage gate** — every PR must clear the 88% filtered floor; the ported `view` logic is the coverage risk. SimulationScreen smoke tests required for tab entry/exit, focus ladder, rail mutations, and the page-wrapper `MouseHandler` focus guard.
- **Race/ordering (from daemon map):** (a) event emission must stay **outside `d.mu`** (events.md) — status/binding writes that emit must not hold the DB mutex across emit; (b) bus delivery must respect the `focusTracker`/`notifier` idle gate (no mid-keystroke writes); (c) born-bound spawn binding write needs its own LIFO compensating cleanup or a failed spawn orphans a binding; (d) **auto-adopt vs depswatcher** — two tick loops mutating task state; key auto-adopt off a distinct *binding* state so they never both flip the same task's session; (e) daemon-restart binding reconciliation must mirror the session-resume/`NeedsSessionRecapture` race handling (daemon-rpc.md).
- **UX rendering** — no new `Sync()`; `OnBranchChange` stays log-only; new panes must cover their full bounding rect via `widget.FillArea`/`DrawBorderedPanel`.
- **Recovery (Aaron's gap)** — the standalone Hera daemon loses sub state on Argus restart (subs detach, waking is imperfect). Native in-process integration resolves this structurally: daemon-owned PTYs already survive restart, `bounce.go` persists the live session set, and bindings live in the same `data.sql` — no cross-process resync. Hook bindings into `bounce.go`'s resume path so they're reconciled on startup like sessions.

---

## Open questions

1. **In-process vs subprocess** for native Hera rendering — drives pluginview disposition. ✅ **DECIDED: in-process** (host-darren, 2026-06-12). `pluginview` is deleted; Hera renders directly to the Argus `tcell.Screen`; coordination runs in the Argus daemon.
2. **Multi-binding impedance mismatch** — Hera lets one task hold N role bindings (worker in A, coordinator in B). ✅ **DECIDED: carry full multi-binding through the UI** (host-darren, 2026-06-12) — rail, task list, and agent view must represent one task appearing under multiple orchestrators; every native hera op carries the `orchestrator` disambiguator. Preserves worker self-promotion end-to-end. **Impact:** enlarges milestone 6 (view) and forces Q3 → first-class tables.
3. Do roles become **first-class tables** or a formalized `task_meta` `hera.*` namespace? ✅ **SETTLED → first-class tables** — the Q2 full-multi-binding decision rules out `task_meta` (a per-task single-value sidecar can't represent N roles/orchestrators per task). Tables land in `data.sql` (see migration section).
4. Does the `/notify` HTTP endpoint stay (for out-of-tree plugins) after the native path bypasses it? ✅ **DECIDED: keep** (host-darren, 2026-06-12) — native Hera calls `notify.Notifier` in-process; the endpoint stays as a thin shim for out-of-tree plugins.
5. **BUG-050 policy:** ✅ **DECIDED** (host-darren, 2026-06-12) — when a worker finishes, **auto-roll its task to `in_review` AND stamp a "ready to close out" indicator** (a `meta:hera.ready_to_close` flag surfaced as a distinct rail + task-list mark), so the coordinator/human can see at a glance which finished workers are awaiting close-out. The status flip and the marker are separate signals: `in_review` = workflow state; the mark = "this is done and ready to be closed."
6. Settings default — ✅ **DECIDED: enabled** (host-darren, 2026-06-12). Disabled → legacy DAG-only second tab.

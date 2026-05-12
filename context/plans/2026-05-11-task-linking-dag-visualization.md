# Plan: Task Linking + DAG Visualization

**Date:** 2026-05-11
**Source:** Inline spec from session (user request to link argus tasks and visualize as CircleCI-style DAG)
**Status:** Draft
**Current Phase:** Phase 1

## Goal

Make the existing `DependsOn` orchestration primitive a first-class user feature: let users create/remove task links from the TUI and web, visualize the resulting DAG in a new tab with status overlay, and surface `result.pr_url` / `result.failed` as actionable UI. Add a `plan_slug` column so a stack is identifiable without graph traversal.

## Background

The daemon already ships every primitive needed for stacked-PR orchestration: `model.Task.DependsOn` / `BaseBranch` / `Result` round-trip through SQLite (`internal/db/tasks.go`), `depswatcher` auto-starts unblocked tasks (`internal/depswatcher`), and `HeadlessCreateTask` accepts the orchestration fields end-to-end (`internal/daemon/headless.go`). The `/orchestrate-stack` skill drives all of this via MCP today.

What's missing is **purely UI**:

- No way to link two existing tasks (the only path is creating a new task with `depends_on` upfront via MCP or HTTP).
- No way to see the DAG. Tasks are listed flat in the task list — a 12-PR stack like `memory/handoff/2026-05-11-175011-orchestrate-mcp-v1-execution.md` is unreadable.
- `result.pr_url` and `result.failed` are stored but never rendered.
- `plan_slug` doesn't exist — orchestrators rebuild stack membership by traversing `depends_on`, which is fragile across archives and retries.

Reference: `context/knowledge/gotchas/orchestration.md` describes the depswatcher contract this plan must respect (status-not-result is the gate; halt-downstream is the orchestrator's responsibility).

## Requirements

### Must Have

- **Schema**: new `plan_slug` column on `tasks`, idempotent ALTER ADD, empty string = unaffiliated task.
- **Linking RPC**: `LinkTasks(child, parent)` and `UnlinkTasks(child, parent)` on the daemon, rejecting cycles via the same DFS check the create path uses.
- **TUI DAG tab**: new header tab after the task list. Layered top-down placement, nodes show task name + status glyph, edges drawn with box-drawing chars. Per-status colors match the task list. Enter on a node = jump to that task's agent view.
- **TUI key bindings**: `l` opens a link picker (depend on…), `L` opens an unlink picker (remove a dep), `h` on a failed node halts downstream (calls `task_stop` on `in_progress` descendants, `task_archive` on `pending` ones).
- **Web `/dag` route**: new view in the SPA, SVG-rendered DAG via a layered layout. Same status colors. Click a node = navigate to the task detail page.
- **Web linking UI**: "Links" section in task detail showing upstream + downstream tasks; add/remove buttons hit the linking endpoints.
- **Result rendering**: parse `result` JSON in both UIs; render `pr_url` as a clickable link on task rows + DAG nodes; render `result.failed` + `reason` as a red banner on the task and a red border on the DAG node.
- **Cycle-detection display**: when a link create returns a cycle error, the modal/banner shows the offending path (e.g. "A → B → A") so the user can see why.

### Should Have

- **Wedged-task badge**: tasks `in_progress` >2h get a warning glyph on the DAG (matches orchestrator skill's threshold).
- **DAG scoping**: dropdown / filter to scope by project and optionally by `plan_slug` so a 60-task vault doesn't render one giant graph.
- **TUI DAG smoke test**: drive `tab → dag`, inject a status transition, assert force-redraw fires.
- **Web smoke test (Playwright)** in `web-tests/`: open `/dag`, verify the SVG renders the expected node count, click a node, expect navigation.

### Won't Do (this iteration)

- Auto-routing of DAG edges (splines, orthogonal). Sugiyama-lite straight edges only.
- Drag-to-link in the web UI. Use buttons + pickers.
- `plan_slug` editor UI. Set via API/MCP only this iteration; surfaced in DAG view as a filter.
- Auto-merge or auto-rebase from the DAG (those belong to `/orchestrate-stack`, not the daemon UI).
- Multi-parent visual highlighting (nice to have, but adds layout complexity — defer).
- Anything in `internal/tui/links.go` (that's a URL link picker for terminal output, unrelated).

## Technical Approach

**Data flow**: depswatcher and `task_create` already do the heavy lifting. The new RPCs (`LinkTasks` / `UnlinkTasks`) mutate `DependsOn` on existing rows, run the same cycle DFS, and the depswatcher tick picks up newly-unblocked tasks within ~60s. No new background workers needed.

**TUI DAG**: rolled by hand, no third-party library — none of the Go TUI ecosystem (tview, bubbletea, charm) ships a DAG widget, and the off-the-shelf ASCII renderer (`graph-easy`, Perl) isn't worth a runtime dep. New widget `internal/tui/dagview/` implements:

1. **Layered layout** — Kahn topological sort assigns each node a `layer` (depth from roots).
2. **Within-layer ordering** — barycentric heuristic (each node's column = mean of parent columns) for one or two sweeps to reduce crossings.
3. **Grid placement** — each layer is a column band, each node a 3-row box (`╭─name─╮ / │ status │ / ╰──────╯`).
4. **Edges** — straight verticals + horizontal jogs using `─ │ ├ ┤ ┬ ┴ ┼` box-drawing chars. No splines.
5. **tcell painting** — direct `SetContent`, damage tracking via the standard `tview.Box` pattern. Implements `OnBranchChange` callback wired to `forceRedraw` (per CLAUDE.md's branch-change-callback contract).

**Web DAG**: `dagre` (MIT, well-supported, ~25 KB) computes layout; we render the resulting node/edge coordinates as inline SVG. No React, no D3 — vanilla DOM, consistent with the rest of the PWA. New static asset under `internal/api/static/vendor/dagre/`, bumped `SW_VERSION`.

**Linking model**: `DependsOn` is the source of truth. There is no separate "links" table. The web endpoint `/api/tasks/{id}/links` is **already used for URL extraction from terminal output** — name collision. Use `/api/tasks/{id}/deps` (GET = current upstream/downstream, POST = add a parent, DELETE = remove a parent) for the new endpoints.

**Halt-downstream**: a single helper `HaltDownstream(taskID)` on the daemon walks `depends_on` reverse-index, calls `runner.Stop` on `in_progress` rows and `db.SetArchived(true)` on `pending` rows. Matches the orchestrator skill's halt semantics. The depswatcher race (a `pending` task may flip to `in_progress` mid-walk) is handled by re-querying status inside the loop and falling back to `Stop` if the row is now running.

## Decisions

| Decision | Rationale |
|----------|-----------|
| New column `plan_slug`, not a new `plans` table | Tasks are the unit of work; a plan is just a grouping label. A separate table would force JOINs everywhere for marginal benefit. Empty string = unaffiliated. |
| Roll our own DAG layout for TUI | No Go-native lib; `graph-easy` (Perl) and `dot` (Graphviz binary) both impose external runtime deps. Sugiyama-lite is ~300 LOC and Argus stacks are ≤30 nodes. |
| `dagre` for web (not D3 or React) | Vanilla DOM matches existing PWA. `dagre` is unmaintained-but-stable; sufficient for our scale. |
| Endpoint name `/api/tasks/{id}/deps` (not `/links`) | `/links` already serves URL extraction from terminal output (`handleGetLinks` at `internal/api/handlers.go:780`). Avoid the collision. |
| Reuse `DependsOn` column for the link source of truth | Adding a `task_links` table would duplicate state; the orchestrator already reads `DependsOn`. |
| Halt-downstream lives in the daemon, not the TUI | Other clients (web, MCP) want the same operation. Putting it in `RPCService` lets all clients share. |
| Cycle detection runs in the daemon, on every link mutation | The daemon already has cycle detection at create time (per `orchestration.md` gotcha #6); reuse it. The returned error must include the offending path for UI display. |
| No drag-to-link in v1 | Picker modal is simpler, matches existing fuzzylinkpicker UX, ships faster. |
| `plan_slug` is opaque text, set by API/MCP only | UI surface this iteration is filter-only. Avoids a settings page just to seed a string. |
| `plan_slug` is strictly user-supplied; daemon never auto-derives | Matches `result` opacity contract. Reachability-based auto-tagging would couple the daemon to orchestrator semantics. |
| Archived tasks render greyed-out in the DAG (not hidden) | Visual continuity for retried/halted stacks — reviewers can still see where a failed branch sat in the graph. Cursor skips archived nodes for `enter`/`h`. |
| Web `/dag` is its own top-level nav tab (parallel to task list) | Matches the TUI layout (`TabDAG` after `TabTasks`); avoids a deep sub-route inside the task list. |

## Implementation Steps

### Phase 1: Schema + RPC primitives
**Status:** pending

- [ ] `internal/db/schema.go` — add `plan_slug TEXT NOT NULL DEFAULT ''` to `CREATE TABLE tasks` + idempotent ALTER ADD migration block.
- [ ] `internal/db/tasks.go` — add `plan_slug` to `taskColumns`, INSERT, UPDATE, scan. Maintain column-order parity (per orchestration gotcha #11).
- [ ] `internal/model/task.go` — add `PlanSlug string` field with `json:"plan_slug,omitempty"` and a doc comment explaining it's the orchestrator's grouping key.
- [ ] `internal/db/tasks_test.go` — extend orchestration round-trip test to cover `PlanSlug`.
- [ ] `internal/daemon/types.go` — add `LinkTasksReq{ChildID, ParentID string}`, `UnlinkTasksReq`, `DepsResp{Upstream, Downstream []model.Task, Cycle []string}`, `HaltDownstreamReq{TaskID string}`.
- [ ] `internal/daemon/rpc.go` — implement `LinkTasks` (append to child's `DependsOn`, run cycle DFS, persist; on cycle, return the offending path), `UnlinkTasks` (remove parent from child's `DependsOn`), `GetDeps` (return upstream + downstream by walking `DependsOn` both directions), `HaltDownstream` (per technical-approach).
- [ ] `internal/daemon/cycledetect.go` (extract from `headless.go` if currently inline) — shared cycle DFS that returns the offending path, not just a boolean.
- [ ] `internal/daemon/rpc_test.go` — table-driven tests for each new RPC; cycle case asserts the returned path; halt case asserts status transitions.

### Phase 2: HTTP API endpoints
**Status:** pending

- [ ] `internal/api/routes.go` — register `GET /api/tasks/{id}/deps`, `POST /api/tasks/{id}/deps` (body `{parent_id}`), `DELETE /api/tasks/{id}/deps/{parent_id}`, `POST /api/tasks/{id}/halt-downstream`, `GET /api/dag?project=X&plan=Y` (full DAG snapshot for rendering).
- [ ] `internal/api/handlers.go` — implement each handler; `requireMaster()` on POST/DELETE/halt; map daemon cycle errors to `409 Conflict` with the path in the body.
- [ ] `internal/api/handlers_test.go` — happy path + cycle rejection + auth check for each endpoint.

### Phase 3: TUI DAG widget
**Status:** pending

- [ ] `internal/tui/dagview/layout.go` — Kahn topological sort → layers; barycentric within-layer ordering (2 sweeps); produce `[]NodePos{TaskID, Col, Row, Layer}` + `[]EdgePos{From, To, path}`. Archived tasks participate in layout but get an `Archived bool` flag the renderer reads.
- [ ] `internal/tui/dagview/render.go` — paint nodes (3-row boxes with status glyph + name truncated to box width) and edges (box-drawing chars) into `tcell.Screen`. Status color = same palette as task list. Archived nodes render with a dim/grey palette (single colour, no status glyph hue).
- [ ] `internal/tui/dagview/widget.go` — `Widget` struct embedding `*tview.Box`; methods: `SetTasks([]model.Task)`, `MoveCursor(dx,dy)`, `CurrentTask() string`, `OnBranchChange func()`. Implements `InputHandler`, `MouseHandler` (click selects nearest node), `PasteHandler` (no-op). Wires `OnBranchChange` on every state-mutating method. Cursor movement skips archived nodes for `enter`/`h` but they remain selectable (just visually inert).
- [ ] `internal/tui/dagview/layout_test.go` + `render_test.go` + `widget_test.go` — table-driven on layout shapes (linear, fan-out, fan-in, diamond), golden-text rendering tests, smoke-test cursor movement + branch-change firing.
- [ ] `internal/tui/dagpage.go` — `DAGPage` wrapper with header banner ("DAG · project · plan slug"), the widget, and a status footer ("l: link, L: unlink, enter: open, h: halt downstream"). MouseHandler guards `setFocus` per the page-wrapper invariant.
- [ ] `internal/tui/dagpage_test.go` — smoke test mouse click on banner doesn't steal focus.
- [ ] `internal/tui/app.go` — register `dag` page, add header tab `TabDAG` after `TabTasks`, route Tab/Shift-Tab through it, wire `OnBranchChange` to `forceRedraw`. Key handlers: `l` → linkpicker (existing modal, repurposed callback), `L` → unlinkpicker (new tiny modal listing current upstream deps), `enter` → switch to agent view for selected task, `h` → confirm halt-downstream modal then call RPC.
- [ ] `internal/tui/smoke_test.go` — `TestSmoke_DAGTabRendersAndFiresRedraw`, `TestSmoke_DAGLinkUnlinkRoundTrip`, `TestSmoke_DAGHaltDownstreamCallsStop`.

### Phase 4: Web /dag route + linking UI
**Status:** pending

- [ ] `internal/api/static/vendor/dagre/dagre.min.js` — vendor (MIT, attribution in NOTICE if not already present).
- [ ] `internal/api/static/index.html` — add `<section id="dag-view" hidden>` with SVG container, link to `/dag` route, nav entry. Existing routing pattern in the SPA.
- [ ] `internal/api/static/app.js` (or wherever the SPA routes live) — `/dag` handler fetches `/api/dag`, builds the `dagre.graphlib.Graph`, calls `dagre.layout`, renders nodes/edges to SVG. Node colors keyed off `task.status` + `result.failed`; archived nodes use a grey fill + reduced opacity. Click → `location.hash = '#/tasks/<id>'`. Add a top-level nav entry "DAG" parallel to "Tasks" (not nested).
- [ ] `internal/api/static/index.html` task detail panel — add "Links" section: list of upstream deps with × buttons (DELETE `/api/tasks/{id}/deps/{parent_id}`), an "Add upstream…" picker (fetches `/api/tasks?project=X`, POSTs `/api/tasks/{id}/deps`). Cycle rejection shows the returned path inline.
- [ ] `internal/api/static/app.js` — render `result.pr_url` as `<a target="_blank">` on task rows; render `result.failed`+`reason` as red banner.
- [ ] `internal/api/static/sw.js` — bump `SW_VERSION` (per CLAUDE.md: any change to static assets bumps the SW version).
- [ ] `web-tests/dag.spec.ts` (or extend an existing spec) — Playwright smoke: seed two tasks via test harness, link them via POST, navigate to `/dag`, assert two nodes + one edge, click a node, assert detail view loads.

### Phase 5: MCP surface
**Status:** pending

- [ ] `internal/mcp/...` — expose **new** tools: `task_link(child_id, parent_id)`, `task_unlink(child_id, parent_id)`, `task_deps(id)`, `task_halt_downstream(id)`, `task_set_plan_slug(id, slug)`. Same cycle error shape returned to MCP clients.
- [ ] `internal/mcp/...` — **update existing** tools to thread `plan_slug`:
  - `task_create` — add optional `plan_slug` param; persist to the new column. Backwards-compatible (empty string = unaffiliated, matches existing rows).
  - `task_get` — include `plan_slug` in the response payload.
  - `task_list` — include `plan_slug` in each returned task; add an optional `plan_slug` filter alongside the existing `project` filter.
- [ ] `internal/mcp/..._test.go` — round-trip each new tool; update existing tool tests to assert `plan_slug` threads through create → get → list.
- [ ] `README.md` Reference section — append the new MCP tools to the table; update the `task_create` row to note the new `plan_slug` param.

### Phase 5b: orchestrate-stack skill update (optional but recommended)
**Status:** pending

Not in this repo — lives in the dots repo at `agents/skills/orchestrate-stack/SKILL.md` and symlinked to `~/.claude/skills/orchestrate-stack/`. Track here so the DAG view actually groups real stacks; the skill keeps working unmodified if these steps slip, but stacks won't be filterable by `plan_slug`.

- [ ] `agents/skills/orchestrate-stack/SKILL.md` (dots repo) — in the "Stack creation" section, derive a `plan_slug` from the plan filename (or KB doc slug) once at the start, then pass it to every `task_create` call.
- [ ] Same file — in the "Halt conditions" section, replace the manual `task_stop` (in_progress) + `task_archive` (pending) walk with a single `task_halt_downstream(failed_task_id)` call. Keep the old code as a fallback comment for daemons predating the tool.
- [ ] Same file — in the "State persistence" section, record `plan_slug` in the KB state doc so re-invocations and the DAG view can deep-link via `/dag?plan=<slug>`.
- [ ] Update the skill's "Argus primitives required" note to mention `plan_slug`, `task_link`, `task_unlink`, `task_halt_downstream`. If the daemon rejects `plan_slug` as unknown, the skill should gracefully omit it (older daemon = no grouping, still works).

### Phase 6: Gotchas + docs
**Status:** pending

- [ ] `context/knowledge/gotchas/orchestration.md` — append: (a) link mutations run the same cycle DFS as create; the DFS must return the offending path or cycle errors are unactionable. (b) halt-downstream walks reverse-index then re-queries each row's status inside the loop — between `task_list` and `runner.Stop`, depswatcher can flip a `pending` row to `in_progress`. (c) `plan_slug` is opaque to the daemon — same contract as `result`; the orchestrator owns it.
- [ ] `context/knowledge/gotchas/tasklist-ui.md` — append: `l`/`L` keys are TUI-global on DAG tab and the link picker shadows the task-list `l` (URL link picker on agent view). Document the routing.
- [ ] `context/knowledge/gotchas/web-remote.md` — append: `/api/tasks/{id}/links` (URLs from terminal) vs `/api/tasks/{id}/deps` (task linkage) name collision; both endpoints coexist intentionally.
- [ ] `context/knowledge/gotchas/misc.md` or new `gotchas/dag-rendering.md` if section grows past 10 bullets — DAG-specific: (a) cursor-movement must clamp to layer bounds; (b) layered layout assumes `DependsOn` is acyclic (cycle detection in the daemon is the only guarantee); (c) edges drawn with single-line box chars only — mixing single + double lines creates joiner glitches in tcell.
- [ ] `README.md` Reference appendix — DAG tab key bindings + `/dag` web route entry.

## Testing Strategy

- **Schema migration**: open a pre-`plan_slug` DB (test fixture), apply migration, assert column exists with default `''`.
- **RPC**: every new method has a happy-path test + an error-path test (cycle, missing parent, halt on a no-deps task).
- **Cycle detection**: table-driven with at least 5 shapes (self-loop, A→B→A, A→B→C→A, diamond + extra edge, deep cycle 6 levels). Each asserts the returned path.
- **TUI layout**: golden-string tests for ≥4 graph shapes (linear chain, single fan-out, diamond, the MCP V1 stack from the KB handoff).
- **TUI smoke**: DAG tab open/close, branch-change → forceRedraw, mouse click → focus, `l`/`L`/`h` round-trip via SimulationScreen.
- **Web smoke (Playwright)**: render DAG with 3 nodes, click a node, navigate to detail, add a link via UI, assert DAG updates after refresh.
- **Halt-downstream race**: spin a fake depswatcher that flips a `pending` row to `in_progress` mid-walk, assert halt-downstream still cleans up correctly.
- **Coverage gate**: per CLAUDE.md, ≥95% on touched packages, ≥90% on `internal/tui/dagview/` (UI smoke-only).

## Risks & Open Questions

| Risk | Mitigation |
|------|------------|
| Sugiyama-lite produces ugly layouts on heavy fan-out (e.g. M1 → {M2, M3, M8} all base on M1, plus M6F parallel) | Limit to ≤30 nodes per view; fall through to a "this stack is too wide to render — view as list" message. Add a future-work item for `dot` shell-out if users complain. |
| Branch-change callback wired wrong → ghost DAG cells after a status transition | Mandatory smoke test asserts force-redraw log line per CLAUDE.md ui-threading contract. |
| Halt-downstream causes work loss if user fires it on a still-healthy stack | Confirmation modal showing the exact list of tasks that will be stopped/archived before firing. |
| `dagre` is unmaintained; could rot | It's tiny and stable; if it breaks, swap to `elkjs` (more maintained, larger). No churn risk in-session. |
| `/api/tasks/{id}/links` confusion with `/deps` | Documented in `web-remote.md`; consider renaming `/links` to `/urls` in a follow-up PR (out of scope here). |
| depswatcher race on halt-downstream (pending → in_progress mid-walk) | Re-query status inside the loop, fall back from archive→stop when needed. Tested directly. |
| `l`/`L` keys may collide with existing TUI keybindings | `l` is currently used for the URL link picker on the agent view (not the task list); routing must scope by mode. Document in `tasklist-ui.md`. |
| Web SVG sluggish on huge DAGs | Out of scope — 30-node cap covers all real stacks. |

**Open questions:** (none — resolved before /dev)

## Dependencies

- `dagre` (vendored under `internal/api/static/vendor/dagre/`) — MIT.
- No new Go module dependencies.
- Cross-repo edit to the dots repo (`agents/skills/orchestrate-stack/SKILL.md`) for full DAG grouping. Can land independently after the daemon side ships — the skill update is forward-compatible (older daemon ignores `plan_slug`, newer daemon stores it).

## Errors Encountered

| Error | Attempt | Resolution |
|-------|---------|------------|

## Estimated Scope

**Phases:** 7 (6 in-repo + 1 in dots repo for the skill update)
**Tasks:** ~50
**Files touched:** ~27 (5 new under `internal/tui/dagview/`, 1 new `internal/tui/dagpage.go`, +1 new `internal/daemon/cycledetect.go`; edits to schema/tasks/model/rpc/types/handlers/routes/static/MCP/3 gotcha files/README; +1 cross-repo edit to `agents/skills/orchestrate-stack/SKILL.md`)

# Argus plugin substrate – eight opinion-pulls to Darren

## Context

Argus today is a great single-user terminal-native LLM code orchestrator: tasks, worktrees, dependency DAG, peer-to-peer messaging, scheduler, MCP server, web UI, sandbox. It covers ~90% of what any sustained-coordination tool needs.

But every extension lands as a code change to argus itself. The MCP tool surface is hardcoded in `internal/mcp/server.go`. The TUI layout is hardcoded in `internal/tui/taskpage.go`. The settings tab is hardcoded in `internal/tui/settings.go`. Anything that wants to add capability has to either fork argus or wait for an upstream PR.

The original goal of this conversation was to build a coordinator-style overlay ("ludwig") on argus. The reframe: rather than build ludwig as a one-off layer, give argus a generic plugin substrate so ludwig (and any future tool of similar shape – a review bus, a Thanx-context loader, a session triage system) can attach from outside argus's tree.

This plan is the substrate. It is **plugin-neutral**: zero references to ludwig in code or docs except as a hypothetical worked example to validate that the contract is sufficient. It is also **default-UX-preserving**: a user who rebuilds argus with no plugins installed sees and uses exactly today's argus.

The plan ships as eight focused PRs to argus, each independently mergeable, each independently justifiable to Darren on its own merit. After they land, what plugin actually gets built first is a separate decision, informed by the friction of running this very project without that plugin.

## Goals

- Add generic plugin extensibility to argus through eight PRs.
- Every PR passes two tests: plugin-neutral (no specific plugin referenced) and net-zero default UX (no visible change without explicit opt-in).
- Coordinate the buildout itself using argus's existing DAG, messaging, and `plan_slug` grouping. The coordinator (the user) drives from a `/orchestrator` session in CCC while worker tasks ship the eight PRs.
- Conclude with a documented plugin contract (`docs/plugins.md`) that captures auth, endpoints, schemas, and example payloads.

## The eight PRs

### PR 1: Plugin-scoped token type

**Scope.**

- Add a `scope` column to the `api_tokens` table. Tokens with a non-empty scope are plugin tokens.
- Extend the auth middleware to tag requests with `X-Argus-Auth: scope:<name>` (parallel to the existing `master` and `device` tagging).
- New CLI verbs:
  - `argus token mint --scope <name>` (returns the token once)
  - `argus token list` (shows scope alongside type)
  - `argus token revoke <id>`
- No new permission gates yet; downstream PRs gate themselves on `X-Argus-Auth`.

**Pitch to Darren.** Argus has master and device tokens today. A scope-bounded third tier opens space for granular per-integration tokens. Also useful for future per-device permission narrowing.

**Files touched.** `internal/api/auth.go`, `internal/api/tokens.go`, `internal/db/schema.go` (additive migration), `cmd/argus/main.go`.

**Default behavior.** No tokens minted with scope by default; existing master/device tokens unchanged.

**Size.** Small (~half a day).

**Depends on.** Nothing. Goes first.

### PR 2: Event stream (SSE)

**Scope.**

- New endpoint `GET /api/events/stream?since=<cursor>` (Server-Sent Events).
- New `events` table: `(id, type, at, task_id, payload_json)`. Bounded ring – the oldest row is evicted on insert once the table hits the cap (default 10,000 rows).
- Internal helper `events.Emit(type, taskID, payload)` callable from existing emission sites: task lifecycle (`internal/agent/`), messaging (`internal/db/messages.go`), linking (`internal/db/links.go`), session start/exit/idle (`internal/daemon/`).
- Initial event types:
  - `task.created`, `task.status_changed`, `task.completed`, `task.archived`, `task.renamed`, `task.forked`
  - `message.sent`, `message.acked`
  - `link.created`, `link.removed`
  - `session.started`, `session.exited`, `session.idle`
- SSE handler streams events from the supplied cursor, then live. On overflow (cursor older than oldest retained event), the handler emits a `resync` event so the client knows it missed events and should snapshot current state.

**Pitch to Darren.** Argus already emits events through internal Go callback chains. Externalizing them as SSE lets integration tests assert on events instead of polling state, debug tools tail the stream, and the web UI eventually consolidates its per-task `/output` SSE channels.

**Files touched.** `internal/api/events/stream.go` (new), `internal/db/events.go` (new), `internal/db/schema.go`, plus call-site additions wherever events are emitted today.

**Default behavior.** No clients connect by default. The endpoint exists but the daemon's event ring is otherwise dormant.

**Size.** Medium (1-2 days).

**Depends on.** Nothing. Standalone.

### PR 3: task_meta table

**Scope.**

- New `task_meta(task_id, namespace, key, value, updated_at)` table. Composite primary key `(task_id, namespace, key)`.
- HTTP endpoints:
  - `GET /api/tasks/:id/meta?namespace=<ns>` – returns all keys in that namespace.
  - `PUT /api/tasks/:id/meta` – body is `{namespace, key, value}` or a batch form.
- Namespace is auto-derived from the request's auth scope. Plugin tokens can only write into their own namespace. Master tokens can write any namespace.
- No UI surfacing in the TUI by default.

**Pitch to Darren.** The `Task` struct has accumulated optional fields over time (`Pinned`, `Archived`, `PlanSlug`, `DependsOn`, opaque `Result` JSON). Some of those should arguably have been metadata from the start. A proper k/v sidecar keeps future tagging out of the core schema and the migration is purely additive. Useful for personal annotations via CLI even with zero plugins installed.

**Files touched.** `internal/db/task_meta.go` (new), `internal/db/schema.go`, `internal/api/task_meta.go` (new).

**Default behavior.** No metadata written by default. Existing tasks unchanged.

**Size.** Small (~half a day).

**Depends on.** Nothing functionally. Plugin tokens use it via PR 1, but master tokens work without PR 1.

### PR 4: MCP tool registration

**Scope.**

- New endpoint `POST /api/mcp/tools`:

  ```json
  {
    "name": "<scope>_<tool_name>",
    "description": "...",
    "input_schema": { ... },
    "callback_url": "http://127.0.0.1:9991/mcp/...",
    "auth_header": "Bearer ..."
  }
  ```

- New endpoint `DELETE /api/mcp/tools/:name` for unregistration.
- Argus's MCP server registers proxied tools alongside built-ins. On invocation, argus POSTs the tool input as JSON to `callback_url` (with the plugin-supplied `auth_header`), and returns the plugin's response to the MCP client.
- Tool-name namespace enforcement: argus rejects registrations where the tool name does not begin with `<scope>_` (where `<scope>` matches the auth token's scope).
- Lifecycle: tools registered by a plugin are auto-unregistered when its token is revoked, or after the plugin has been silent for the configured idle window (default 10 minutes; survives a daemon restart).

**Pitch to Darren.** Argus's MCP surface is hardcoded; new tools require releases. Runtime registration lets argus act as an MCP composition layer (aggregating multiple MCP servers behind one endpoint). This is the direction the MCP ecosystem is moving generally.

**Files touched.** `internal/mcp/registry.go` (new), `internal/mcp/server.go` (consult registry alongside built-ins), `internal/api/mcp_tools.go` (new).

**Default behavior.** No tools registered at boot. Existing built-in MCP tools work identically.

**Size.** Medium-large (2-3 days; proxy logic and lifecycle handling are non-trivial).

**Depends on.** PR 1 (uses plugin scope for namespace enforcement). Can be developed against a stubbed scope check if PR 1 hasn't merged yet.

### PR 5: Input endpoint formalized as plugin API

**Scope.**

- `POST /api/tasks/:id/input` already exists for the web terminal. Verify it accepts plugin-scoped tokens.
- Add a `from` audit tag to input records: when a plugin token writes input, the entry logs the scope as origin.
- Documented in PR 8.
- Likely no schema changes; mostly auth-middleware verification plus an audit-log column if not already present.

**Pitch to Darren.** Already exists. This formalizes its contract as a stable plugin-callable surface.

**Files touched.** `internal/api/terminal.go` or wherever `/input` lives today, `internal/uxlog/uxlog.go`.

**Default behavior.** Unchanged.

**Size.** Extra small (a few hours, mostly verification and docs).

**Depends on.** PR 1.

### PR 6: Layout registry, streampane widget, example files

**Scope.**

- Refactor `internal/tui/taskpage.go` into a **layout registry**. The current three-panel layout becomes the named default layout `tasks-default`.
- Argus scans `~/.argus/layouts/*.json` at boot. Each file is parsed and registered as a named layout. Default is still `tasks-default`.
- New tview widget `internal/tui/streampane/streampane.go`. Renders ANSI text from a `chan []byte`. Read-only by default, with an optional input-back channel for stream-content sections in PR 7. Damage tracking via `Touched()`.
- Per-pane PTY sizing: refactor `computePTYSize` in `internal/tui/app.go` to take a target `Rect` instead of inferring from the whole screen. Each terminal panel in a multi-pane layout gets sized for its own rect.
- Layout JSON schema v1 (see the plugin contract reference below).
- `docs/examples/layouts/` directory in the repo:
  - `split-horizontal.json` (terminal | terminal, left pinned via bind, right cycles)
  - `split-vertical.json` (terminal / terminal stacked, both cycle independently)
  - `logs.json` (terminal + streampane tailing `~/.argus/ux.log`)
  - `reshuffle.json` (terminal in main slot, file panel on left, git on right – demonstrates the registry isn't gimmicky)
- Argus binds **no hotkeys** to non-default layouts. Layouts are only reachable through the Layouts settings section in PR 7.

**Pitch to Darren.** The hardcoded layout in `taskpage.go` becomes one of many possible. Default boot behavior unchanged. Personal customization is now possible without forking. Examples in `docs/` show the JSON schema as an executable spec.

**Files touched.** `internal/tui/layout/registry.go` (new), `internal/tui/layout/parser.go` (new), `internal/tui/streampane/` (new), `internal/tui/taskpage.go` (refactor), `internal/tui/terminalpane.go`, `internal/tui/app.go`, `docs/examples/layouts/*.json`.

**Default behavior.** Boots to `tasks-default` exactly as today. No example layouts loaded unless the user explicitly copies them into `~/.argus/layouts/`.

**Size.** Large (3-5 days). Per-pane PTY sizing and the layout refactor are the bulk.

**Depends on.** Nothing.

### PR 7: Settings section registry

**Scope.**

- Refactor `internal/tui/settings.go` into a **section registry**. The existing built-in sections (status, sandbox, projects, backends, KB, UX logs) become registry entries.
- New built-in section: **Layouts**. Lists every registered layout, lets the user toggle enabled and bind a hotkey to each. Hides entirely if no non-default layouts are registered.
- New HTTP endpoint `POST /api/plugins/settings/sections` for plugin-registered sections. Body:

  ```json
  {
    "title": "...",
    "type": "form" | "stream",
    "spec": { ... },
    "callback_url": "..."
  }
  ```

- Section content types:
  - **`form`** – typed argus-subset schema. Field types: `bool`, `int`, `string`, `enum`. Common attributes: `key`, `label`, `type`, `default`. Type-specific: `min`/`max` for `int`, `options` for `enum`. Argus renders inputs natively; on user save, posts the `{key: value, ...}` map to `callback_url`.
  - **`stream`** – argus opens a WebSocket to the plugin's `callback_url`. Plugin pushes ANSI content; argus pushes back focused-pane keystrokes. Rendered via the streampane widget from PR 6.
- Section ordering:
  - Built-ins first, in their current hardcoded order (status, sandbox, projects, backends, KB, UX logs, Layouts when present).
  - Blank-line separator.
  - **`Plugins`** header.
  - Plugin sections alphabetical by title.
  - The `Plugins` header (and the preceding separator) hides entirely when no plugin sections are registered – consistent with hide-on-empty for the Layouts section.

**Pitch to Darren.** Settings becomes data-driven. Existing sections move into the registry. The only visible new thing is the Layouts section, which self-hides when there's nothing to manage.

**Files touched.** `internal/tui/settings/registry.go` (new), `internal/tui/settings/form.go` (new), `internal/tui/settings.go` (refactor into registry entries), `internal/api/plugin_settings.go` (new).

**Default behavior.** Settings page renders identically when no plugins or non-default layouts are present.

**Size.** Medium-large (2-3 days; the existing settings refactor is the bulk).

**Depends on.** PR 1 (auth). PR 6 for the streampane widget if implementing `stream` content type. PR 7 can start with only the `form` type and add `stream` once PR 6 lands.

### PR 8: Plugin contract documentation

**Scope.**

- New file `docs/plugins.md` covering:
  - Auth model (token minting, scope binding, header tagging).
  - All five HTTP endpoints with request/response examples.
  - Event types enumerated with their payload shapes.
  - Layout JSON schema (panels, splits, sizes, bindings, hotkeys).
  - MCP tool callback contract (request body shape, expected response shape).
  - Settings section schemas (form fields and stream channel).
  - Versioning policy.
- One small pseudocode example showing the registration sequence for a "hello world" plugin that registers a settings section and one MCP tool.

**Pitch to Darren.** Documents the surface area he just merged. Future contributors don't have to reverse-engineer.

**Files touched.** `docs/plugins.md`.

**Default behavior.** N/A (docs).

**Size.** Small (~1 day).

**Depends on.** PRs 1 through 7.

## Plugin contract reference

This is what PR 8 documents. Including it inline so it's reviewable as a coherent surface.

### Auth

Plugin mints a token via `argus token mint --scope <name>`. The token is shown once and stored on the plugin side. On every request, the plugin sends `Authorization: Bearer <token>`. Argus tags the request internally as `X-Argus-Auth: scope:<name>`. The scope is also the plugin's namespace for metadata and the required prefix for its MCP tool names.

### Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/api/events/stream?since=<id>` | GET (SSE) | Subscribe to typed events |
| `/api/tasks/:id/meta?namespace=<ns>` | GET | Read task metadata |
| `/api/tasks/:id/meta` | PUT | Write metadata in plugin's own namespace |
| `/api/mcp/tools` | POST | Register an MCP tool (prefix-enforced) |
| `/api/mcp/tools/:name` | DELETE | Unregister an MCP tool |
| `/api/tasks/:id/input` | POST | Inject input bytes into a task's PTY |
| `/api/plugins/settings/sections` | POST | Register a settings section |

### Event payloads

Each SSE event has a stable shape:

```json
{
  "id": 12345,
  "type": "task.status_changed",
  "at": "2026-05-23T18:42:01Z",
  "task_id": "abc123",
  "payload": { "from": "pending", "to": "in_progress" }
}
```

Payload shape varies per event type and is enumerated in the docs.

### Layout JSON schema v1

```json
{
  "name": "<unique-name>",
  "title": "Human-readable title",
  "root": {
    "type": "split",
    "direction": "horizontal" | "vertical",
    "sizes": [50, 50],
    "children": [
      { "type": "terminal", "bind": "task:<id>" | "meta:<key>=<value>", "cycle": true },
      { "type": "streampane", "source": "callback:<url>" | "file:<path>" }
    ]
  },
  "hotkeys": {
    "tab": "cycle right",
    "ctrl-1": "focus first",
    "ctrl-2": "focus second"
  }
}
```

Panel types: `terminal`, `task-list`, `git`, `file`, `streampane`, `split`.

Out of scope for v1: borders, conditional rendering, user-resizable splits.

### MCP tool callback

When a Claude session invokes a registered MCP tool, argus POSTs to the registered `callback_url`:

```json
{
  "tool": "<full_tool_name>",
  "input": { ... },
  "context": { "task_id": "<id>", "session_id": "<id>" }
}
```

The plugin responds with MCP's native tool response shape:

```json
{
  "content": [ { "type": "text", "text": "..." } ],
  "is_error": false
}
```

### Settings section schemas

**Form section:**

```json
{
  "title": "Plugin name",
  "type": "form",
  "fields": [
    { "key": "interval", "label": "Check interval (s)", "type": "int", "min": 60, "max": 3600, "default": 300 },
    { "key": "enabled", "label": "Enabled", "type": "bool", "default": true },
    { "key": "backend", "label": "Default backend", "type": "enum", "options": ["claude", "codex"], "default": "claude" },
    { "key": "label", "label": "Display label", "type": "string", "default": "" }
  ],
  "callback_url": "..."
}
```

On user save, argus POSTs `{key: value, ...}` to `callback_url`.

**Stream section:**

```json
{
  "title": "Plugin name",
  "type": "stream",
  "callback_url": "ws://..."
}
```

Argus opens a WebSocket. Plugin streams ANSI bytes; argus pushes back keystrokes as bytes.

### Versioning

The contract ships as `v1`. Plugins should send `X-Argus-Plugin-Version: 1` on every request. Argus rejects requests with unsupported versions. Future breaking changes bump the major; additive changes do not.

## Defaults on previously-unresolved details

These were the loose ends raised in conversation that did not get an explicit decision. The plan adopts these defaults; each can be challenged at review.

1. **MCP proxying**: argus proxies all plugin tool calls. Centralized auth, audit, rate limiting. Plugins do not run their own MCP server.
2. **Event durability**: bounded ring of 10,000 events, replayable via `since=<cursor>`. On overflow, argus emits a `resync` event so clients know they need to resnapshot.
3. **Metadata namespacing**: auto-prefix by plugin scope ID. Plugin writes `role`; argus stores under namespace = `<scope>`, key = `role`. Cross-namespace reads are out of scope for v1.
4. **Plugin lifecycle**: externally managed. Argus authenticates on connect via token; does not start or stop plugins. Settings shows registered plugins and lets the user revoke tokens.
5. **No in-tree reference plugin.** The docs + example layouts are the spec.
6. **Token capabilities (single-user trust model)**: a plugin token can read all events, register tools under its own scope prefix, write metadata in its own namespace, inject input into any task, register one settings section, and register any number of layouts.
7. **MCP tool name enforcement**: force-prefix with `<scope>_`. Argus rejects registrations that violate.
8. **Layout JSON schema v1**: panels + splits + sizes + bindings + hotkeys. No borders, no conditional rendering, no user-resizable splits.

## Worked example: a hypothetical "ludwig" plugin

This is included only to validate that the contract is sufficient to host a coordinator-shaped plugin. It is not a design document for ludwig; the real ludwig (if it gets built) is a separate workstream.

After the substrate lands, a coordinator plugin called `ludwig` would:

- Mint a scope token: `argus token mint --scope ludwig`.
- Subscribe to `/api/events/stream` for `task.*` and `message.*` events.
- Register MCP tools with forced prefix: `ludwig_decision_add`, `ludwig_question_add`, `ludwig_question_resolve`, `ludwig_thread_set_status`, `ludwig_ask`, `ludwig_update`, `ludwig_join`.
- Write metadata: `ludwig.role` (`coordinator` / `worker` / `freelance`), `ludwig.thread_status`.
- Register one settings section "Ludwig" – a form for cadence config, plus a stream block listing active orchestrators.
- Register a `ludwig-split` layout (left pinned to `meta:ludwig.role=coordinator`, right cycles `meta:ludwig.role=worker`).
- Run its own scheduler that pokes worker tasks via `POST /api/tasks/:id/input` to fire `/check-messages` on a cadence.
- Store its own state (decisions, questions, threads, freeform notes) in `~/.ludwig/*.sqlite`.

If the substrate is sufficient to host this, we are done. If a gap is discovered when ludwig (or any plugin) is later built, that gap becomes a new argus PR following the same model.

## Practical mechanics for building this in argus

The buildout is itself an argus project, coordinated through argus's existing tools.

- **Tasks.** Create 8 tasks via `task_create`, one per PR. Suggested names: `pr-1-tokens`, `pr-2-events`, `pr-3-task-meta`, `pr-4-mcp-register`, `pr-5-input`, `pr-6-layout`, `pr-7-settings`, `pr-8-docs`.
- **Branches.** All eight forked off `master`. Branch names mirror task names (e.g., `argus-plugin-tokens`).
- **Grouping.** Tag all eight with `plan_slug = argus-plugin-substrate` so the DAG view groups them.
- **Dependencies.** Wire with `task_link`:
  - PR 1 → PR 4, PR 5, PR 7
  - PR 6 → PR 7 (for the stream content type; PR 7 can soft-defer the stream type and ship form-only first)
  - PRs 1 through 7 → PR 8
- **Coordination.** The user runs `/orchestrator` (CCC) in a session parallel to the workers. Orchestrator name: `argus-plugin-substrate`. Worker tasks report progress via `task_message_send --kind update`. The coordinator polls inboxes via `task_inbox` manually.
- **Merge order.** PR 1 first, then PRs 2/3/5 in any order, then PR 4 once PR 1 is in, then PR 6, then PR 7 (full version once PR 6 is in), then PR 8 once everyone else has landed.

The act of running this build without ludwig is a forcing function: every annoyance encountered (manual inbox polling, manual decision logging, manual status tracking) becomes a concrete feature ludwig should fix. The friction log informs ludwig's MVP scope better than any upfront design.

## Verification

End-to-end verification is per-PR plus a final substrate check.

**Per-PR verification.** Each PR ships with:

- Unit tests for new types, helpers, and HTTP handlers (`internal/api/*_test.go`, `internal/db/*_test.go`).
- A small integration test in `internal/api/testutil/` exercising the new endpoint with a fake plugin token.
- For PRs 6 and 7: smoke tests in `internal/tui/smoke_test.go` using SimulationScreen to verify the layout/settings refactor doesn't break the default UX.
- Manual check: `make test && make vet && make lint-pr && make fmt-check` all green.

**Substrate-level verification** (after all 8 PRs land):

1. **Default UX preserved.** Boot argus with an empty `~/.argus/` (or after a fresh build). Run through the existing main flows: task list, agent view, settings (status / sandbox / projects / backends / KB / UX logs), web UI, MCP tools (`task_*`, `schedule_*`, `kb_*`). Everything renders and behaves identically to current `master`. No new tabs, no new keybindings active, no new settings sections visible.

2. **Plugin token round-trip.** `argus token mint --scope test-plugin` returns a token. Use it to:
   - Open SSE to `/api/events/stream` and observe live events as tasks are created/messaged.
   - PUT a metadata key via `/api/tasks/:id/meta`, GET it back, verify auto-namespacing.
   - POST a tool registration to `/api/mcp/tools` with prefix `test-plugin_hello`. Invoke it from a Claude session in any worktree; verify argus proxies the call to the registered callback URL.
   - POST a settings section. Verify it appears in the TUI under the `Plugins` header.
   - POST input to `/api/tasks/:id/input` and verify the running session receives it.

3. **Layout round-trip.** Copy `docs/examples/layouts/split-horizontal.json` into `~/.argus/layouts/`. Verify it appears in Settings → Layouts. Bind a hotkey. Press it. Two terminal panes appear with correct per-pane PTY sizing.

4. **Hide-on-empty.** With everything removed (no layouts copied, no plugins registered), verify the Layouts settings section and the `Plugins` header (and its separator) are both absent.

5. **Event resync.** Connect with `since=<very-old-cursor>`. Verify argus emits a `resync` event when the cursor predates the retained ring.

## Open questions for the user

1. **Repo target.** Send the PRs to `anutron/argus` (the user's fork) or upstream to Darren's repo? If upstream, do we want Darren's blessing on the overall direction before opening PR 1, or do we open PR 1 itself as the proposal vehicle?

2. **Branch naming.** `argus-plugin-<short-name>` (descriptive) or `plugin/tokens` style (path-style)? Either works; argus's existing branches lean descriptive.

3. **Schema migration ordering.** PR 1 (`api_tokens`), PR 2 (`events`), PR 3 (`task_meta`), and PR 6 (possibly a `layouts` table) all touch `internal/db/schema.go`. Pre-allocate migration numbers in PR 1 to reserve ordering, or let merge order dictate?

4. **Shared test harness.** Worth landing `internal/api/testutil/plugin_mock.go` (a reusable fake-plugin harness) in PR 1 or PR 2 for everyone else to reuse, or per-PR ad-hoc fakes?

5. **`argus plugin` CLI subcommand.** Is there value in `argus plugin register` / `argus plugin list` / `argus plugin revoke` alongside the HTTP endpoints? Or do plugins manage their own CLI integration entirely?

6. **Streampane keystroke routing.** When a stream-content section is focused, do keystrokes route to the plugin verbatim? Argus probably wants to reserve at least `esc` (exit focus) and `ctrl-c` (interrupt). Worth deciding before PR 6 / 7.

7. **task_meta size cap.** Unbounded with monitoring, or a soft cap (e.g., 64 KiB per task per plugin) from the start? Default in this plan is unbounded; flag if you want a cap.

8. **Plugin contract versioning.** Ship as `v1` from day one with `X-Argus-Plugin-Version` header enforced, or introduce versioning only when we need to break compat? Default is `v1` from day one.

9. **CCC `/orchestrator` name.** The plan uses `argus-plugin-substrate`. Confirm this won't collide with anything else you're running, or pick a different name.

10. **`default-layout` config knob.** Should PR 6 also ship a config that lets the user pick their boot layout from `~/.argus/layouts/`, or is that a phase-2 enhancement (boot stays hardcoded to `tasks-default`)?

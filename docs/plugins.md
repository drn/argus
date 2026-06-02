# Argus plugin contract (v1)

This document describes the HTTP, MCP, and TUI extension surface a plugin can attach to. The surface is plugin-neutral — argus ships with no first-party plugins, and a stock build with no plugins installed behaves exactly like argus before the substrate landed.

A plugin is any external process that holds an `argus token mint --scope <name>` token and talks to the daemon's HTTP API on `127.0.0.1:7743` (or the Tailscale IP). Argus does not manage plugin processes; lifecycle (start, restart, supervise) is the plugin author's problem.

## Status

Contract version: **v1**.

Plugins should send `X-Argus-Plugin-Version: 1` on every request. Argus does not currently enforce the header — additive changes through the v1 window stay backwards compatible. A future major version bump will start rejecting requests without the header set, so wire it in now.

Future breaking changes (event renames, endpoint relocations, schema changes that drop fields) bump the major. Additive changes (new event types, new endpoints, new optional fields) do not.

## Authentication

### Minting a token

Mint a plugin token from the host running the daemon:

```bash
argus token mint --scope my-plugin
```

Scopes must match `[a-z0-9][a-z0-9_-]{0,63}`. Argus prints the plaintext token once. Store it on the plugin side; it is not recoverable.

The CLI manages the local SQLite token table directly (WAL-mode SQLite accepts a writer alongside the daemon's readers), so plugin setup works before the daemon is installed as a service.

Other CLI verbs:

- `argus token list` — show every token with its type (`master` / `device` / `scope:<name>`), label, last4, created/last-used timestamps, and revoked flag.
- `argus token revoke <id>` — revoke a token by its numeric id from `argus token list`.

### Sending credentials

Every request carries either:

- `Authorization: Bearer <token>` — preferred.
- `?token=<token>` query param — required for `EventSource`, which cannot set headers.

### How the daemon tags requests

The auth middleware sets one of three values on the inbound `X-Argus-Auth` request header before any handler runs:

| Tag             | Origin                                            |
| --------------- | ------------------------------------------------- |
| `master`        | the master token loaded from `~/.argus/api-token` |
| `device`        | a per-device token (scope is empty)               |
| `scope:<name>`  | a plugin-scoped token                             |

Handlers branch on this tag to gate destructive verbs and to derive a plugin's namespace.

### Token capabilities (single-user trust model)

A `scope:<name>` token can:

- Read every event from `/api/events/stream` (events are not filtered per scope).
- Register MCP tools under the `<name>_` prefix (any number, capped at 100 per scope).
- Write task metadata in namespace `<name>`. Reads are open to all authenticated tokens; cross-namespace writes are rejected.
- Inject input into any task via `POST /api/tasks/:id/input`. The audit log stamps `origin=scope:<name>` on the write.
- Register exactly one settings section. Re-registering with the same title replaces the prior entry.

A revoked token loses all of the above; argus cascades the revocation by dropping every MCP tool and settings section owned by the scope.

## Endpoints

| Endpoint                                                | Method     | Auth                          | Purpose                                |
| ------------------------------------------------------- | ---------- | ----------------------------- | -------------------------------------- |
| `/api/events/stream?since=<id>`                         | GET (SSE)  | any authenticated             | Subscribe to the daemon event stream   |
| `/api/tasks/:id/meta?namespace=<ns>`                    | GET        | any authenticated             | Read task metadata                     |
| `/api/tasks/:id/meta`                                   | PUT        | `master` or `scope:<name>`    | Write task metadata (scope auto-derives namespace) |
| `/api/tasks/:id/input`                                  | POST       | any authenticated             | Inject input bytes into a task PTY     |
| `/api/mcp/tools`                                        | POST       | `scope:<name>`                | Register an MCP tool                   |
| `/api/mcp/tools/:name`                                  | DELETE     | owning scope or `master`      | Unregister an MCP tool                 |
| `/api/plugins/settings/sections`                        | GET        | any authenticated             | List registered plugin settings sections |
| `/api/plugins/settings/sections`                        | POST       | `scope:<name>`                | Register a settings section            |
| `/api/plugins/settings/sections/:scope/:title`          | DELETE     | owning scope or `master`      | Unregister a settings section          |
| `/api/plugins/views`                                    | POST       | `master` or `scope:<name>`    | Register a top-level plugin view       |
| `/api/plugins/views`                                    | GET        | `master` or `scope:<name>`    | List plugin views (scope-filtered)     |
| `/api/plugins/views/:id`                                | DELETE     | owning scope or `master`      | Unregister a plugin view               |

All endpoints return JSON. Errors are `{"error": "..."}` with the appropriate HTTP status. Bodies are capped (1 MiB for metadata, 256 KiB for MCP tool registration and section registration, 64 KiB for input).

### Plugin token version header

```
X-Argus-Plugin-Version: 1
```

Send on every request. Today the header is observed but not validated; expect future major versions to reject mismatches.

## Event stream

`GET /api/events/stream?since=<cursor>` is a Server-Sent Events channel. Each event arrives in the standard SSE block:

```
event: task.status_changed
data: {"id":12345,"type":"task.status_changed","at":"2026-05-23T18:42:01Z","task_id":"abc123","payload":{"from":"pending","to":"in_progress"}}

```

The `event:` field carries the type so clients can dispatch with `addEventListener("task.status_changed", ...)` instead of parsing every `data:` line.

### Cursor and replay

`since=<id>` is **exclusive** — passing the last id you saw delivers everything after it. Pass `0` (or omit the query) to subscribe live only.

The handler is fenced: it subscribes to the live bus BEFORE snapshotting the latest persisted id, then replays `[since+1, replayEnd]` from the DB, then resumes live, dropping any live event whose id falls within the replayed range. No event is delivered twice; no event is missed.

### Resync

Events live in a bounded ring (default 10,000 rows; the oldest is evicted on each insert). When `since` predates the oldest retained event, history has rotated out from under you. Argus emits a synthetic event before any replay:

```
event: resync
data: {"id":0,"type":"resync","at":"2026-05-23T18:42:01Z","payload":{"reason":"cursor_older_than_ring","cursor":<your-cursor>,"oldest":<oldest-id>}}
```

`resync` is never persisted — clients seeing it should snapshot daemon state (`GET /api/tasks`, etc.) before resuming the stream.

### Keepalives

Argus sends `: ping` lines every 30 seconds so proxies and clients can detect dead connections.

### Event types

| Type                    | Payload                                              | Emission site                                |
| ----------------------- | ---------------------------------------------------- | -------------------------------------------- |
| `task.created`          | `{"name":"...","project":"...","status":"pending"}`  | `db.Add`                                     |
| `task.status_changed`   | `{"from":"<status>","to":"<status>"}`                | `db.Update` when status changes              |
| `task.completed`        | `null`                                               | `db.Update` when status transitions to `complete` |
| `task.archived`         | `null`                                               | `db.Archive`                                 |
| `task.renamed`          | `{"from":"<name>","to":"<name>"}`                    | `db.Rename` / `db.RenameIfName`              |
| `task.forked`           | `{"from_task_id":"<id>","to_task_id":"<id>"}`        | `POST /api/tasks/:id/fork`                   |
| `message.sent`          | `{"id":<n>,"from":"<id>","to":"<id>","kind":"..."}`  | `db.SendMessage`                             |
| `message.acked`         | `{"count":<n>}`                                      | `db.AckMessages`                             |
| `link.created`          | `{"child":"<id>","parent":"<id>"}`                   | `orch.Link`                                  |
| `link.removed`          | `{"child":"<id>","parent":"<id>"}`                   | `orch.Unlink`                                |
| `session.started`       | `{"pid":<n>,"resume":<bool>}`                        | `agent.Runner.Start`                         |
| `session.exited`        | `{"stopped":<bool>,"err":"<str>","pending_restart":<bool>}` | session readLoop exit                  |
| `session.idle`          | `null`                                               | push idle watcher                            |
| `resync`                | `{"reason":"...","cursor":<n>,"oldest":<n>}`         | synthetic, SSE handler only                  |

Payloads are stable. A future major version will rename or restructure them; v1 is append-only.

Statuses cited in `task.status_changed` payloads are the canonical `model.Status` strings: `pending`, `in_progress`, `in_review`, `complete`.

## Task metadata sidecar

A free-form key/value sidecar table keyed by `(task_id, namespace, key)`. Plugins use it to annotate tasks without piling new columns onto the `tasks` schema.

### Read

```
GET /api/tasks/<task-id>/meta?namespace=<ns>
```

Returns every row for the task, optionally filtered by namespace. The body shape:

```json
{
  "entries": [
    { "namespace": "my-plugin", "key": "role", "value": "coordinator", "updated_at": "2026-05-23T18:42:01Z" }
  ]
}
```

Reads are open to any authenticated token. Cross-namespace reads (no `?namespace=`) are allowed.

### Write

```
PUT /api/tasks/<task-id>/meta
Content-Type: application/json

{ "namespace": "my-plugin", "key": "role", "value": "coordinator" }
```

Or in batch form:

```json
{ "namespace": "my-plugin", "entries": { "role": "coordinator", "thread_status": "open" } }
```

Exactly one of `(key, value)` or `entries` must be set. The handler rejects an empty namespace.

Today the handler requires the master token (see footnote on the endpoint table above). Future work auto-derives the namespace from the auth tag for plugin-scoped writes.

The HTTP body cap is 1 MiB. Per-key value size is otherwise unbounded.

## MCP tool registration

A plugin can extend argus's MCP surface at runtime. Argus proxies tool invocations from any connected Claude session through to the plugin's HTTP callback URL and returns the plugin's response to the client.

### Register

```
POST /api/mcp/tools
Authorization: Bearer <scope-token>
Content-Type: application/json

{
  "name": "my-plugin_hello",
  "description": "Say hello",
  "input_schema": { "type": "object", "properties": { "name": { "type": "string" } } },
  "callback_url": "http://127.0.0.1:9991/mcp/hello",
  "auth_header": "Bearer <secret-shared-with-plugin>"
}
```

**Name prefix is enforced.** The tool name MUST start with `<scope>_` (here, `my-plugin_`) — `name == prefix` (nothing after the underscore) is also rejected. The allowed character set is ASCII alphanumerics plus `_` and `-`, max 128 bytes.

Other caps:

- `description`: 4 KiB
- `input_schema`: 64 KiB; must be valid JSON; defaults to `{}` when empty
- `callback_url`: 2048 bytes, must be `http://` or `https://`
- `auth_header`: 4 KiB
- Per-scope tool count: 100

A re-POST with an existing `name` (idempotent upsert) refreshes the row's `LastSeenAt` heartbeat. A re-POST with a `name` registered by a different scope is rejected.

Response on success:

```json
{ "name": "my-plugin_hello", "scope": "my-plugin" }
```

`201 Created` on first registration, `200 OK` is reserved for the heartbeat path through the registry (the API currently returns `201` for upserts; clients should treat both as success).

### Unregister

```
DELETE /api/mcp/tools/my-plugin_hello
```

Allowed for the owning scope, or for the master token (operator cleanup). A `device` token cannot unregister anything. Idempotent — deleting a missing name returns `200 OK`.

### Lifecycle

A registered tool is dropped when any of these happens:

- The owning token is revoked. Cascade clears every tool under that scope.
- The sweeper runs and finds `LastSeenAt` older than the idle window (default 10 minutes). Re-POST the registration on a cadence shorter than the window to keep the tool alive. Every successful invocation also refreshes the heartbeat.
- The plugin explicitly deletes the tool.

The registry persists in SQLite, so registrations survive a daemon restart.

### Callback contract

When a Claude session invokes the registered tool, argus POSTs to `callback_url`:

```
POST <callback_url>
Authorization: <auth_header>          # exactly the string the plugin registered
Content-Type: application/json

{
  "tool": "my-plugin_hello",
  "input": { "name": "world" }
}
```

Plugins follow argus's MCP convention — callers identify themselves via tool inputs (typically a `cwd` parameter, matching `task_complete` and other built-ins). Argus does not auto-inject caller identity into the proxy payload.

The pattern: declare `cwd` in your tool's `input_schema`, instruct the agent (via the tool's `description`) to pass `$PWD`, then resolve the calling task on the plugin side from the cwd:

```json
{
  "name": "my-plugin_hello",
  "description": "Say hello. Always pass the current working directory as `cwd` (use $PWD).",
  "input_schema": {
    "type": "object",
    "properties": {
      "cwd":  { "type": "string", "description": "Pass $PWD" },
      "name": { "type": "string" }
    },
    "required": ["cwd"]
  },
  "callback_url": "http://127.0.0.1:9991/mcp/hello",
  "auth_header": "Bearer ..."
}
```

The plugin must respond with the MCP-native tool result shape:

```json
{
  "content": [ { "type": "text", "text": "hello, world" } ],
  "isError": false
}
```

Notes:

- The HTTP timeout for a single callback is 30 seconds. Plugins that need longer should respond with an in-progress acknowledgement and stream completion via events.
- Responses with status `>= 400` propagate to the MCP client as a tool error containing the response body (first 512 bytes).
- Response bodies are capped at 4 MiB.

## Input injection

```
POST /api/tasks/<task-id>/input
Content-Type: text/plain

<raw bytes — up to 64 KiB>
```

Bytes are written verbatim to the task's PTY. Audit log entries stamp `origin=<auth-tag>` (`master`, `device`, or `scope:<name>`) so writes from a plugin token are attributable post-hoc.

Returns `{"status":"ok","bytes":<count>}`. Returns 404 with `{"error":"no active session"}` if the task has no running PTY.

## Settings sections

A plugin can register one settings section that appears in the TUI Settings page under a "Plugins" header. The TUI renders the section natively; plugin code never touches tcell.

### Register

```
POST /api/plugins/settings/sections
Authorization: Bearer <scope-token>
Content-Type: application/json

{
  "title": "My plugin",
  "type": "form",
  "callback_url": "http://127.0.0.1:9991/settings/save",
  "fields": [
    { "key": "interval", "label": "Check interval (s)", "type": "int", "min": 60, "max": 3600, "default": 300 },
    { "key": "enabled",  "label": "Enabled",            "type": "bool", "default": true },
    { "key": "backend",  "label": "Default backend",    "type": "enum", "options": ["claude", "codex"], "default": "claude" },
    { "key": "label",    "label": "Display label",      "type": "string", "default": "" }
  ]
}
```

The `spec` envelope (`"spec": { "fields": [...] }`) is also accepted; inline `fields` is the preferred shape.

Validation:

- `title` and `callback_url` are required.
- `type` defaults to `form` when omitted; `stream` is reserved for a future PR and currently rejected at registration.
- Field keys must be unique within the form.
- `int` fields with both `min` and `max` require `min ≤ max`.
- `enum` fields require a non-empty `options` array. Any `default` must appear in `options`.
- Each `default` (when set) must match the field's declared type.

Field types:

| Type     | JSON shape    | Default | Extras                |
| -------- | ------------- | ------- | --------------------- |
| `bool`   | `true`/`false`| `false` | —                     |
| `int`    | number        | `0`     | `min`, `max` (both optional) |
| `string` | string        | `""`    | —                     |
| `enum`   | string        | `""`    | `options` (required)  |

Re-registering with the same `(scope, title)` replaces the prior row — plugins register exactly one section per scope.

### Section type: `stream`

Reserved. PR 7 of the substrate ships form-only; a follow-up adds the streaming variant. Plugins should not register `type: "stream"` today.

When stream lands, the registered shape will be:

```json
{
  "title": "My plugin live view",
  "type": "stream",
  "callback_url": "ws://127.0.0.1:9991/settings/stream"
}
```

Argus will open a WebSocket; the plugin pushes ANSI bytes and argus pushes back focused-pane keystrokes through the streampane widget.

### List

```
GET /api/plugins/settings/sections
```

Open to any authenticated token. Returns:

```json
{
  "sections": [
    {
      "scope": "my-plugin",
      "title": "My plugin",
      "type": "form",
      "callback_url": "http://127.0.0.1:9991/settings/save",
      "fields": [ ... ]
    }
  ]
}
```

### Save flow

When the user edits a section in the TUI and saves, argus POSTs the `{key: value, ...}` map to the plugin's `callback_url`:

```
POST <callback_url>
Content-Type: application/json

{ "interval": 600, "enabled": true, "backend": "claude", "label": "" }
```

The daemon proxies the save (the TUI in `--remote` mode is often on a phone with no LAN route to the plugin). The plugin's status and response body are returned verbatim to the TUI. Plugin response time is capped at 10 seconds.

### Unregister

```
DELETE /api/plugins/settings/sections/<scope>/<title>
```

A plugin can unregister only its own section. The master token can drop any section (operator cleanup). The substrate's revocation cascade also clears every section owned by a scope when its token is revoked.

### Ordering and hide-on-empty

Built-in settings sections render first in their canonical order, followed by a blank-line separator and a `Plugins` header before alphabetically-sorted plugin sections. When no plugin sections exist, both the `Plugins` header and the preceding separator disappear.

## Plugin views

A plugin can register a top-level view — its own full-screen page in the TUI, listed alongside argus's built-in tabs. The plugin renders the page itself: argus opens a WebSocket to the view's `callback_url` when the user activates it (via the registered hotkey), pipes the plugin's ANSI bytes into a terminal pane that fills the screen, and forwards keystrokes back to the plugin.

Register / list / unregister via the `/api/plugins/views` endpoints in the table above. Activation, the WebSocket protocol, and the key-handling contract are described below.

### The WebSocket protocol

Once activated, argus dials `callback_url` and runs a read pump and a write pump for the life of the connection. Two frame types flow in each direction:

| Direction        | Frame type | Carries                                                                 |
| ---------------- | ---------- | ----------------------------------------------------------------------- |
| argus → plugin   | binary     | Keystrokes, encoded as the raw terminal bytes (see "Key encoding" below) |
| argus → plugin   | text       | A control envelope: `resize` / `focus` / `blur`                          |
| plugin → argus   | binary     | ANSI bytes for the plugin's pane (full-screen terminal output)          |
| plugin → argus   | text       | A control envelope: `release` / `hotkeys` / `help`                       |

The split is strictly by WebSocket frame type — binary frames are the ANSI/keystroke fast path, text frames are JSON control envelopes. A malformed text frame is logged and dropped; it never stalls the pump or disturbs the binary stream.

The connector disables the per-message read cap (`SetReadLimit(-1)`) because a full-screen ANSI surface with per-cell SGR colors routinely exceeds the 32 KiB default. This is safe only because the connector is loopback-only — argus and the plugin daemon both bind `127.0.0.1`.

### Control envelopes

**argus → plugin** (sent by argus as TEXT frames):

| Envelope                                   | When argus sends it                                                        |
| ------------------------------------------ | -------------------------------------------------------------------------- |
| `{"type":"resize","cols":N,"rows":M}`      | On initial connect, and on every terminal resize while the view is active. |
| `{"type":"focus"}`                         | On activation, after the first resize.                                     |
| `{"type":"blur"}`                          | Just before argus closes the connection (deactivation).                    |

**plugin → argus** (sent by the plugin as TEXT frames, decoded defensively by argus):

| Envelope                                                                  | Effect                                                                                                                                                              |
| ------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `{"type":"release"}`                                                      | Hand the keyboard back to argus. argus blurs + closes the connection and returns to the task list. This is the plugin's normal "I'm done" exit.                     |
| `{"type":"hotkeys","items":[{"key":"^F","label":"next pane","bar":true}]}` | Push (or replace) the plugin's hotkey dictionary. Items with `"bar":true` populate the context-sensitive bottom bar; the full set (bar flag ignored) drives the `?` help overlay. Re-pushing replaces the prior dictionary live. |
| `{"type":"help"}`                                                         | Pop argus's help overlay, rendering the plugin's full hotkey dictionary styled like argus help. The overlay shows only the plugin's hotkeys.                        |

Unknown `type` values and malformed JSON are logged and ignored — they do not disrupt the binary stream or deactivate the view. The `key` / `label` / `bar` fields in a `hotkeys` item are display-only: argus never binds them itself (it has surrendered the keyboard), it only advertises them.

### Full key surrender

While a plugin view holds the keyboard, **argus forwards every key to the plugin and reserves nothing for its own navigation.** Esc, Ctrl+C, `?`, tab-switch number keys, and modified arrows all reach the plugin's pane — none trigger an argus action. This is a hard departure from argus's normal global key routing: a plugin view is the one mode where argus is a transparent pipe.

There are exactly **two** keys argus intercepts in this mode:

- The **double-Ctrl+Q failsafe** (below).
- A **single key to dismiss the help overlay**, while that overlay is visible (see "Help overlay").

Everything else, including a single `?` and a single Ctrl+Q, is forwarded.

### The double-Ctrl+Q failsafe

So a hung or misbehaving plugin can never trap the keyboard, argus watches for two Ctrl+Q presses within ~400 ms:

- A **single** Ctrl+Q is forwarded to the plugin like any other key (the plugin may bind it).
- A **second** Ctrl+Q **within ~400 ms** is intercepted by argus — not forwarded — and force-returns control to argus (equivalent to the plugin sending `release`).
- Two Ctrl+Q presses **more than ~400 ms apart** are both forwarded; the failsafe does not fire.

The bottom bar advertises this as the reserved exit hint `^Q^Q argus`, rendered flush-right and never displaceable by plugin hints. Because Esc is surrendered, the double-Ctrl+Q is the only argus-guaranteed escape.

### The bottom bar and help overlay

While a view is active, the bottom status bar switches to plugin mode:

- The left side shows a `▶ <plugin> has the keyboard` affordance.
- The right side shows the plugin's `bar:true` hotkeys (capped in count and width, truncated if they overflow), followed by the reserved `^Q^Q argus` exit hint, which is always rendered last and can never be pushed off.
- With no dictionary pushed yet, only the fallback affordance + exit hint render.

A `{"type":"help"}` frame pops argus's help modal showing the plugin's full dictionary (every item, the `bar` flag ignored). Because argus has surrendered `?`, the plugin decides when help appears — typically by binding `?` itself and sending the `help` frame in response. While the overlay is up, the next single key dismisses it; the overlay does not capture the keyboard beyond that one dismissal.

### Key encoding (argus → plugin keystrokes)

argus encodes keystrokes into the raw bytes a terminal application expects, using standard xterm conventions — the same encoder the agent PTY uses, so a plugin pane behaves like any other terminal:

- Runes: plain → UTF-8 bytes; with Alt → ESC-prefixed.
- Arrows / Home / End / PgUp / PgDn: unmodified → the bare CSI final; modified → the xterm form `CSI 1 ; <mod> <final>`, where `<mod> = 1 + Shift(1) + Alt(2) + Ctrl(4)`. So `Ctrl+Right` → `\x1b[1;5C`, `Shift+Right` → `\x1b[1;2C`, `Ctrl+Alt+Right` → `\x1b[1;7C`.
- Ctrl+letter → the C0 control byte (e.g. Ctrl+Q → `0x11`).
- Enter → CR; Shift/Alt+Enter → ESC+CR (newline-insert). Tab → HT; Backtab → `CSI Z`. Backspace → `0x7f`; Delete → `CSI 3 ~`.

#### iTerm2 Cmd → Ctrl+Alt remap

A plugin can bind **Cmd+arrow** on macOS by configuring iTerm2 to send Cmd+arrow as the **Ctrl+Alt (mod 7)** xterm form. iTerm2 delivers Cmd+Right as `\x1b[1;7C`, which argus round-trips faithfully to the plugin (argus does not special-case it — `Ctrl+Alt` is just `<mod> = 7` in the standard encoding). The plugin sees the exact `\x1b[1;7<final>` sequence and can map it to a Cmd+arrow binding.

## Versioning

The plugin contract ships as `v1`. Plugins should send:

```
X-Argus-Plugin-Version: 1
```

on every request. Today the header is not enforced (the daemon accepts requests without it). A future major bump will start rejecting requests missing or mismatching the header; wire it in now and the upgrade is a single-line change.

Additive changes through v1:

- New event types (clients ignore unknown `event:` lines).
- New endpoints under `/api/`.
- New optional fields on existing JSON bodies.

Breaking changes (require major bump):

- Renaming or removing an event type.
- Changing the field name or type within an event payload.
- Removing or renaming an endpoint.
- Changing the prefix-enforcement rule on MCP tool names.

## A hello-world plugin

The shortest plausible plugin that exercises the contract end-to-end:

```pseudo
# 1. One-time setup, run on the host that hosts argus:
$ argus token mint --scope hello
id:    7
scope: hello
label: hello
token: <copy-this>

Store this token now — it will not be shown again.

# 2. Plugin process (run anywhere reachable from the daemon's network bind):

ARGUS = "http://127.0.0.1:7743/api"
TOKEN = "<the token from step 1>"

def headers():
    return {
        "Authorization": f"Bearer {TOKEN}",
        "X-Argus-Plugin-Version": "1",
    }

# 2a. Subscribe to events.
async for ev in sse_connect(f"{ARGUS}/events/stream", headers=headers()):
    if ev.type == "task.created":
        log(f"new task {ev.data.task_id}")

# 2b. Register a settings section.
http.post(
    f"{ARGUS}/plugins/settings/sections",
    headers=headers(),
    json={
        "title": "Hello plugin",
        "type":  "form",
        "callback_url": "http://127.0.0.1:9991/settings/save",
        "fields": [
            { "key": "greeting", "label": "Greeting", "type": "string", "default": "hello" }
        ],
    },
)

# 2c. Register one MCP tool. The prefix MUST match the scope.
# The description tells the agent to pass $PWD as cwd; the plugin uses cwd
# to resolve which task is calling.
http.post(
    f"{ARGUS}/mcp/tools",
    headers=headers(),
    json={
        "name":         "hello_say",
        "description":  "Say hello. Always pass the current working directory as `cwd` (use $PWD).",
        "input_schema": {
            "type": "object",
            "properties": {
                "cwd":  { "type": "string", "description": "Pass $PWD" },
                "name": { "type": "string" },
            },
            "required": ["cwd"],
        },
        "callback_url": "http://127.0.0.1:9991/mcp/say",
        "auth_header":  "Bearer plugin-internal-secret",
    },
)

# 2d. Serve the two callbacks.
def on_settings_save(body):
    # body is the {key: value} map argus POSTed.
    return ok()

def on_mcp_say(body):
    # body is {"tool": "...", "input": {"cwd": "...", "name": "world"}}
    task = resolve_task_by_cwd(body["input"]["cwd"])  # plugin-side lookup
    name = body["input"].get("name", "world")
    return {
        "content": [{ "type": "text", "text": f"hello, {name} (from task {task})" }],
        "isError": False,
    }
```

That's the whole contract. Argus does the rest:

- The Settings page now has a `Plugins` header with `Hello plugin` underneath.
- Any Claude session in any worktree can call `hello_say` as an MCP tool.
- The plugin sees every event the daemon emits and can react on a cadence of its choosing.

## Known divergences from PLAN.md

The substrate was specified up front in `PLAN.md` and implemented across seven independent PRs. A handful of things shifted between plan and ship. Plugin authors should code against this document; the divergences below are recorded so a reader who started from the plan can recalibrate.

- **`X-Argus-Plugin-Version` is observed, not enforced.** The plan says "Argus rejects requests with unsupported versions." The middleware does not check the header today. The recommendation to send `X-Argus-Plugin-Version: 1` stands — when enforcement lands as part of a major bump, that line keeps every existing plugin working.

- **Settings section type `stream` ships end-to-end.** Plugins can register `type: "stream"` and the TUI opens a WebSocket to `callback_url` when the user focuses the rail entry, piping received ANSI bytes into a streampane filling the right pane. The connector closes on blur (category change or section unregister); the streampane survives focus toggles so re-entering the section preserves received content. Stream sections must not declare `fields` — registration rejects the combination.

- **MCP tool registration responds `201 Created` even on the heartbeat upsert path.** The plan implied `200 OK` on idempotent re-registration. The API currently returns `201` for both first-create and refresh; the registry distinguishes the two internally but the HTTP layer does not. Clients should treat both as success.

- **Schema migration ordering.** Open question 3 in the plan asked whether to pre-allocate migration numbers in PR 1. We did not; migrations landed in merge order. No external contract impact, but anyone scanning `internal/db/schema.go` should know the version numbers reflect what merged when, not the order in the plan.

- **No `argus plugin` CLI subcommand.** Open question 5 in the plan. Plugins manage themselves; `argus token mint --scope` is the only CLI surface argus offers a plugin author. Listing registered tools and sections is HTTP-only (`GET /api/plugins/settings/sections`; no `/api/mcp/tools` GET endpoint today — query the database directly via `argus daemon` introspection if you need it during development).

- **No JSON layouts.** The plan called for user-supplied JSON layouts under `~/.argus/layouts/` to recompose argus's built-in widgets. That story was superseded by plugin views (PR 9), which let a plugin register its own top-level page and render it via WebSocket. The `~/.argus/layouts/` directory is no longer scanned at boot.

If a divergence above bothers you for a specific plugin, file an argus issue — the contract is additive within v1, so a new opt-in field is usually the right shape.

## Where to look next

- `internal/api/auth.go` — middleware, header tagging, token minting.
- `internal/api/events_stream.go` — SSE handler, cursor fence, resync logic.
- `internal/api/task_meta.go` — metadata read/write endpoints.
- `internal/api/mcp_tools.go` — registration / unregistration handlers.
- `internal/api/plugin_settings.go` — section registration and save proxy.
- `internal/mcp/registry.go` — the persistent registry the MCP server consults alongside built-ins, plus the proxy logic.
- `internal/tui/settings/form.go` — settings section parser and validator.
- `internal/model/event.go` — canonical event type strings (renames here are breaking changes).

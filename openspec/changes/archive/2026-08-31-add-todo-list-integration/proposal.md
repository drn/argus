# Add configurable to-do-list backend integration

## Why

Argus has no way to touch a personal to-do list. The user already runs Things 3 for personal task tracking and wants Argus (both the user directly and agents running inside Argus) able to create, update, resolve, and delete items in it, without Argus adopting its own competing to-do concept (that role stays with the `Task` model — agent work units — which is deliberately not this). A third-party MCP server was considered and rejected: Things 3 has no hosted MCP offering (it's a local macOS app automated via AppleScript), and folding the integration into Argus's own already-running local MCP server (`internal/mcp`, bound to `127.0.0.1`/Tailscale only) avoids standing up a second local process and a second credential surface for something this small.

The user also wants this to generalize: Things 3 first, other backends (e.g. a hosted service) later, but never more than one active at once.

## What Changes

- New `internal/todo` package: a small `Backend` interface (`Create`, `List`, `Update`, `Complete`, `Delete`) that every to-do backend implements, plus `internal/todo/things3`, the first implementation, driving Things 3 via `osascript`/AppleScript (macOS-only).
- New `[todo]` config section (`internal/config`): `backend` (empty = disabled, else a registered backend name) and one backend-specific sub-table (`[todo.things3]`: optional destination list name, optional tag). Only one backend is ever active — setting `backend` swaps the whole thing, never adds a second.
- New Argus Settings section (TUI) to pick the backend and fill its fields, persisted through the existing generic config-value mechanism (`SetConfigValue`, same path `kb.enabled`/`api.enabled` already use) — no new DB table.
- `internal/mcp/server.go` gains a `SetTodoManager`-style wiring point and five new tools — `todo_create`, `todo_list`, `todo_update`, `todo_complete`, `todo_delete` — following the existing `task_*`/`schedule_*` conditional-exposure pattern: present in `tools/list` only while a backend is configured, and gone the instant it isn't.
- Unlike the existing `Set*Manager` calls (which are wired once before `ListenAndServe` and never change), the todo wiring must be swappable at runtime: saving a backend selection in Settings must make `todo_*` tools appear on the very next `tools/list` call, with no daemon restart. This is a new capability for the MCP server, not a rename of the existing pattern.
- README Reference appendix: add the five `todo_*` tools to the MCP tools table, and the new `[todo]` block to the config reference.

## Non-Goals (this change)

- **No promotion of a to-do item into a real Argus `Task`.** Explicitly descoped by the user in favor of plain CRUD; may return as a later change.
- **No local persistence/mirroring of to-do items.** Argus is a thin, live pass-through to whichever backend is configured — it is not a second source of truth. `todo_list` always queries the backend directly.
- **No web SPA / macOS Settings UI for this in the same PR.** The user asked for "Argus settings," and this change ships TUI Settings only. Per this repo's Frontend Parity rule, that gap is named here explicitly rather than silently dropped: web/macOS todo-backend configuration is a tracked follow-up, not an oversight. (The `todo_*` MCP tools themselves are reachable from any MCP client regardless of which UI configured them, so this gap is Settings-UI-only, not functional.)
- **No second backend implementation.** The `Backend` interface exists because the user explicitly asked for pluggability, but Things 3 is the only adapter built now.

## Impact

- Affected specs: new capability `todo-list-integration` (this change creates it from scratch — no existing spec covers to-do backends).
- Affected code (implementation phase, not this proposal):
  - `internal/todo/` (new: `Backend` interface, `Item` type, registry of known backend names)
  - `internal/todo/things3/` (new: AppleScript-driven adapter)
  - `internal/config/config.go` (new `TodoConfig`/`Things3Config`)
  - `internal/mcp/server.go` (new tool defs, `SetTodoManager`, mutex-guarded runtime swap, `todoMgmtEnabled()`)
  - `internal/daemon/` (wiring at startup + re-wiring on Settings change)
  - `internal/tui/settings.go` (new Todo settings section)
  - `README.md` (Reference appendix: MCP tools table, config reference)
  - `context/knowledge/gotchas/misc.md` or a new `todo.md` gotcha file (macOS-only constraint, AppleScript failure modes, live-pass-through-not-cache design note)

## Why

Sandboxing today is only a global (`config.Sandbox.Enabled`) and per-project
(`config.Project.Sandbox.Enabled *bool`) setting, resolved once at task-creation
time by `agent.ResolveSandboxConfig`/`IsTaskSandboxed` and baked into
`task.Sandboxed`. There is no way to force a single task on or off without
editing the project's config — e.g. a one-off task that needs unrestricted
filesystem access (installing a system dependency, debugging an SBPL denial)
on a project that sandboxes by default, or the reverse: sandboxing one
higher-risk task on a project that normally runs unsandboxed. Today the only
lever is `internal/tui/projectform.go`'s Inherit/Enabled/Disabled cycler, which
changes the setting for every future task on that project, not just one.

## What Changes

- **`model.Task` gains a `SandboxOverride string` field** (`json:"sandbox_override,omitempty"`),
  a tri-state per-task override: `""` (inherit — the resolved
  global/project setting, unchanged behavior), `"enabled"` (force sandboxed
  regardless of global/project config), `"disabled"` (force unsandboxed).
  Persisted in a new `tasks.sandbox_override` column (idempotent
  `ALTER TABLE ... ADD COLUMN sandbox_override TEXT NOT NULL DEFAULT ''`).
  Set once at creation time, like `Archetype`/`Profile`/`BaseBranch` — not
  editable after the task exists.

- **`agent.ResolveSandboxConfig` gains a third precedence tier.** Today it
  merges global → project (`project.Sandbox.Enabled` wins when set). This
  change adds task → the resolution order becomes
  **task override (if set) > project override (if set) > global**, mirroring
  the existing `ResolveBackend` precedence pattern. `IsTaskSandboxed` and the
  `BuildCmd` wrap decision are unchanged — they already consult
  `ResolveSandboxConfig`, so a task-level override flows through for free once
  `ResolveSandboxConfig` knows about it. `task.Sandboxed` continues to be
  computed and persisted once at creation time (`agent.CreateAndStart`,
  `create.go:209`) — the override does not change that invariant, it just
  changes what gets baked in.

- **The TUI new-task form gains a Sandbox cycling selector** (Inherit /
  Enabled / Disabled, default Inherit), alongside the existing
  Backend/Model/Profile/Archetype selectors, following the exact
  Inherit/Enabled/Disabled option set and nil-sentinel-style persistence
  `internal/tui/projectform.go` already uses for the per-project override.
  Leaving it on Inherit submits no override (today's behavior, driven by
  whatever Settings/project config resolves to); cycling to Enabled or
  Disabled submits that override.

- **The web new-task form gains a matching Sandbox `<select>`** (Inherit /
  Enabled / Disabled), submitted as `sandbox_override` in the create-task
  POST body — the same three values, same default. SPA shell change ⇒ bump
  `SW_VERSION`.

- **`POST /api/tasks` (JSON body) accepts an optional `sandbox_override`
  field**, validated to be one of `""`, `"enabled"`, `"disabled"` (reject
  anything else with 400, mirroring the existing backend-name validation).
  Threaded through `agent.CreateInput.SandboxOverride` → `HeadlessInput` (used
  by the REST path) into the task row.

## Non-Goals (named follow-ups, not silence)

- **macOS app.** `macos/Sources/Argus/NewTaskSheet.swift` has no Model,
  Archetype, Profile, or BaseBranch override either — this is a pre-existing
  parity gap on those fields, not one this change introduces. Adding a
  Sandbox override to the macOS new-task sheet is an explicit, named
  follow-up, tracked here rather than silently skipped, and should land
  alongside (or after) closing that pre-existing gap for the other overrides.
- **MCP `task_create`.** Not requested by this change (scoped to "TUI and
  webapp"); `mcp.TaskCreateInput` is a struct so adding `SandboxOverride`
  later is a pure additive field with no signature churn, unlike the REST
  path's positional `api.TaskCreator`.
- **Scheduled tasks.** `model.ScheduledTask` has no sandbox override; a fired
  schedule always inherits (empty override), matching today's behavior. Not
  in scope here.
- **Editing the override after creation.** Like `Sandboxed` itself, the
  override is resolved and baked in once at creation time; the task detail
  view does not gain an edit control for it.

## Impact

- Affected specs: `sandbox-execution` (new per-task override resolution
  requirement), `forms-and-modals` (new TUI selector requirement),
  `mobile-pwa` (new web selector requirement), `data-persistence` (new column
  round-trip requirement), `rest-api` (new scenario on the existing task
  creation requirement).
- Affected code: `internal/model/task.go`, `internal/db/schema.go`,
  `internal/db/tasks.go`, `internal/agent/agent.go`, `internal/agent/create.go`,
  `internal/daemon/headless.go`, `internal/daemon/daemon.go`,
  `cmd/argus-test-server/main.go`, `internal/api/server.go`,
  `internal/api/handlers.go`, `internal/tui/newtaskform.go`,
  `internal/tui/app.go`, `internal/api/static/{index.html,sw.js}`.
- Backwards compatible: empty `SandboxOverride` preserves today's resolution
  exactly; the new column defaults to `''`; the new REST field is optional and
  `omitempty`.
- No new keybinding — the TUI field is reached via the form's existing
  Tab/Backtab and Up/Down nav, so the help modal is unchanged.

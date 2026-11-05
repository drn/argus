## Why

The per-task `SandboxOverride` (`""`/`"enabled"`/`"disabled"`) is already
supported by `agent.CreateInput`, `daemon.HeadlessInput`, and the REST
`POST /api/tasks` surface, but the MCP `task_create` tool cannot set it. An
agent-driven flow such as the `/handoff` skill therefore always creates its
follow-up task with the project/global sandbox default, even when the handoff
should run unrestricted (or be forced sandboxed).

## What Changes

- `task_create` gains an optional `sandbox_override` string property
  (`"enabled"` | `"disabled"`; empty/omitted = inherit). Invalid values error.
- `mcp.TaskCreateInput` gains `SandboxOverride`; the daemon's `TaskCreator`
  wrapper forwards it to `HeadlessInput.SandboxOverride`.
- **Escalation guard:** `"disabled"` is rejected when the calling task
  (resolved from `caller_id` / `cwd`, as for account inheritance) is itself
  sandboxed, so a sandboxed agent cannot spawn an unsandboxed one over MCP.
  `"enabled"` is always allowed. When the caller cannot be identified,
  `"disabled"` is rejected (fail closed).
- The `/handoff` skill (`~/.claude/skills/handoff/SKILL.md`, outside this repo)
  accepts a standalone `sandbox=enabled|disabled` token and passes it through.
  Updated as a follow-up step, not part of the Go PR.

## Non-Goals

- No TUI/web/macOS change: those surfaces already expose the override
  (Frontend Parity unaffected; the REST contract does not change).
- The handoff-skill delta is applied outside the repo (`~/.claude/skills/handoff/SKILL.md`) and is not merged into a base spec.
- `hera_spawn_worker` / `schedule_create` are not extended here.

## Impact

`internal/mcp/server.go`, `internal/daemon/daemon.go`, tests, README MCP tools
table, `gotchas/sandbox.md`.
- The disabled-caller guard is not a security boundary: MCP callers self-report `caller_id`/`cwd`, so a deliberate caller can name another task. Closing that needs a task-scoped MCP transport (named follow-up).

## Why

A schedule can already pin a **backend** (`sched.Backend`), but not a **model**.
At fire time the scheduler calls `TaskCreator(name, prompt, project, backend)` and
the created task carries no `Model`, so `agent.ResolveModel` always falls back to
the backend's configured default (`internal/scheduler/scheduler.go:27`). The only
way to make a schedule run on, say, sonnet today is to define a whole second
backend pinned to that model and repoint the schedule at it — heavy, indirect, and
it leaks onto everything else that uses that backend.

The immediate driver: the "Gryffin Jersey Watch" daily schedule should run on
**sonnet** (a cheap daily watcher does not need opus), without standing up a
parallel backend.

Tasks created from the new-task form already support a per-task `Model` override
(commit c8f9863), and `HeadlessInput.Model` already threads it through
`agent.CreateAndStart`. Schedules are the one creation path that drops it.

## What Changes

- **`model.ScheduledTask` gains a `Model string` field** (`json:"model,omitempty"`)
  — an optional per-schedule model override. Empty = the backend's configured
  default (unchanged behavior). Persisted in a new `scheduled_tasks.model` column
  (idempotent `ALTER TABLE … ADD COLUMN model TEXT NOT NULL DEFAULT ''`, mirroring
  the existing `tasks.model` migration).

- **The fire path carries the model to the created task.** `scheduler.TaskCreator`
  grows a `taskModel string` parameter; `fire()` passes `sched.Model`; the daemon's
  creator closure forwards it via `HeadlessInput.Model`. A fired task with a
  non-empty schedule model launches its agent with `--model <value>`; an empty one
  is byte-identical to today.

- **REST + MCP + remote surfaces round-trip `model`.** `scheduleJSON` /
  `scheduleRequest` (and their `apiclient` mirrors) gain `Model`;
  `applyScheduleRequest` copies it (trimmed) as a partial-updatable field;
  `schedule_create` / `schedule_update` MCP tools accept a `model` arg; the
  `apistore` adapter sends/reads it. The `/raw` round-trip is automatic (it
  marshals the full model struct).

- **Both schedule editors expose the field**, mirroring the new-task form's
  per-backend model selector (default / known-models / custom…): the TUI
  `ScheduleForm` adds a Model selector row, and the SPA schedule modal adds a
  `model` field rebuilt from the selected backend. SPA shell change ⇒ bump
  `SW_VERSION`.

- **The Gryffin Jersey Watch schedule is then set to `model: "sonnet"`** (an
  operational `PUT`, done after the code lands — not part of the spec).

## Impact

- Affected specs: `scheduling` (the fire requirement now includes the model
  override; the lifecycle requirement notes `model` as a partial-updatable field).
- Affected code: `internal/model/schedule.go`, `internal/db/{schema,schedules}.go`,
  `internal/api/schedules.go`, `internal/apiclient/schedules.go`,
  `internal/apistore/{convert,store}.go`, `internal/scheduler/scheduler.go`,
  `internal/daemon/daemon.go`, `internal/mcp/server.go`,
  `internal/tui/scheduleform.go`, `internal/api/static/{index.html,sw.js}`.
- Backwards compatible: empty `Model` preserves today's behavior exactly; the new
  column defaults to `''`; all new wire fields are `omitempty` / optional pointers.
- No new keybinding (the form field is reached via existing nav keys), so the help
  modal is unchanged.

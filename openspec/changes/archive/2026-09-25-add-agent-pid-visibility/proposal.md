## Why

Every argus task's agent process (Claude Code or Codex, spawned via `sh -c` in
`internal/agent/agent.go`) is already tracked end-to-end — `model.Task.AgentPID`
is set at spawn/restart/resume and persisted in the DB (`agent_pid` column) —
but it is never surfaced anywhere in the TUI. A user staring at Activity
Monitor (or `ps`) sees an opaque process name — Claude Code's own
background-session supervisor execs the real per-session worker directly from
its version-pinned install path (e.g. `.../versions/2.1.281`), so the OS
process name is a version number, not "claude"; Codex's background worker
shows as the generic `codex-code-mode-host` shared by every Codex task. In
both cases the PID is the only reliable local handle back to a specific argus
task, and there is currently no way to go from "I see PID 17557 in Activity
Monitor" to "that's the `remove-v4-feedback-endpoints` task" without shelling
out to `lsof`/`pgrep` by hand.

## What Changes

- **The task detail panel gains a PID row.** When the selected task has a
  nonzero `AgentPID`, the panel shows `PID: <n>` alongside the existing
  Backend/Sandbox/Worktree rows. A `0` (never started) or a task whose last
  known session has since exited both behave like the panel's other
  optional fields — the value shown is the last one recorded, not a
  liveness guarantee; the existing "(running)"/"(idle)" status annotation
  is still the source of truth for whether the process is actually alive.
- **The inline `/` filter also matches PID.** A filter term matches a task if
  it's a case-insensitive substring of the task's name OR project (existing
  behavior) OR a substring of its nonzero `AgentPID`'s decimal string. Tasks
  with no recorded PID (`AgentPID == 0`) are not PID-matchable — a filter
  term of `0` should not spuriously match every never-started task.

## Non-Goals

- **The compact task-list row itself does not gain a PID cell.** It already
  reserves cells for status/PR/hera glyphs against a cramped name column;
  the filter is how a PID becomes actionable (it narrows straight to the
  owning task without needing the row to print the number). Revisit only if
  requested.
- **Web SPA and macOS app parity.** Per `AGENTS.md`'s Frontend Parity rule,
  this is a real REST-exposed surface change and normally requires
  evaluating all three clients in the same PR — but it doesn't, in a
  narrower sense: the raw API (`GET /api/tasks-raw`, `.../{id}/raw`) already
  carries `AgentPID`, and the remote (`--remote`) TUI already receives it
  today via `apistore`'s `ListTasksRaw`. The gap is real for two OTHER
  surfaces: the SPA consumes the separate lossy `taskJSON` shape
  (`internal/api/handlers.go`), which has no `agent_pid` field, and the
  macOS app's `ArgusKit` `Task` model has no PID field either. Both would
  need their own field-plus-render follow-up to show PID in their detail
  views. Tracked here as a named follow-up rather than silently dropped;
  not part of this change's implementation.

## Capabilities

### Modified Capabilities

- `task-list-view`: the task detail panel now displays the selected task's
  agent PID when known, and the inline substring filter now also matches
  against it.

## Impact

- **Modified code:**
  - `internal/tui/taskview/taskdetail.go` — new PID row in `Draw()`;
    `taskShape()` hashes `AgentPID` so a PID change (e.g. task restarted
    under a new PID) triggers `OnBranchChange`.
  - `internal/tui/taskview/tasklist.go` — `matchesFilter` gains PID
    substring matching.
- **No schema change** (the column already exists), **no new key**, **no
  daemon RPC change**, **no REST wire-format change** (raw endpoints already
  serve `AgentPID`).
- Specs are LOCAL DOCS only (`openspec/project.md`): no CI/Make/Go-build
  wiring added. The quality gate stays `make pre-pr`.

# Proposal: optional task-name input in the new-task / new-coordinator / spawn-worker popup

## Why

The shared new-task modal always auto-derives the task name from the prompt; users have no way to name a task (or, for `n`, an orchestrator) up front. An optional name field lets the user choose the name when they care, while preserving today's auto-naming when they don't.

## What Changes

- Add an OPTIONAL single-line name input to the shared `NewTaskForm`, placed after the prompt, placeholder `(optional)`, in Tab/Backtab focus order, with paste support. Whitespace-only is treated as blank.
- New-task submit: when the name is non-empty, create the task with that name (trimmed, sanitized) and suppress the background LLM auto-rename (`AutoName=false`); when blank, behavior is unchanged (prompt-derived slug + today's auto-naming).
- Hera `w` (spawn worker): a non-empty name names the worker task/role (overriding the prompt-derived name); blank derives as today.
- Hera `n` (new coordinator): a non-empty name names BOTH the new orchestrator (the rail label) and the coordinator task (overriding the prompt-derived, de-collided orchestrator name); blank derives as today. The coordinator role stays `coord`.
- The entered name is sanitized through the existing safe-name path; task-name collisions keep using the existing auto-suffix; orchestrator-name collisions surface via the existing (now auto-clearing) status-bar error.

No breaking changes. All behavior is identical to today when the field is left blank.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- **forms-and-modals** — the new-task form gains an optional name field and name-resolution + auto-name-suppression on submit.
- **hera-view** — the shared `w`/`n` modal's name field drives worker / orchestrator + coordinator-task naming.
- **auto-naming** — clarify that a user-supplied name at creation suppresses the background LLM rename.

## Impact

- Code: `internal/tui/newtaskform.go` (new field + render + focus + paste + submit), `internal/tui/heraactions.go` (`heraSpawnWorker` / `heraNewCoordinator` consume the entered name), `internal/agent/hera_spawn.go` (`SpawnHeraWorker` / `SpawnHeraCoordinator` accept an explicit name override), `internal/agent/create.go` (set `AutoName=false` when a name is supplied — likely already wired via `CreateInput.Name`).
- Help modal / README: no key added or rebound, so no help-modal change required.
- Tests: `internal/tui/newtaskform_test.go` (+ hera action tests) for field presence/placeholder/focus/paste and the naming behaviors.

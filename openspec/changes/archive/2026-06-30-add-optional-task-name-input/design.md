# Design: optional task-name input in the new-task / new-coordinator / spawn-worker popup

## Context

The new-task popup (`NewTaskForm`) is the single shared modal behind three TUI entry points:

- the Tasks-tab new-task popup,
- the Hera-rail `w` (spawn worker) popup,
- the Hera-rail `n` (new coordinator) popup.

Today the modal collects project / branch / backend / model / prompt, and the task name is always auto-derived from the prompt (`model.GenerateNameFromPrompt`), with an optional background LLM rename (`CreateInput.AutoName`). There is no way for the user to name the task up front. This change adds an optional name input after the prompt.

## Goals

- Add one optional single-line name input to the shared modal, placed after the prompt, placeholder `(optional)`.
- When filled, use the entered name as the task name (verbatim, after trim) and suppress the background LLM auto-rename.
- For `n` (new coordinator), the entered name also names the orchestrator (the rail label), not just the task.
- For `w` (spawn worker), the entered name names the worker task/role.
- When blank, behavior is exactly as today (prompt-derived name; orchestrator/worker names derive from the prompt).

## Non-Goals

- No change to backend/model/project/branch fields or their resolution.
- No new validation UI beyond reusing existing error surfacing.
- No change to the coordinator role name (stays `coord`).
- No bulk rename / post-hoc rename changes (the `r` rename key is untouched).

## Decisions

### The field

- New field `ntFieldName = 5`; `ntFieldCount → 6`. Inserted in the Tab/Backtab focus order immediately after `ntFieldPrompt`.
- Single-line `[]rune` buffer + cursor, mirroring the existing project / branch / custom-model inputs (same render, same caret, same `PasteHandler` switch case). Placeholder `(optional)`.
- Whitespace-only input is treated as blank (trim, then empty ⇒ "not provided").

### Threading the value out of the form

- The form's submit callback already carries an unused second `string` parameter (`func(task *model.Task, _ string)`). That parameter now carries the trimmed entered name (`""` when blank). This avoids widening the callback signature and keeps the form's `*model.Task` construction as the single source for the prompt-derived defaults.
- For the plain new-task path, the form sets `Task.Name` directly: entered name when non-empty, else `GenerateNameFromPrompt(prompt)` (unchanged).

### Per entry point

- **New task:** non-empty ⇒ `Task.Name` = entered; the create path sets `CreateInput.AutoName = false`. Blank ⇒ today's behavior (prompt slug + AutoName as today).
- **Spawn worker (`w`):** non-empty ⇒ worker task/role name = entered (overrides `agent.DeriveHeraWorkerName(prompt)` inside `SpawnHeraWorker`). Blank ⇒ derive as today.
- **New coordinator (`n`):** non-empty ⇒ the new orchestrator name AND the coordinator task name = entered (overrides the prompt-derived, de-collided orchestrator name inside `SpawnHeraCoordinator`). Blank ⇒ orchestrator name derives from the prompt as today. Coordinator role stays `coord`.

### Sanitization & conflicts

- The entered name is sanitized through the existing safe-name path before it is used for a task/worktree/branch/orchestrator/role name (the same sanitization the auto-derived names already pass through). If sanitization yields empty, treat as blank.
- Task-name collisions keep relying on `agent.CreateAndStart`'s existing auto-suffix behavior — no new logic.
- An orchestrator-name collision (for `n`) surfaces through the existing spawn error path (status-bar error, form stays open). This now auto-clears thanks to the status-bar TTL change (fix-bug-030), so a transient collision message no longer sticks.

## Alternatives considered

1. **Widen the submit callback to `func(task, name string)` everywhere (chosen-adjacent).** Rejected in favor of reusing the existing unused second `string` param — same effect, smaller blast radius, no signature churn across call sites.
2. **Separate name field only in new-task, conditionally hidden for `w`/`n`.** Rejected: the user wants it in all three, and a shared field avoids per-context form state.
3. **Name sets the task name only (not the orchestrator) for `n`.** Rejected by the user: the rail label is the orchestrator name, so "what you type is what you see" requires naming the orchestrator too.

## Discovery findings

- `internal/tui/newtaskform.go`: fields `ntFieldProject..ntFieldPrompt` (count 5); single-line inputs (project/branch/custom-model) and the multi-line prompt; `PasteHandler()` switches on `f.focused`; submit builds `&model.Task{Name: GenerateNameFromPrompt(prompt), ...}`.
- `internal/agent/create.go`: `CreateInput{Name, AutoName, ...}` — `Name` already bypasses auto-generation; `AutoName` gates the background LLM rename.
- `internal/agent/hera_spawn.go`: `SpawnHeraWorker` / `SpawnHeraCoordinator`; worker/orchestrator names derive from the prompt via `DeriveHeraWorkerName`.
- `internal/tui/heraactions.go`: `heraSpawnWorker` / `heraNewCoordinator` open the shared modal via `openHeraNewTaskForm` and consume `(task, _ string)`.
- Specs: `forms-and-modals` "New-task form submission"; `hera-view` "`w` and `n` use the full new-task modal (area 4)"; `auto-naming` "Replace the auto-generated slug with an LLM-suggested name".

## Acceptance criteria

### Field (forms-and-modals)

- it should render an optional single-line name input after the prompt with placeholder `(optional)`.
- it should place the name field in Tab/Backtab focus order immediately after the prompt.
- it should accept pasted text into the name field via the form's paste handler.
- it should treat a whitespace-only name as blank.

### New-task submission (forms-and-modals + auto-naming)

- it should create the task with the entered name verbatim (after trim) when the name field is non-empty.
- it should suppress background auto-naming (no LLM rename) when the user supplied a name.
- it should fall back to the prompt-derived auto-generated name when the name field is blank.

### Hera `w` / `n` (hera-view)

- it should name the spawned worker task/role with the entered name when non-empty (`w`).
- it should name the new orchestrator AND the coordinator task with the entered name when non-empty (`n`).
- it should derive the worker/orchestrator name from the prompt when the name field is blank.

## Risks / Trade-offs

- Low blast radius: one new optional field; all defaults preserved when blank.
- Shared modal means the field shows in all three popups by design (intended).
- Sanitization reuse avoids a new charset-validation surface.

## Open questions

- None blocking. (Freelance-adopt naming is out of scope — that path has no creation popup.)

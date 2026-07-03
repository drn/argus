# hera-view (delta)

## MODIFIED Requirements

### Requirement: `w` and `n` use the full new-task modal (area 4)

The system SHALL open the SAME modal as the new-argus-task popup (project / branch / backend / model / prompt / optional name, with project and skill autocomplete) for both the rail `w` (spawn worker) and `n` (new coordinator) keys. The project field SHALL default to the selected coordinator's project for `w`, and to the current selection's coordinator project (else the last-selected Tasks-tab project) for `n`. The modal SHALL return to the Hera tab on submit or cancel (not the Tasks tab). On submit:

- `w` spawns a born-bound worker under the selected coordinator's orchestrator via the shared `agent.SpawnHeraWorker` primitive, carrying the form's project, branch, backend, model, and prompt. When the optional name is non-blank, it SHALL name the worker task/role (overriding the prompt-derived name); when blank, the worker name derives from the prompt as before.
- `n` creates a NEW top-level orchestrator + coordinator role bound to a freshly created argus task via the shared `agent.SpawnHeraCoordinator` primitive. When the optional name is non-blank, it SHALL name BOTH the new orchestrator and the coordinator task (overriding the prompt-derived, de-collided orchestrator name); when blank, the orchestrator name is derived from the prompt and de-collided as before. The coordinator role is named `coord` in both cases.

The worker/coordinator spawn runs off the tview main thread (it creates a worktree + session) and refreshes the rail on completion. A spawn with no live coordinator (for `w`) surfaces visible feedback and does nothing.

Derived from: `internal/tui/heraactions.go` (`heraSpawnWorker`, `heraNewCoordinator`), `internal/agent/hera_spawn.go` (`SpawnHeraWorker`, `SpawnHeraCoordinator`), `internal/tui/newtaskform.go`.

#### Scenario: `w` spawns a worker from the full modal

- **WHEN** a coordinator is selected and the user presses `w`, fills the modal, and submits
- **THEN** a born-bound worker is spawned under that coordinator's orchestrator with the form's project/branch/backend/model/prompt, and the rail refreshes

#### Scenario: `n` creates a new root coordinator

- **WHEN** the user presses `n`, fills the modal, and submits
- **THEN** a new top-level orchestrator + `coord` coordinator role is created, bound to a freshly created argus task — not nested under the current selection

#### Scenario: `w` with no live coordinator is feedback-only

- **WHEN** the user presses `w` on a selection whose orchestrator has no live coordinator
- **THEN** the status bar shows a "no live coordinator" error and no worker is spawned

#### Scenario: `w` with an explicit name names the worker

- **WHEN** the user presses `w`, enters a non-blank name in the modal, fills the rest, and submits
- **THEN** the spawned worker's task/role name is the entered name rather than a prompt-derived name

#### Scenario: `n` with an explicit name names the orchestrator and coordinator task

- **WHEN** the user presses `n`, enters a non-blank name in the modal, fills the rest, and submits
- **THEN** the new orchestrator's name (the rail label) AND the coordinator task's name are the entered name rather than a prompt-derived name, and the coordinator role is still named `coord`

#### Scenario: blank name derives from the prompt as before

- **WHEN** the user presses `w` or `n`, leaves the name field blank, fills the rest, and submits
- **THEN** the worker / orchestrator name is derived from the prompt exactly as it was before the optional name field existed

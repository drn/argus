# hera-view delta: rail key family (new-task modal, new coordinator, EOL keys)

## MODIFIED Requirements

### Requirement: Rail keybindings (area 4)

The system SHALL bind the following keys while the rail holds focus: `j`/`k` and Down/Up move the cursor; Space collapses/expands the row under the cursor; `Tab`/`Backtab` and `Ctrl+Alt+Left`/`Ctrl+Alt+Right` walk the focus ladder; `Ctrl+Q` returns focus to the rail; `Enter` enters the selected role's pane (restarting a dead/suspended session first); `w` spawns a worker under the selected coordinator's orchestrator via the full new-task modal; `n` creates a new top-level coordinator via the full new-task modal; `r` renames the selected role/orchestrator; `a` toggles archive; `P` toggles pin; `s`/`S` advance/revert the selected role's hera status; `J` adopts a freelancer / re-parents a coordinator; `R` retires the selected worker; `C` prunes the selected coordinator's archived descendants; `Ctrl+R` prunes all finished coordinators + agents rail-wide; `Ctrl+Z` fullscreens the focused pane; `/` filters the rail by name; `Ctrl+D` deletes the selected role/orchestrator. Every bound key SHALL appear in the help overlay's "Hera View (rail)" section.

All rail mutation keys SHALL be suppressed while the rail is in `/` filter INPUT mode (every keystroke is filter input). Selection-acting keys (`w`, `r`, `a`, `P`, `s`/`S`, `J`, `R`, `C`, `Ctrl+D`) are no-ops on an empty selection; `n` and `Ctrl+R` are selection-INDEPENDENT and fire even on an empty rail.

Derived from: `internal/tui/hera/rail.go` (rail `InputHandler`), `internal/tui/hera/page.go` (page `InputHandler` focus ladder + `handleRailMutation`), `internal/tui/modal/help.go` (help overlay Hera section).

`NOTE:` `Ctrl+R` is scoped to the rail's own handler — it never collides with the agent-view `Ctrl+R` (Claude session switcher), which runs in a different mode/widget. Plain Left/Right are unused by the rail (free for future horizontal nav). `Cmd+↑/↓` rail-selection-while-pane-focused collides at the byte level with agent-view task navigation and remains an unresolved rebinding decision.

#### Scenario: Mutation key acts on the current selection

- **WHEN** the rail is focused and the user presses `a` on a selected role
- **THEN** the archive-toggle callback fires for that role's `(role, orchestrator)` selection and the key does not leak to navigation

#### Scenario: New-coordinator key fires on an empty rail

- **WHEN** the user presses `n` while the rail is focused and nothing is selected
- **THEN** the new-coordinator callback fires (it is the bootstrap affordance) and does not route through the selection-gated path

#### Scenario: Mutation keys suppressed while filtering

- **WHEN** the rail is in `/` filter INPUT mode and the user types `n`, `R`, `C`, or `w`
- **THEN** the keystroke is appended to the filter query, not interpreted as a command

#### Scenario: Help overlay lists every rail key

- **WHEN** the help overlay is opened
- **THEN** its "Hera View (rail)" section lists `j`/`k`, space, Tab/Ctrl+Q, Enter, `w`, `n`, `r`, `a`, `P`, `s`/`S`, `J`, `R`, `C`, `Ctrl+R`, `Ctrl+Z`, `/`, and `Ctrl+D`

## ADDED Requirements

### Requirement: `w` and `n` use the full new-task modal (area 4)

The system SHALL open the SAME modal as the new-argus-task popup (project / branch / backend / model / prompt, with project and skill autocomplete) for both the rail `w` (spawn worker) and `n` (new coordinator) keys. The project field SHALL default to the selected coordinator's project for `w`, and to the current selection's coordinator project (else the last-selected Tasks-tab project) for `n`. The modal SHALL return to the Hera tab on submit or cancel (not the Tasks tab). On submit:

- `w` spawns a born-bound worker under the selected coordinator's orchestrator via the shared `agent.SpawnHeraWorker` primitive, carrying the form's project, branch, backend, model, and prompt.
- `n` creates a NEW top-level orchestrator + coordinator role bound to a freshly created argus task via the shared `agent.SpawnHeraCoordinator` primitive (the orchestrator name is derived from the prompt and de-collided; the coordinator role is named `coord`).

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

### Requirement: `R` retires a worker (area 7)

The system SHALL bind `R` on the focused rail to a confirm-gated retire of the selected WORKER role. Retire steps the hera role status to `done` (rolling a worker's task to `in_review` + `ready_to_close`), STOPS the session, ARCHIVES the underlying argus task (the worktree is KEPT — retire is reversible), ends this role's live binding, and archives the role row. For a task bound under MULTIPLE orchestrators the worktree and task are PRESERVED and only THIS role's binding ends + role archives (multi-binding isolation). On a coordinator or orchestrator-header selection `R` surfaces "Retire applies to workers" and is a no-op.

Derived from: `internal/tui/heraactions.go` (`heraRetireWorker`, `heraDoRetire`), `internal/tui/hera/ops.go` (`Ops.RetireRole`).

#### Scenario: Retire a sole-bound worker

- **WHEN** the user presses `R` on a worker whose task is bound only to this role and confirms
- **THEN** the session stops, the argus task is archived (worktree kept), the role status is `done`, this role's binding ends, and the role row is archived

#### Scenario: Retire a multi-bound worker preserves the task

- **WHEN** the user presses `R` on a worker whose task is also bound under another orchestrator and confirms
- **THEN** only this role's binding ends and the role row is archived; the argus task and its worktree are preserved

#### Scenario: Retire on a coordinator is a no-op

- **WHEN** the user presses `R` on a coordinator/header selection
- **THEN** the status bar shows "Retire applies to workers" and nothing is changed

### Requirement: `C` prunes a coordinator's archived descendants (area 7)

The system SHALL bind `C` on the focused rail to a confirm-gated prune scoped to the selected coordinator's subtree (`Model.BridgeSubtree`). Prune completes the tasks and reclaims the worktrees+branches of all ARCHIVED descendant worker roles in the subtree, then removes those role rows. A task still bound live under another orchestrator is PRESERVED (its worktree is kept; the role row is still removed). The confirm modal SHALL show a count summary (roles pruned / worktrees reclaimed / preserved); when the subtree has no archived descendant workers the status bar SHALL show "nothing to prune" and no confirm opens.

Derived from: `internal/tui/heraactions.go` (`heraPruneDescendants`, `heraDoPruneDescendants`, `heraReclaimRole`), `internal/tui/hera/eol.go` (`Model.SubtreeArchivedWorkers`).

#### Scenario: Prune archived descendants

- **WHEN** the user presses `C` on a coordinator with archived descendant workers and confirms
- **THEN** each archived descendant worker's task is completed, its worktree+branch reclaimed (unless bound live elsewhere), and its role row removed

#### Scenario: Nothing to prune

- **WHEN** the user presses `C` on a coordinator with no archived descendant workers
- **THEN** the status bar shows "nothing to prune" and no confirm modal opens

### Requirement: `Ctrl+R` prunes all finished coordinators and agents (area 7)

The system SHALL bind `Ctrl+R` on the focused rail (its OWN handler, never colliding with the agent-view session switcher) to a confirm-gated rail-wide prune. It reclaims every FINISHED role (a role that is archived, or whose hera status is `done`, or that is flagged `ready_to_close`) — completing its task, reclaiming its worktree+branch (unless bound live elsewhere), and removing its role row — and deletes orchestrators whose every role is finished. The confirm modal SHALL show a count summary; when no finished roles exist the status bar SHALL show "nothing to prune" and no confirm opens.

Derived from: `internal/tui/heraactions.go` (`heraPruneDone`, `heraDoPruneDone`), `internal/tui/hera/eol.go` (`RoleView.IsFinished`, `Model.FinishedRoles`, `Model.FullyFinishedOrchestratorIDs`).

#### Scenario: Sweep finished roles rail-wide

- **WHEN** the user presses `Ctrl+R` with finished roles present and confirms
- **THEN** each finished role's task is completed, its worktree+branch reclaimed (unless bound live elsewhere), and its role row removed; fully-finished orchestrators are deleted

#### Scenario: Ctrl+R does not collide with the agent view

- **WHEN** the user presses `Ctrl+R` while a content pane (not the rail) holds focus
- **THEN** the keystroke forwards to the pane's PTY and the rail-wide prune does NOT fire

#### Scenario: Nothing to prune

- **WHEN** the user presses `Ctrl+R` with no finished roles on the rail
- **THEN** the status bar shows "nothing to prune" and no confirm modal opens

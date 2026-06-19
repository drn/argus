> **PARTIALLY SUPERSEDED by `hera-eol-redesign` (BUG-022).** The end-of-life keys this change introduced — `R` (retire) and the rail-wide `Ctrl+R` (prune) — were REMOVED, and `C` was repurposed from "prune archived descendants" to "clear this coordinator's archive (nuke)". The two-state hide/nuke model in `hera-eol-redesign` is the current EOL surface; the `w`/`n` new-task-modal parts of this change still stand. See `hera-eol-redesign` for the live behavior.

## Why

The native Hera rail can spawn and adopt agents but has no first-class way to *create a new top-level coordinator from the TUI*, no rich form for spawning a worker (the `w` key opened a bare single-line prompt, unlike the full new-argus-task popup), and no end-of-life (EOL) affordances. An operator who wanted to retire a finished worker, reclaim the worktrees of a coordinator's archived agents, or sweep the whole rail of done coords had to drop to the Tasks tab or the MCP tools. This is the rail key-family gap tracked by the bug bash (BUG-005/006/010/011/012).

The EOL ladder native was missing is: **retire** (archive a finished worker, reversible, keeps the worktree) → **prune** (reclaim the worktrees of already-archived/finished work). Without it the rail accumulated archived rows whose worktrees were never reclaimed, and there was no single-keystroke "close out this coordinator" or "sweep the rail".

## What Changes

- **`w` uses the full new-task modal (BUG-005).** The rail `w` key now opens the SAME modal as the new-argus-task popup (`NewTaskForm`: project / branch / backend / model / prompt with autocomplete), with the project field DEFAULTING to the selected coordinator's project. On submit it spawns a born-bound worker under the current coordinator via the shared `agent.SpawnHeraWorker` primitive (now carrying the form's branch/backend/model too), replacing the bare single-field `openHeraInput` prompt.
- **`n` = new ROOT coordinator (BUG-006).** A new rail rune opens the SAME new-task modal (independent of the current selection); on submit it creates a NEW top-level orchestrator + coordinator role bound to a freshly created argus task — `hera_new_orchestrator` semantics — via a new shared `agent.SpawnHeraCoordinator` primitive. `n` works even on an empty rail (it is the bootstrap key), so it does NOT route through the selection-gated `fire` path.
- **`R` = retire worker (BUG-010).** One confirm-gated keystroke on a worker role: step the hera role status to `done` (which rolls a worker's task to `in_review` + `ready_to_close`), STOP the session, ARCHIVE the underlying argus task (worktree is KEPT — retire is reversible; prune reclaims later), end this role's binding, and archive the role row. For a multi-bound task the worktree and task are preserved and only THIS role's binding ends + role archives (same isolation as conservative delete). On a coordinator/header selection it surfaces "Retire applies to workers" and is a no-op.
- **`C` = prune the selected coordinator's archived descendants (BUG-011).** Confirm-gated, scoped to the selected coordinator's subtree (`Model.BridgeSubtree`): completes the tasks and reclaims the worktrees+branches of all that coordinator's ARCHIVED descendant worker roles, then removes those role rows. A task still bound live elsewhere is preserved (worktree kept). The confirm shows a count summary; an empty set surfaces "nothing to prune".
- **`Ctrl+R` = rail-wide prune of all done coords + agents (BUG-012).** Confirm-gated; rail-scoped (the rail's own `InputHandler`, so the agent-view `Ctrl+R` Claude-session-switcher is untouched). Reclaims every FINISHED role across the rail — completes its task, reclaims its worktree+branch, removes its role row — and deletes orchestrators whose every role is finished. The confirm shows a summary; an empty set surfaces "nothing to prune".

## Capabilities

### Modified Capabilities

- `hera-view`: the "Rail keybindings (area 4)" requirement gains `n`, `R`, `C`, and `Ctrl+R`, and the `w` key's behavior changes from a single-field prompt to the full new-task modal; the omitted-key NOTE/scenarios drop `n` and `Ctrl+R`. New requirements capture the full-modal worker spawn, the new-root-coordinator action, and the retire/prune EOL family.

## Impact

- **New code:** `internal/agent/hera_spawn.go` (`SpawnHeraCoordinator` + `HeraCoordinatorSpawnInput`/`Result` and a coordinator orientation string); `internal/db/hera.go` (`UniqueHeraOrchestratorName`); `internal/tui/hera/eol.go` (pure, testable selection helpers: `RoleView.IsFinished`, `Model.SubtreeArchivedWorkers`, `Model.FinishedRoles`, `Model.FullyFinishedOrchestratorIDs`).
- **Modified code:** `internal/tui/hera/page.go` (`OnNewCoordinator`/`OnRetire`/`OnPruneDescendants`/`OnPruneDone` callbacks + `n`/`R`/`C`/`Ctrl+R` cases in `handleRailMutation`); `internal/tui/hera/ops.go` (`MutateStore` gains `HeraLiveBindingByRole`/`EndHeraBinding`; `Ops.RetireRole`); `internal/tui/app.go` (callback wiring; new-task form return-page + `newTaskOnDone` override so the same modal serves the Hera tab); `internal/tui/heraactions.go` (handlers + reclaim helpers); `internal/tui/newtaskform.go` (settable title); `internal/tui/modal/help.go` (+ `help_test.go`); README keybinding table.
- **No schema change** — reuses `hera_orchestrators` / `hera_roles` / `hera_bindings` and the existing task status/archive columns.
- **Multi-binding isolation** is preserved throughout: every EOL op acts on the Selection's role/orchestrator and only reclaims a task/worktree when no OTHER live binding points at it.
- **Specs are LOCAL DOCS only** (`openspec/project.md`). Do NOT wire `openspec validate` into Go CI or `make`; the quality gate stays `make pre-pr`. Run `openspec validate --strict` locally only.

## MODIFIED Requirements

### Requirement: Rail keybindings (area 4)

The system SHALL bind the following keys while the rail holds focus: `j`/`k` and Down/Up move the cursor; Left walks to the parent; Space collapses/expands the row under the cursor; `Tab`/`Backtab` and `Ctrl+Alt+Left`/`Ctrl+Alt+Right` walk the focus ladder; `Ctrl+Q` returns focus to the rail; `Enter` enters the selected role's pane (restarting a dead/suspended session first); `w` spawns a worker under the selected coordinator's orchestrator via the full new-task modal; `n` creates a new top-level coordinator via the full new-task modal; `r` renames the selected role/orchestrator; `a` HIDES the selected worker / sub-coordinator into its parent coordinator's nested archive (Tier 1 — reversible, keeps the session + worktree alive); `P` toggles pin; `s`/`S` advance/revert the selected role's hera status; `m`/`M` advance/revert the selected TOP-LEVEL coordinator's kanban status (see the dedicated kanban requirement — a wholly separate axis from `s`/`S`); `J` adopts a freelancer / re-parents a coordinator; `B` bounces the selected role — on a coordinator selection it forces an immediate recycle (kills and restarts its session on the same task, seeded from its mission, plan-DAG state, and any handoff note — see `coordinator-context-management`); on a worker or freelance selection it instead sends the role's live session a system-input instruction asking it to call `hera_status(handoff_note=..., request_recycle=true)` itself, which the existing self-service recycle path (see `coordinator-context-management`) then completes once the role's session goes idle — both variants sit behind a confirmation modal, and `B` remains a no-op on an empty selection; `C` clears the selected coordinator's archive (NUKES every Tier-1 hidden item under it); `Ctrl+Z` fullscreens the focused pane; `Ctrl+J` opens the unified task/role switcher (see the switcher requirement below) — like `Ctrl+Z`, this fires regardless of which region (rail, coordinator pane, or agent pane) holds focus; `/` filters the rail by name; `Ctrl+D` NUKES the selected role/orchestrator (Tier 2 — removes it and its whole subtree from the rail, reclaims worktrees). Every bound key SHALL appear in the help overlay's "Hera View (rail)" section.

The rail SHALL NOT bind `R` (retire) or a rail-wide `Ctrl+R` (prune) — both are removed by this redesign. All rail mutation keys SHALL be suppressed while the rail is in `/` filter INPUT mode (every keystroke is filter input). Selection-acting keys (`w`, `r`, `a`, `P`, `s`/`S`, `m`/`M`, `J`, `B`, `C`, `Ctrl+D`) are no-ops on an empty selection; `n` is selection-INDEPENDENT and fires even on an empty rail. `m`/`M` additionally no-op on any non-empty selection that is not a top-level coordinator header (a role row, a nested/bridged sub-coordinator row, or a Freelance row) — see the dedicated kanban requirement.

`Ctrl+K` SHALL NOT be a rail or Hera-page key at all — it is intercepted globally (see the `command-palette` capability) before the Hera page ever sees it, so it never forwards to a focused pane's PTY and never collides with any rail-focus key.

Derived from: `internal/tui/hera/rail.go` (rail `InputHandler`), `internal/tui/hera/page.go` (page `InputHandler` focus ladder + `handleRailMutation`), `internal/tui/heraactions.go` (handlers), `internal/tui/modal/help.go` (help overlay Hera section), `internal/tui/keymap/actions.go` (`ActHeraKanbanAdv`/`ActHeraKanbanRev`).

`NOTE:` `Ctrl+D` is the only key that NUKES a live selection directly (`C` nukes only the selected coordinator's already-hidden Tier-1 archive items); the rail binds no `R` (retire) or rail-wide `Ctrl+R` (prune). `Ctrl+D` never collides with the agent-view `Ctrl+R` (Claude session switcher), which runs in a different mode/widget. `B` acts on a live coordinator, worker, or freelance session — immediate kill-and-restart for a coordinator, a self-service instruct-and-wait bounce for a worker/freelance role — but in every case preserves the task/worktree/branch/binding; unlike `Ctrl+D`, nothing is removed from the rail. A focused content pane forwards `C`/`a`/`Ctrl+D` to its PTY. `m`/`M` never collides with `s`/`S`: the two step entirely independent data (an orchestrator's `kanban_status` column vs. a role's `hera_role_status` row) and use independent stepping rules (`m`/`M` wraps; `s`/`S` clamps).

#### Scenario: Hide key acts on the current selection

- **WHEN** the rail is focused and the user presses `a` on a selected worker
- **THEN** the hide callback fires for that worker's `(role, orchestrator)` selection (no confirmation) and the key does not leak to navigation

#### Scenario: Retire and rail-wide prune keys are unbound

- **WHEN** the user presses `R` or `Ctrl+R` while the rail is focused
- **THEN** nothing end-of-life happens (`R` is unbound; `Ctrl+R` is not a rail-wide prune) — the redesign removed both

#### Scenario: Help overlay lists every rail key

- **WHEN** the help overlay is opened
- **THEN** its "Hera View (rail)" section lists `j`/`k`, space, Left, Tab/Ctrl+Q, Enter, `w`, `n`, `r`, `a` (hide), `P`, `s`/`S`, `m`/`M`, `J`, `B` (force recycle), `C` (clear archive), `Ctrl+Z`, `Ctrl+J` (switcher), `/`, and `Ctrl+D` (nuke), and does NOT list `R`, a rail-wide `Ctrl+R`, or `Ctrl+K` (which is not a Hera-page key)

#### Scenario: Force-recycle key requires confirmation on a coordinator selection

- **WHEN** the rail is focused, a coordinator row is selected, and the user presses `B`
- **THEN** a confirmation modal appears before the recycle proceeds immediately

#### Scenario: Bounce key requires confirmation on a worker or freelance selection

- **WHEN** the rail is focused, a worker or freelance row is selected, and the user presses `B`
- **THEN** a confirmation modal appears before anything is sent to the role's session

#### Scenario: Bounce key sends a self-service recycle instruction to a worker or freelance role

- **WHEN** the confirmation is accepted for a worker or freelance selection
- **THEN** a system-input instruction is sent to the role's live session asking it to call `hera_status(handoff_note=..., request_recycle=true)`, and no session is killed or restarted directly by this key press

#### Scenario: ctrl+j opens the switcher regardless of focused region

- **WHEN** the user presses `ctrl+j` while the rail, the coordinator pane, or the agent pane holds focus
- **THEN** the unified task/role switcher opens in every case, and the key never reaches a focused pane's PTY

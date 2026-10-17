**Design doc:** `openspec/changes/add-two-column-switcher/design.md`

Read `context/knowledge/gotchas/keybindings.md`, `gotchas/hera-view.md`, `gotchas/tasklist-ui.md`, and `context/knowledge/testing.md` before starting.

## 1. Tests

- [ ] 1.1 Write failing widget tests for the new seams: `TaskListView.SetFilter`, `TaskListView.NextNeedsInputTaskID` (wraps, expands the holding project), `Rail.SetFilter`, `Rail.CopyFoldStateFrom` (copy is independent: mutating the copy leaves the source unchanged), `HeraPage.SelectOrch`
- [ ] 1.2 Write failing `SwitcherPopup` unit tests from `specs/jump-switcher/spec.md`: Tasks-left/Projects-right layout render, hera-managed and archived tasks excluded from the Tasks column even with the hide toggle off, ↑/↓/←/→ navigation with per-column cursors, Space folds in Projects, typing filters both columns, empty-active-column handoff, `ctrl+g` order (Tasks then Projects, wrap, no-op when none), start-cursor rule (first `(?)`, else current task, else top of Tasks), Esc clears filter then closes, `ctrl+q` closes, remote-mode Projects placeholder skipped by ←/→ and `ctrl+g`
- [ ] 1.3 Write failing App/keymap tests: `global.switcher` default `ctrl+j` and `agent.switcher` gone; `ctrl+j` opens the popup from the classic agent view (zoom on and off), the Tasks tab, the Projects rail, and a focused Projects pane, with no PTY write; does not open over the new-task form or in plugin-view mode; `ctrl+g` inside the popup does not fire the global jump; config override of `global.switcher` applies everywhere; help overlay lists it
- [ ] 1.4 Write failing Enter-routing tests: Tasks row → agent view with tasklist cursor synced and no session restart; Projects role from the agent view → `exitAgentView` then Projects tab, ancestors expanded, focus on the role pane (port the intent of `TestSwitcher_SelectingHeraManagedEntryJumpsIntoHeraTab`); task-less Projects row → Projects tab with that orchestrator selected; real Tasks/Projects cursor and folds unchanged after popup navigation + Esc
- [ ] 1.5 Write a failing SimulationScreen smoke test (`smoke_test.go`) opening the popup via `ctrl+j` through the real event loop, typing a filter, and pressing Enter
- [ ] 1.6 Confirm every acceptance criterion in `design.md` maps to at least one failing test

## 2. Widget seams

**Depends on:** Stage 1

- [ ] 2.1 Add `TaskListView.SetFilter` and `TaskListView.NextNeedsInputTaskID` in `internal/tui/taskview/tasklist.go`
- [ ] 2.2 Add `Rail.SetFilter` and `Rail.CopyFoldStateFrom` in `internal/tui/hera/rail.go`; add `HeraPage.SelectOrch` in `internal/tui/hera/page.go`
- [ ] 2.3 Make the 1.1 tests pass; uxlog the seams that change cursor/filter state (`[tasklist]` / `[hera-view]` prefixes)

## 3. Jump switcher popup widget

**Depends on:** Stage 2

- [ ] 3.1 Create `internal/tui/switcherpopup.go`: `SwitcherPopup` (`tview.Box`) owning a private `TaskListView` and `Rail`, with `Draw` (filter line, two headed columns, footer hint, active-column highlight), `InputHandler`, `PasteHandler` (pastes into the filter), and a `MouseHandler` that keeps focus on the popup
- [ ] 3.2 Implement the shared filter, column switching + empty-column handoff, `ctrl+g` cycling, start-cursor rule, Esc/`ctrl+q` semantics, and an outcome API (`Canceled()`, `Selected()` → task ID / orch ID / column)
- [ ] 3.3 Make the 1.2 tests pass

## 4. App wiring and dispatch

**Depends on:** Stage 3

- [ ] 4.1 Keymap: replace `ActAgentSwitcher` with `ActGlobalSwitcher` (`global.switcher`, `ctrl+j`) in `defaultSpecs`, `actionLabels`, `contextOrder`; relabel the Hera rail static help row to "jump switcher"
- [ ] 4.2 `app.go`: add the unconditional `ActGlobalSwitcher` case next to `ActGlobalPalette`; delete the Tasks-tab `ActAgentSwitcher` block and the `handleAgentKey` case; delete `HeraPage.OnSwitcher` and its `KeyCtrlJ` case
- [ ] 4.3 Add `openJumpSwitcher` / `handleJumpSwitcherKey` / `closeJumpSwitcher`: snapshot tasks + needs-input/running/idle/PR maps + managed set into the Tasks column, `heraPage.Refresh()` + rail model + fold copy into the Projects column, return-mode/focus restore (three-way Tasks/Hera/agent fallback), re-feed both columns on each tick while open; uxlog open (candidate counts), selection, and cancel with a `[switcher]` prefix
- [ ] 4.4 Implement Enter routing per design D7 (Tasks row, Projects role incl. `exitAgentView` before `switchTab(TabHera)`, task-less Projects row via `SelectOrch`)
- [ ] 4.5 Update `commandpalette_actions.go` to register `ActGlobalSwitcher` → `openJumpSwitcher`
- [ ] 4.6 Make the 1.3, 1.4, and 1.5 tests pass

## 5. Retire the old switcher

**Depends on:** Stage 4

- [ ] 5.1 Delete `openTaskSwitcher`, the grouped path of `openTaskSwitcherModal`/`handleTaskSwitcherKey`, and `TaskSwitcherModal`'s grouped mode (`SetGrouped`, grouped rows/cursor/draw); keep the flat mode `herapicker.go` uses
- [ ] 5.2 Remove or port the obsolete grouped-switcher tests in `taskswitcher_test.go` / `taskswitcher_wiring_test.go`; keep flat-mode and status-glyph coverage

## 6. Docs and closeout

**Depends on:** Stage 5

- [ ] 6.1 README Reference keybinding table: `ctrl+j` → jump switcher (global); remove the agent-context row
- [ ] 6.2 Gotchas: update `gotchas/keybindings.md` (ctrl+j now unconditional `CtxGlobal`; popup consumes `ctrl+g`; plugin/modal exclusions; three old dispatch paths removed) and add popup invariants (private widget copies never persist; `exitAgentView` before `switchTab(TabHera)`); update `context/knowledge/index.md` bullet counts
- [ ] 6.3 Run `make pre-pr` clean (coverage ≥ floor on touched packages)
- [ ] 6.4 Archive: `openspec archive add-two-column-switcher` (or the manual merge-and-move fallback) on this branch before merge

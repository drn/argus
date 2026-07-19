## 1. Fix ctrl+j dead in the plain Tasks tab (keybindings capability)

- [x] 1.1 In `handleGlobalKey` (`app.go`), add a block gated on `a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks` that resolves `CtxAgent`'s `ActAgentSwitcher` directly and calls `a.openTaskSwitcher()` — reusing the existing binding table rather than adding a new `CtxTaskList` entry.
- [x] 1.2 Fix `closeTaskSwitcherModal`'s focus-restore bug the new reach exposes: it assumed Hera was the only non-agent origin; switch on `a.header.ActiveTab()` (mirrors `closeCommandPaletteModal`'s existing three-way fallback).
- [x] 1.3 Add a SimulationScreen smoke test driving `ctrl+j` through the real `handleGlobalKey` dispatch path from the plain Tasks tab, asserting the switcher opens and Esc returns focus to the task list (not Hera).

## 2. Add ctrl+g global jump-to-next-needs-input (keybindings + hera-view capabilities)

- [x] 2.1 Add `ActGlobalJumpNeedsInput` (`global.jump_needs_input`, default `ctrl+g`) to `CtxGlobal`'s `defaultSpecs`/`actionLabels`/`contextOrder` in `internal/tui/keymap/actions.go`.
- [x] 2.2 In `handleGlobalKey`, add the `ActGlobalJumpNeedsInput` case to the SAME unconditional-dispatch section `ActGlobalPalette` already uses, calling a new `a.jumpToNextNeedsInput()`.
- [x] 2.3 Add `railRow.needsInputTaskID()` and `Rail.NextNeedsInputTaskID()` in `internal/tui/hera/rail.go`: scan-and-cycle over `r.rows` in built order, candidates gated on `row.role != nil && role.needsInputOwn()` (excludes top-level coordinator headers by construction), starting after the cursor and wrapping around.
- [x] 2.4 Add `HeraPage.JumpToNextNeedsInput()` in `internal/tui/hera/page.go`, reusing `JumpToTask` verbatim (no new ancestor-expand/reattach/focus logic).
- [x] 2.5 Add `App.jumpToNextNeedsInput()` in `app.go`: switches to the Hera tab (tearing down the classic agent view first when active, mirroring the switcher's own hera-managed landing), calls `HeraPage.JumpToNextNeedsInput`, and flashes a "No role needs input" notice on a safe no-op.
- [x] 2.6 Add the palette registry entry (`commandpalette_actions.go`) so `ctrl+g` is also invokable from the command palette.
- [x] 2.7 Unit tests in `internal/tui/hera` (`Rail.NextNeedsInputTaskID`, `HeraPage.JumpToNextNeedsInput`): empty/no-candidates, single candidate, cycling through multiple without repeating, a coordinator's rolled-up header is never a distinct candidate, a top-level coordinator's own need is excluded, a candidate revealed behind a closed fold is still found, ancestor expansion + selection on landing, remote-mode no-op.
- [x] 2.8 SimulationScreen smoke tests in `internal/tui`: `ctrl+g` reachable from the plain Tasks tab, the classic fullscreen agent view, Hera rail focus, and a focused Hera pane (byte does not leak to the PTY); repeated real key presses cycle through two needs-input roles without repeating; safe no-op flashes a notice when nothing needs input.

## 3. Documentation

- [x] 3.1 Verify the `?` help modal reflects `ctrl+g` and the Tasks-tab `ctrl+j` fix automatically (keymap-generated) — add a `help_test.go` assertion rather than assuming.
- [x] 3.2 Update the README Reference keybinding table for the new `ctrl+g` action.
- [x] 3.3 Add invariants to `context/knowledge/gotchas/keybindings.md`: the Tasks-tab `ctrl+j` fix + its `closeTaskSwitcherModal` focus-restore bug, and the `ctrl+g` unconditional-dispatch mechanism.
- [x] 3.4 Add invariants to `context/knowledge/gotchas/hera-view.md`: `Rail.NextNeedsInputTaskID`'s cycling mechanics and its top-level-coordinator-header exclusion rationale.

## 4. Verification and ship

- [x] 4.1 Run `make test` and `make test-cover` on touched packages (`internal/tui`, `internal/tui/keymap`, `internal/tui/hera`); confirm ≥95% on touched packages (90% for UI smoke-only code) per this repo's testing rules.
- [x] 4.2 Archive this OpenSpec change (`openspec archive add-hera-jump-question`) in the same PR, before merge.
- [x] 4.3 Run `make pre-pr` clean (build+vet+fmt-check+lint-pr+vuln+test-cover-gate).
- [x] 4.4 Push to the existing `argus/hera-nav-palette` branch / PR #865 (no new PR).

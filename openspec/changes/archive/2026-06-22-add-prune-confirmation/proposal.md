## Why

`Ctrl+R` on the Tasks tab pruned every completed task immediately — deleting
their DB rows, stopping sessions, and removing worktrees and branches — with no
confirmation. This is irreversible and trivially fired by accident (Ctrl+R is a
single chord, and the adjacent keys drive common actions). A stray keypress can
wipe a stack of completed work and its git state. Destructive task actions
elsewhere (delete task, delete project, restart supervisor) are already gated
behind a y/N modal; prune was the conspicuous exception.

## What Changes

- `Ctrl+R` on the Tasks tab no longer prunes directly. It first counts the
  completed tasks and, if any exist, opens a `modal.ConfirmModal` ("Prune
  completed tasks") that names the count and warns the deletion of worktrees and
  branches cannot be undone. Only an explicit confirm (`Enter`/`y`) runs the
  existing two-phase prune; `Esc`/`n` cancels with nothing removed.
- When no completed tasks exist, the modal is skipped entirely and a brief
  status note ("No completed tasks to prune") replaces the previous silent
  no-op, so the keypress always has visible feedback.
- The re-entrancy guard moves to the gate: if a prune is already running (header
  notice set) the confirmation does not open, matching the prior double-Ctrl+R
  protection.
- No keybinding is added, removed, or rebound — `Ctrl+R` keeps its "prune
  completed" meaning, so the help modal and statusbar are unchanged.

## Impact

- Affected specs: `task-list-view` (the `Ctrl+R` trigger gains a confirmation
  gate). The two-phase prune mechanic in `worktree-management` is unchanged.
- Affected code: `internal/tui/app.go` (`openConfirmPrune`,
  `handleConfirmPruneKey`, `closeConfirmPrune`, `modeConfirmPrune`, the
  `Ctrl+R` handler, and the InputCapture dispatch). Reuses the existing
  `modal.ConfirmModal`.

## Pre-tcell Bubble Tea Reference Commit

The last commit before the tcell/tview migration is `5b8d560`. The old Bubble Tea UI lived in `internal/ui/` and was deleted in commit `4f7ae62` (Phase 11: Delete Bubble Tea runtime).

To view old implementations: `git show 5b8d560:internal/ui/<file>`

Key old files:
- `internal/ui/tasklist.go` — task list with navigation, archive, auto-expand
- `internal/ui/root.go` — key bindings, callbacks, app-level wiring
- `internal/ui/newtask.go` — new task form with autocomplete
- `internal/ui/agentpane.go` — agent view
- `internal/ui/wordboundary.go` — word navigation helpers
- `internal/ui/worktree.go` — worktree cleanup helpers

When checking for missing functionality from the old UI, diff or read files at this commit to see the original behavior.

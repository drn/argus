# Copy name or prompt from the task list

## Why

The `c` key in the task list copies the task prompt directly to the clipboard.
Users also frequently want the task **name** (e.g. to paste into a branch name,
a message, or a search). Today there is no keyboard path to copy the name.

## What Changes

- `c` on the selected task now opens a small **copy-choice modal** instead of
  copying the prompt immediately.
- The modal offers two choices: **Name** and **Prompt**. Selecting one copies
  that field to the OS clipboard and dismisses the modal.
- The prompt choice is only offered when the task has a non-empty prompt; the
  name choice is always available (every task has a name).
- Esc / Ctrl+Q cancels the modal without copying.
- Help modal + README reference table updated for the new `c` behavior.

## Impact

- Affected specs: `task-list-view` (copy action), `forms-and-modals` (new modal).
- Affected code: `internal/tui/taskview/tasklist.go` (rename `OnCopyPrompt` →
  `OnCopy`, always-fire on a selected task), `internal/tui/modal/copychoice.go`
  (new), `internal/tui/app.go` (mode + wiring), `internal/tui/modal/help.go`,
  README reference table.

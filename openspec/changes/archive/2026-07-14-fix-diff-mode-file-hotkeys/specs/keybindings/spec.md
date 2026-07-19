## ADDED Requirements

### Requirement: File-action hotkeys remain available while viewing a diff

The diff-view context (`CtxDiff`) SHALL also resolve the file-action hotkeys
(reveal in Finder, open file, open in editor, open terminal) against the
currently selected file, so a user does not lose these hotkeys the moment
`Enter` displays a file's diff.

#### Scenario: Finder hotkey works while a diff is displayed

- **WHEN** a file's diff is currently displayed (diff mode active)
- **AND** the user presses the reveal-in-Finder key (default `f`)
- **THEN** the currently selected file is revealed in Finder, same as when the
  file panel (not the diff) has focus

#### Scenario: Open/editor/terminal hotkeys work while a diff is displayed

- **WHEN** a file's diff is currently displayed (diff mode active)
- **AND** the user presses the open-file, open-in-editor, or open-terminal key
  (defaults `o`/`e`/`t`)
- **THEN** the corresponding action runs against the currently selected file
  (or the worktree directory, for open-terminal), same as when the file panel
  has focus

## Why

In the agent view's file panel, the file-action hotkeys (`f` reveal in Finder,
`o` open file, `e` open in editor, `t` open terminal) work while a file is
merely selected/highlighted in the list, but stop working the moment `Enter`
displays that file's diff. `App.handleGlobalKey` routes every key to
`handleDiffKey` once `agentPane.InDiffMode()` is true, and `handleDiffKey` only
resolves the `keymap.CtxDiff` action table (split-toggle + scroll), never
`CtxFilePnl` — so `f`/`o`/`e`/`t` are silently swallowed while a diff is
displayed. The selected file (`filePanel.SelectedFile()`) is still correct in
diff mode (arrow-key file navigation already depends on this), so there's no
reason these hotkeys should stop working.

## What Changes

- **The diff-view keymap context gains its own Finder/Open/Editor/Terminal
  actions**, bound to the same default keys as the file panel (`f`/`o`/`e`/`t`),
  dispatched to the existing `openInFinder`/`openFile`/`openInEditor`/
  `openTerminal` handlers against the currently-selected file.
- No change to file-panel-mode behavior, diff entry/exit, or any other hotkey.

## Capabilities

### Modified Capabilities

- `keybindings`: the diff-view context (`CtxDiff`) now also resolves the
  file-action hotkeys (reveal in Finder, open file, open in editor, open
  terminal), previously only available in the file-panel context.

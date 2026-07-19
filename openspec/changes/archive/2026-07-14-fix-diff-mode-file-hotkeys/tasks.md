## 1. Keymap

- [x] 1.1 Add `ActDiffFinder`/`ActDiffOpen`/`ActDiffEditor`/`ActDiffTerminal` to
      `internal/tui/keymap/actions.go` (defaults `f`/`o`/`e`/`t`, labels,
      `contextOrder[CtxDiff]`).

## 2. Dispatch

- [x] 2.1 In `internal/tui/app.go` `handleDiffKey`, dispatch the new actions to
      `openInFinder`/`openFile`/`openInEditor`/`openTerminal`.

## 3. Tests

- [x] 3.1 Keymap unit test: `CtxDiff` resolves `f`/`o`/`e`/`t` to the new
      actions.
- [x] 3.2 `app` test: pressing `f`/`o`/`e`/`t` while `InDiffMode()` invokes the
      corresponding opener stub with the selected file.

## 4. Docs

- [x] 4.1 Archive this change into `openspec/specs/keybindings/spec.md`.

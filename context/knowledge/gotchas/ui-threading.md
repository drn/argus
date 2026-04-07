## UI Threading

- **Never run git commands synchronously on the UI thread.** Even fast commands take 50-500ms. Use background goroutines + `QueueUpdateDraw`.
- **Never call `GetInnerRect()` from the tick goroutine.** tview is not thread-safe. Store pending values under mutex in `Draw()`, read from tick goroutine.
- **`refreshTasks()` must not do RPC while holding `a.mu`.** Fetch `runningIDs` OUTSIDE the lock.
- **`TaskPreviewPanel.Draw()` must never call `runner.Get()` or create a VT emulator.** Pre-render in `RefreshOutput()` on tick goroutine; `Draw()` only paints cached cells.
- **Never run synchronous git commands on the tick goroutine.** Blocking the tick goroutine prevents `QueueUpdateDraw` from firing, freezing the UI. Use `go` + cooldown (e.g., `lastTaskGitRefresh` with 3s interval). The agent view already follows this pattern — the task list must too.
- **`onTick` must modify tview widget state only inside `QueueUpdateDraw`.** `TaskListView`, preview panels, agent pane, and reviews have no internal mutex. Direct modification from the tick goroutine races with `Draw()`/`InputHandler()` on the tview goroutine. Symptom: `buildRows()` sets `tl.rows = nil` then rebuilds — a concurrent `Draw()` sees the nil slice and renders an empty task list (project folders disappear).

## Paste & Input Batching

- **`tapp.EnablePaste(true)` is required for fast paste.** Without it, tview delivers paste as thousands of individual `EventKey` events, each triggering a full screen redraw. With it, tview buffers all pasted text and delivers it as a single `PasteHandler()` call with one redraw.
- **`EnablePaste`/`EnableMouse` must be called AFTER `SetScreen`.** tview's `EnablePaste` only calls `screen.EnablePaste()` when `a.screen != nil`. And `Run()` only auto-enables when it creates its own screen (`a.screen == nil`). If `SetScreen` is called before `Run`, and `EnablePaste` was called before `SetScreen`, the flag is stored but `screen.EnablePaste()` is never invoked.
- **Every custom widget with text input must implement `PasteHandler()`.** tview's paste path bypasses `InputCapture` entirely — it goes through the focus chain calling `PasteHandler()` on the focused primitive. If a widget only has `InputHandler()`, paste is silently dropped when `EnablePaste` is on.
- **TerminalPane paste must wrap text in bracket paste sequences.** Send `\x1b[200~` + text + `\x1b[201~` so the agent's readline treats it as a paste (no per-character echo/processing).

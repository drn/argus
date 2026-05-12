## UI Threading

- **Never run git commands synchronously on the UI thread.** Even fast commands take 50-500ms. Use background goroutines + `QueueUpdateDraw`.
- **The "no sync external commands on the UI thread" rule generalizes beyond git** — `launchctl`, `gh`, `go install`, anything that shells out. The tempting "<100ms is fine" carve-out is a trap: setting `busy=true` + calling `rebuildRows()` before the blocking call has zero visible effect because the tview event loop can't repaint until your handler returns. Pattern: settings view exposes an `OnXxx` callback, app wires it to `go app.doXxx()`, the goroutine calls `tapp.QueueUpdateDraw(SetXxxResult)` to clear busy on completion. See `OnToggleAutoStart` / `app.toggleAutoStart` for a minimal example.
- **Never call `GetInnerRect()` from the tick goroutine.** tview is not thread-safe. Store pending values under mutex in `Draw()`, read from tick goroutine.
- **`refreshTasks()` must not do RPC while holding `a.mu`.** Fetch `runningIDs` OUTSIDE the lock.
- **`TaskPreviewPanel.Draw()` must never call `runner.Get()` or create a VT emulator.** Pre-render in `RefreshOutput()` on tick goroutine; `Draw()` only paints cached cells.
- **Never run synchronous git commands on the tick goroutine.** Blocking the tick goroutine prevents `QueueUpdateDraw` from firing, freezing the UI. Use `go` + cooldown (e.g., `lastTaskGitRefresh` with 3s interval). The agent view already follows this pattern — the task list must too.
- **`onTick` must modify tview widget state only inside `QueueUpdateDraw`.** `TaskListView`, preview panels, and agent pane have no internal mutex. Direct modification from the tick goroutine races with `Draw()`/`InputHandler()` on the tview goroutine. Symptom: `buildRows()` sets `tl.rows = nil` then rebuilds — a concurrent `Draw()` sees the nil slice and renders an empty task list (project folders disappear).
- **Never call `QueueUpdateDraw` (or `QueueUpdate`) before `tapp.Run()` starts.** It deadlocks. tview v0.42 made `QueueUpdate` synchronous: it enqueues `{f, done: ch}` on the buffered `updates` channel (cap 100), then **blocks on `<-ch` until the event loop runs `f` and signals `ch` (a send, `update.done <- struct{}{}` at `application.go:512` — not a `close`)**. Without `tapp.Run()` running, no one drains the queue, the closure never fires, and `<-ch` blocks forever. Symptom: TUI starts (alt-screen sequences emitted), but no frame is ever painted, no log line after the queued call appears, and the process hangs until killed. Fire startup-time UI side effects (like opening the daemon-stale modal) by **calling the open function directly** before `tapp.Run()` — direct mutation is safe because no Draw goroutine exists yet (not because tview synchronizes; `Pages.AddPage` / `SwitchToPage` have no internal lock — only `Application.SetFocus` does). If you genuinely need the event loop, spawn a goroutine: `go a.tapp.QueueUpdateDraw(...)`. Earlier docs in this file claimed the buffered channel made queuing safe — that was wrong (tview's API changed).

## lazyScreen & Widget Painting

- **`lazyScreen` is a passthrough wrapper with no Clear-skip optimization.** A previous revision had `skipClear` to avoid the ~10K cell writes that tview's screen-wide `Clear()` triggered per PTY keystroke. That caused ghost status bars and tearing whenever the layout shifted between a skipped-Clear frame and the next real-Clear frame (resize, page swap, agent-header toggle). It was removed because `tcell.Show()` already diffs cells against their last-emitted state — `Clear()` followed by widgets restoring the same content produces zero terminal I/O, so the "optimization" was only saving in-process cell-buffer writes. Do not re-add `skipClear` without a strategy for invalidating it on every layout change.
- **Every widget that uses `DrawBorderedPanel` inherits an interior blank-fill.** `DrawBorderedPanel` calls `FillArea` on the inner rect with `(' ', StyleDefault)` before drawing the border. This is defense-in-depth — `tview.Clear()` already blanks the whole screen each frame — but it keeps widgets correct under any future Clear-bypass optimization.
- **If you add a new widget that does NOT go through `DrawBorderedPanel`, fill its full bounding box yourself in `Draw`.** `StatusBar` and `Header` already do this (they fill their row with a background-styled space). Widgets that only paint "occupied" rows (just the rows with content) break the moment a future optimization bypasses the screen-wide `Clear()`.
- **Don't add `screen.Sync()` to fix tearing.** This is the rule. The codebase went through a 12-commit cycle (`9bb8d4c`, `1ff2bcc`, `b9baafc`, `0bc1cb0`, `b2bb42f`, `9a14af4`, `f52bce0`, `9d0a56c`, `056b283`, `47c2e84`, `08df735c`, `2fdcb44`) chasing visible "tearing" in tmux by sprinkling Sync calls everywhere. Every fix made it worse because `tcell.Sync()` emits `CSI 2J` (clear-screen) which tmux faithfully propagates to the outer terminal as a visible flash. The whole saga was a self-inflicted regression — see the post-mortem below.

## tmux UX-tearing post-mortem (March 22 – May 12, 2026)

### The regression vector

Commit `94797775` (2026-03-22, "Eliminate residual typing lag via lazyScreen Clear() bypass") added a `skipClear` optimization based on a wrong premise: that `tview.Clear()` triggered 10K cell writes per keystroke. It did mark cells dirty in tcell's cell buffer, but `tcell.Show()` diffs each cell against its **last-emitted** state (not its post-Clear value), so widgets restoring the same content produced zero terminal I/O regardless. The "win" was illusory.

`skipClear` forced a load-bearing invariant: "every widget's Draw must paint every cell in its bounding rect, or stale cells leak through the no-op Clear." Widgets that drew only "occupied" rows started leaking ghost text. The first ghost report (`644dda6c`, Apr 21) was fixed by adding `FillArea` to `DrawBorderedPanel` — the right fix.

The wrong fix came one day later. `e516ad33` (Apr 22) removed `skipClear`. The very next day, `0e0e38d5` (Apr 23) added the first `screen.Sync()` call ever — `forceRedraw` on tab switches, agent enter/exit, Ctrl+L. The author's commit message: **"Root cause in tcell/tmux/Alacritty emission could not be pinned to a single bug from code review alone. The Sync-based fix is defense-in-depth."** Translation: I couldn't find the bug, so I added a Sync.

That Sync should have been removed when `skipClear` went away. It wasn't. Twelve subsequent commits piled on `OnBranchChange`, `OnContentChange`, `forceSync`, `multiplexerMode`, `pendingContentSync`, hash-gating, focus-event recovery — all built on the unchallenged premise that "tcell's diff is unreliable inside tmux, we need to Sync." That premise is **factually wrong**.

### Why the premise is wrong

For tcell v2.13.x:

1. **There is no cross-frame cursor or SGR cache to desync.** `tscreen.go` `draw()` (called by both `Show()` and `Sync()`) resets `t.cx = -1`, `t.cy = -1`, `t.curstyle = styleInvalid` at the top of every call. The first dirty cell of every frame emits absolute positioning and a fresh style. There is no state carried across frames that tmux could corrupt.

2. **tcell auto-emits DECSET 2026 (BSU/ESU / Synchronized Output) when the terminfo is `XTermLike`** — and tmux IS `XTermLike` per `terminfo/t/tmux/term.go:42`. PR [gdamore/tcell#830](https://github.com/gdamore/tcell/pull/830) landed in v2.13.0. Every `draw()` is wrapped in `\x1b[?2026h` … `\x1b[?2026l` automatically. Frame emission is atomic.

3. **tmux passthrough requires user config.** For the outer terminal to honor the inner BSU/ESU, the user needs `set -as terminal-features ',xterm*:sync'` in `~/.tmux.conf` (tmux 3.4+). Without it, tmux 3.4+ swallows the inner sequence and the outer terminal sees unsynchronized frames. **This is a tmux config issue, not an argus code issue.**

4. **gdamore (tcell maintainer)** in [issue #647](https://github.com/gdamore/tcell/issues/647): *"Sync is probably not what you want to do. Sync should only be used to repair screen damage because it assumes everything is messed up and it clears the screen. Show() should be used instead."*

### What argus does now

- **`afterDraw` is minimal — resize-Sync only.** It compares the screen size against `lastScreenW/H`; on change, it Syncs once. No `pendingSync`/`forceRedraw`/`OnContentChange` consumption — those scaffolds are all gone. The reason resize needs Sync: tcell's diff compares the new cell buffer against the prior emit, not against the terminal's actual state. Resize is the one event where those diverge — the terminal physically changed size, prior-frame cells at the old positions can survive into the next frame as stale content (visible as stacked status bars).
- **`forceRedraw` is log-only.** It does NOT trigger Sync. It's preserved so a debug trail of "what transitions fired this frame" survives in `~/.argus/ux.log` — useful for chasing future drift reports.
- **`pendingSync`, `pendingContentSync`, `multiplexerMode`, `forceContentSync`, `detectMultiplexer`, `multiplexer.go`, `ARGUS_FORCE_SYNC` — all deleted.**
- **Three callsites call `screen.Sync()` directly, by design — all "repair screen damage" cases per gdamore's intent:**
  - **`afterDraw` on resize** (lastScreenW/H mismatch). One Sync per resize event.
  - **`onFocusGained`** (lazyScreen.PollEvent → focus regain). tmux/iTerm2 may have repainted our pane from a stale backing while we were unfocused. One Sync on focus return clears any drift. Rare event.
  - **Ctrl+L** (user-initiated refresh). One Sync, expected to flash, that's the contract.
- **OnBranchChange / OnLayoutChange callbacks still exist** on widgets and the app still wires them to `forceRedraw`. They're now harmless log-only signals. Don't add new ones expecting a Sync — the helper doesn't do that anymore.

### Rules going forward

1. **Don't add `screen.Sync()` for any "tearing" symptom without first ruling out user-side tmux config.** Direct the user to README's "Running inside tmux" section.
2. **Don't restore the `forceRedraw → pendingSync → Sync` chain.** That whole pattern was a downstream consequence of `skipClear`. It's gone.
3. **If you see tearing that tmux config doesn't fix:** verify with `set -as terminal-features ',xterm*:sync'` enabled. If it persists, the issue is a real widget bug (not painting its full bounding rect, or some bytes leaking past tcell). Fix the widget, not by adding Sync.
4. **`FillArea` and `DrawBorderedPanel` are the correct primitives for "make sure every cell in this rect is painted every frame."** Use them when adding new widgets.
5. **Read this entire post-mortem before adding any rendering-related code.** And then update it if the situation evolves — but don't delete the history. Future agents need the context.

## Widget painting

- **Every widget that uses `DrawBorderedPanel` inherits an interior blank-fill.** `DrawBorderedPanel` calls `FillArea` on the inner rect with `(' ', StyleDefault)` before drawing the border. Defense-in-depth on top of `tview.Clear()`'s screen-wide blank.
- **If you add a new widget that does NOT go through `DrawBorderedPanel`, fill its full bounding box yourself in `Draw`.** `StatusBar` and `Header` already do this (they fill their row with a background-styled space).
- **`lazyScreen` is a passthrough wrapper with no Clear-skip optimization.** A previous revision had `skipClear` (commit `94797775`) — see post-mortem above. **Do not re-add it under any circumstance.** It silently set off the entire 12-commit tearing-fix cycle.
- **Historical note: commits `9bb8d4c`, `1ff2bcc`, and several others in the 12-commit cycle iteratively added OnBranchChange/OnLayoutChange wirings ("every branch swap must Sync"). Those wirings still exist** in the widget structs (`OnBranchChange func()`, `OnLayoutChange func()`) but the App-side handler `forceRedraw` is now log-only — they fire log entries useful as a debug trail of what fired this frame but do NOT trigger Sync. The "every branch swap must Sync" contract was wrong; the correct contract is "every Draw fully paints its bounding rect" (enforced by `DrawBorderedPanel`'s FillArea), which tcell.Show()'s diff can then handle correctly on its own. Don't restore the Sync side of this contract. If you observe tearing in a specific widget, check that widget's Draw covers its rect — don't wire Sync.

## Paste & Input Batching

- **`tapp.EnablePaste(true)` is required for fast paste.** Without it, tview delivers paste as thousands of individual `EventKey` events, each triggering a full screen redraw. With it, tview buffers all pasted text and delivers it as a single `PasteHandler()` call with one redraw.
- **`EnablePaste`/`EnableMouse` must be called AFTER `SetScreen`.** tview's `EnablePaste` only calls `screen.EnablePaste()` when `a.screen != nil`. And `Run()` only auto-enables when it creates its own screen (`a.screen == nil`). If `SetScreen` is called before `Run`, and `EnablePaste` was called before `SetScreen`, the flag is stored but `screen.EnablePaste()` is never invoked.
- **Every custom widget with text input must implement `PasteHandler()`.** tview's paste path bypasses `InputCapture` entirely — it goes through the focus chain calling `PasteHandler()` on the focused primitive. If a widget only has `InputHandler()`, paste is silently dropped when `EnablePaste` is on.
- **TerminalPane paste must wrap text in bracket paste sequences.** Send `\x1b[200~` + text + `\x1b[201~` so the agent's readline treats it as a paste (no per-character echo/processing).

## Key Learnings

Non-obvious invariants and gotchas. For architecture, see CLAUDE.md. For feature descriptions, read the code.

### Worktree Management

- **Orphan sweep must use `App.wtRoot`, never hardcode `~/.argus/worktrees/`.** Tests MUST set `app.wtRoot = t.TempDir()` — without this, `sweepOrphanedWorktrees` scans real worktrees and deletes them (including running agents' working directories).
- **`testGuard` prevents tests from operating on real `~/.argus/` worktrees (defense-in-depth).** `removeWorktree` and `removeWorktreeAndBranch` call `testGuard(path)` which detects `go test` (via `os.Args[0]` suffix `.test`) and blocks operations on real `~/.argus/` paths. Last-resort safety net — tests should still use `t.TempDir()` correctly.
- **`CreateWorktree` must prune stale refs first.** `git worktree prune` runs at the top (best-effort). Without it, a deleted worktree directory leaves a stale entry that locks the branch, causing exit status 255. After cmd failure, check `os.Stat(wtDir)` for partial success from post-checkout hook failures.
- **Worktree creation must succeed before task persistence.** Never `db.Add(task)` without a valid worktree. `CreateWorktree` returns `(wtPath, finalName, branchName, err)` — store `branchName` on `task.Branch` so cleanup deletes the correct `argus/*` branch.
- **`removeWorktree` must validate paths before `os.RemoveAll`.** The `isWorktreeSubdir` guard ensures the path contains `/.argus/worktrees/` or `/.claude/worktrees/` before any removal.
- **Worktree cleanup must always `os.RemoveAll` after `git worktree remove`.** The git command can exit 0 but leave behind empty dirs. Run `git worktree prune` before `git branch -D` — git refuses to delete branches with stale worktree references.
- **`git worktree add` requires a valid local ref or remote-tracking ref.** `resolveStartPoint()` checks `git rev-parse --verify` and falls back to `upstream/<branch>` then `origin/<branch>`.
- **Task names must be sanitized before branch/dir creation.** `sanitizeBranchName()` strips git-invalid characters. Without this, characters like `?` cause exit status 255.

### Daemon & RPC

- **Reconciliation must distinguish RPC failure from empty running set.** `Client.Running()` returns `nil` on RPC error vs `[]string{}` when daemon confirms no sessions. Check `runningIDs != nil && !daemonRestarting` before reconciling — during restart the new daemon has no sessions, so reconciliation would incorrectly mark all InProgress tasks Complete.
- **All daemon RPC calls must have a timeout.** `net/rpc.Client.Call()` blocks indefinitely. Use the `c.call()` wrapper with `select + time.After`. The buffered channel (`make(chan error, 1)`) is critical to prevent goroutine leak.
- **Stream loss ≠ process exit.** Use `StreamLost` flag to distinguish. When `isSessionAlive()` returns `reachable=false`, treat as stream lost, not exit. **The `<-rs.done` path in `connectStream`'s retry loop must also use `removeSessionStreamLost`** — `Client.Close()` during daemon restart closes `done`, and using `removeSession` causes a zero-value ExitInfo (StreamLost=false) because the GetExitInfo RPC fails on the closed client, incorrectly marking the task Complete.
- **`client.Get()` must return `nil` for any non-alive session.** Check `!info.Alive` alone — PID is stale between `onFinish()` and `delete(r.sessions)`.
- **Daemon health check uses `Ping()`, not `refreshTasks()`.** Ping fails fast; refreshTasks blocks on RPC.
- **Never call `refreshTasks()` from the tview main goroutine.** It blocks on RPC. Use `refreshTasksAsync()` (spawns goroutine + QueueUpdateDraw) or `refreshTasksLocal()` (reuses cached IDs, no RPC). Use `refreshTasksLocal` when only DB state changed (delete/prune); use `refreshTasksAsync` when session state may have changed.
- **Never call `refreshTasksAsync()` between `db.Add(task)` and `startSession()`.** The async RPC snapshot captures running IDs before the session exists, then reconciliation sees InProgress + not-in-running-set → marks Complete. Use `refreshTasksLocal()` instead (no RPC, no reconciliation race).
- **Always use `RunningAndIdle()` instead of separate `Running()` + `Idle()`.** They share the same `ListSessions` RPC — calling both separately doubles RPC overhead and doubles head-of-line blocking risk on the single `net/rpc` connection.
- **Daemon cleanup must run on the `Serve` goroutine, not `Shutdown`.** `Shutdown()` runs on a non-main goroutine — process may exit before cleanup completes. `signal.Stop(sigCh)` must be called after the handler goroutine exits.
- **Daemon restart must preserve `SessionID` on tasks.** Clearing it loses conversation history. On re-launch, `--resume <id>` picks up where it left off.
- **Rebuilding the binary does NOT update the running daemon.** Kill and restart: `kill -TERM $(cat ~/.argus/daemon.pid)`.
- **`onFinish` must fire before session removal.** Callback runs OUTSIDE `r.mu` (to avoid deadlock), then `delete(r.sessions)` in a second lock section.
- **`startOrAttach` must revert all state on Start failure.** Reset to Pending, clear SessionID, zero StartedAt. `SetStatus(StatusPending)` doesn't clear StartedAt — explicit zero required.
- **Daemon session exits must be wired to TUI via `client.OnSessionExit`.** Without this, tasks stay InProgress forever in daemon mode.
- **`restartDaemon` must re-wire `OnSessionExit` on the new client.**
- **SessionID must be populated before first `runner.Start` for Claude backends, and captured post-exit for Codex.** Claude uses `--session-id <uuid>` on first run and `--resume <uuid>` on subsequent runs. Codex captures its ID from `~/.codex/state_5.sqlite` after exit. Without a SessionID, `resume` is always false and every start is a fresh conversation.
- **Codex session ID capture (`CaptureCodexSessionID`) must run off the tview main goroutine.** It opens a SQLite connection which can block. Use a background goroutine + `QueueUpdateDraw` to persist.

### PTY & Terminal Rendering

- **`pty.Setsize` (which calls `os.File.Fd()`) races with `os.File.Close()`.** `Fd()` reads the internal fd field without synchronization, while `Close()` modifies it. `Read()`/`Write()` are safe (they use internal `fdMutex`). Fix: `ptmxClosed` bool flag under Session mutex; `waitLoop` sets flag + closes under lock; `Resize` checks flag under lock before calling `Setsize`.
- **PTY needs real terminal size at launch** (`pty.StartWithSize`), not 0x0. TUI apps won't render with zero dimensions. Start at actual panel width, not 80x24 — agents format initial output for launch PTY size.
- **Single-reader-tee pattern is critical.** Two goroutines reading the same fd causes data loss.
- **`AddWriter` must replay before registering.** Register first → live bytes arrive before replay → duplicate data → rendering corruption.
- **x/vt `SafeEmulator` hangs on terminal query sequences.** Use `newDrainedEmulator()` which starts `go io.Copy(io.Discard, emu)` to drain the response pipe. Never use bare `xvt.NewSafeEmulator()` in tui2.
- **x/vt can panic on replay from differently-sized terminals.** Use `safeEmuWrite()` which wraps with `recover()`.
- **Cursor rendering respects `CursorVisibility` callback.** Tracked via `cursorVisible` field, updated by x/vt callback. Defaults to `false` on emulator creation.
- **Ring buffer must be bounded (256KB).** Unbounded causes CPU spikes and OOM. Session log file provides full scrollback.
- **Live scrollback reads from session log file, not ring buffer.** `readLogTail` seeks from EOF — gives infinite scrollback while live follow-tail stays on the fast ring buffer path.
- **Anchor-lock keeps scrolled-up content pinned when new output arrives.** Track `anchorTotalLines`; bump `scrollOffset` by the delta. Reset to 0 on scroll-to-bottom.
- **`renderReplay` must reset `anchorTotalLines` on rebuild.** The replay emulator (fed from 1MB log tail) has far more scrollback than the live emulator (256KB ring buffer). Without reset, anchor-lock sees the totalLines jump as "new output" and bumps scrollOffset by the delta, causing a half-page jump on the first Shift+Up.
- **`ScrollUp`/`AccelScrollUp` must invalidate `replayEmu` AND `anchorTotalLines` on the 0→>0 transition.** Two bugs: (1) stale replay emu content — built during a previous scroll, its bottom doesn't match current live output, so `scrollOffset=1` shows content from hundreds of lines ago; (2) stale anchor — `anchorTotalLines` from live mode causes `paintEmu` to bump scrollOffset by `(replayTotal - liveTotal)`. Fix: `tp.replayEmu = nil; tp.anchorTotalLines = 0` when `scrollOffset == 0`, forcing a fresh rebuild from current log tail.
- **Replay emulator is cached and reused when only scroll offset changes.** Rebuild only when input size or dimensions change. For **live sessions scrolled up**, treat cache as always valid (new bytes arrive below viewport) — checking log size growth invalidates on every Draw since the agent is writing. For dead sessions, stat the log file (~1μs) instead of reading it to check validity.
- **`replayEmuMaxScroll` must reflect actual emulator scrollback capacity, not the build-time scroll offset.** Setting it to `tp.scrollOffset` caused a full rebuild (log read + emulator feed) on every scroll step beyond the initial offset — the primary cause of slow scrollback in long sessions. Compute from `emu.ScrollbackLen() + lastContentRow + 1 - viewportHeight` after building.
- **Replay emulators use `newDrainedReplayEmulator` with 50K-line scrollback (vs 10K default).** This allows deep scrolling without rebuilds. The 8MB minimum log read populates ~60-70% of the buffer on first scroll.
- **Agent view replay must use current panel dimensions, not stale PTY size.** Override `ptyCols/ptyRows` with current dimensions for dead/nil sessions.
- **Preview panel must use PTY width, not panel width, for VT emulation.** Otherwise text double-wraps.
- **New emulators must default `cursorVisible` to `false`.** Agents send `\e[?25l` early, but after ring buffer wrap or emu rebuild, that sequence is lost. Defaulting to `true` causes a phantom cursor at bottom-left. Also, `lastContentRow` must not extend to cursor position when cursor is hidden.
- **`renderLive` must skip `RecentOutput()` when `newBytes == 0`.** The 256KB ring buffer copy on every draw causes typing lag — keystroke redraws trigger `Draw()` before PTY echo arrives, so copying is wasted. `emuFedTotal` must only advance when bytes are actually fed to the emulator; advancing it on an empty `raw` silently skips those bytes permanently.
- **`paintEmu` must cache cells for replay on idle redraws.** tview's `screen.Clear()` defeats tcell's dirty tracking (fills all cells with spaces → every `SetContent` marks cells dirty → full terminal I/O). On no-change redraws, replay cached `[]cachedCell` directly — skips 10K+ mutex ops, allocations, and style conversions per frame. Invalidate cache on scroll, reset, or session change.
- **`startAgentRedrawLoop` must skip `QueueUpdateDraw` when idle.** Keystroke and resize events trigger their own tview redraws; the 200ms loop only needs to fire when new PTY output arrives (`TotalWritten` changed).
- **`uvCellToTcellStyle` must map ALL ultraviolet attributes.** Missing `AttrFaint→Dim` caused Ink-based CLIs (Codex) to lose visual contrast. Keep in sync with `uv.Attr*` constants: Bold, Faint, Italic, Blink, Reverse, Strikethrough. Also map underline styles (curly/dotted/dashed/double), underline color, and hyperlinks (OSC 8).

- **PTY keystroke follow-up redraw must guard on `TotalWritten`.** The immediate tview draw (from returning `nil`) fires before the PTY echo arrives. A 16ms delayed `QueueUpdateDraw` catches the echo, but it MUST check `sess.TotalWritten() != tw` — without the guard, `Clear()` runs without `skipClear`, causing the same full ~10K cell repaint that `lazyScreen` prevents.
- **PTY-forwarded keystrokes must skip `screen.Clear()` via `lazyScreen`.** tview calls `screen.Clear()` → `CellBuffer.Fill(' ', StyleDefault)` on every draw, changing `currStr` for all cells. When widgets redraw identical content, `Put()` sees `cl != c.currStr` and calls `setDirty(true)` (sets `lastStr=""`). Then `Show()` → `drawCell()` finds all cells dirty → full terminal I/O (~10K cells per keystroke). The `lazyScreen` wrapper skips `Clear()` when `skipClear` is set, so cells retain previous values, widgets write identical content, tcell sees no changes, and `Show()` becomes a no-op.
- **`RingBuffer.total` is `atomic.Uint64` — the only lock-free field.** All other RingBuffer fields (`data`, `pos`, `full`) require the caller's mutex. `TotalWritten()` is safe to call without any lock. `Session.TotalWritten()` and `RemoteSession.TotalWritten()` are lock-free.
- **`readLoop` data alias (`tmp[:n]`) requires all writers to consume synchronously.** Avoids a per-read heap allocation. Safe because `buf.Write`, `logFile.Write`, and all `io.Writer.Write` implementations copy or consume before returning. A future async writer that stores a reference to `p` would silently corrupt data.
- **`refreshPreview` TotalWritten guard fields must be protected by `a.mu`.** `lastPreviewTaskID` and `lastPreviewTW` are accessed from both the tick goroutine and `onTaskCursorChange` goroutines concurrently.
- **Never use `len(raw)` as a cache key for ring buffer content.** Once the ring buffer fills (256KB), `len(Bytes())` is constant — same length but different content on every wrap. Use `TotalWritten()` for change detection instead.

### Paste & Input Batching

- **`tapp.EnablePaste(true)` is required for fast paste.** Without it, tview delivers paste as thousands of individual `EventKey` events, each triggering a full screen redraw. With it, tview buffers all pasted text and delivers it as a single `PasteHandler()` call with one redraw.
- **`EnablePaste`/`EnableMouse` must be called AFTER `SetScreen`.** tview's `EnablePaste` only calls `screen.EnablePaste()` when `a.screen != nil`. And `Run()` only auto-enables when it creates its own screen (`a.screen == nil`). If `SetScreen` is called before `Run`, and `EnablePaste` was called before `SetScreen`, the flag is stored but `screen.EnablePaste()` is never invoked.
- **Every custom widget with text input must implement `PasteHandler()`.** tview's paste path bypasses `InputCapture` entirely — it goes through the focus chain calling `PasteHandler()` on the focused primitive. If a widget only has `InputHandler()`, paste is silently dropped when `EnablePaste` is on.
- **TerminalPane paste must wrap text in bracket paste sequences.** Send `\x1b[200~` + text + `\x1b[201~` so the agent's readline treats it as a paste (no per-character echo/processing).

### UI Threading

- **Never run git commands synchronously on the UI thread.** Even fast commands take 50-500ms. Use background goroutines + `QueueUpdateDraw`.
- **Never call `GetInnerRect()` from the tick goroutine.** tview is not thread-safe. Store pending values under mutex in `Draw()`, read from tick goroutine.
- **`refreshTasks()` must not do RPC while holding `a.mu`.** Fetch `runningIDs` OUTSIDE the lock.
- **`TaskPreviewPanel.Draw()` must never call `runner.Get()` or create a VT emulator.** Pre-render in `RefreshOutput()` on tick goroutine; `Draw()` only paints cached cells.
- **Never run synchronous git commands on the tick goroutine.** Blocking the tick goroutine prevents `QueueUpdateDraw` from firing, freezing the UI. Use `go` + cooldown (e.g., `lastTaskGitRefresh` with 3s interval). The agent view already follows this pattern — the task list must too.

### Sandbox (macOS sandbox-exec)

- **sandbox-exec uses SBPL profiles, not JSON.** `GenerateSandboxConfig()` returns `(profilePath, params, cleanup, err)`.
- **SBPL symlink trap:** Deny rules on symlinked paths don't work — kernel resolves symlinks first. Never test with `/tmp` paths (symlink to `/private/tmp`). Use `$HOME`-relative paths.
- **Must allow writes to:** `~/.claude.json`, `~/.claude/` (or Claude hangs silently), `/var/folders` (macOS temp dirs), and the main repo's `.git` dir (for worktree git operations — `resolveGitDir()` handles this automatically).
- **No domain-level network filtering.** sandbox-exec works at socket/address level. Argus uses `(allow network*)`.
- **Per-project sandbox config:** 3 columns on `projects` table. `ResolveSandboxConfig()` merges global + per-project.
- **Verify profile in effect:** Read the temp `.sb` file logged in `~/.argus/daemon.log`.

### Key Bindings & Navigation

- **`ctrl+c` only exits from task list view.** In agent mode, writes `0x03` to PTY (or no-op if dead).
- **`ctrl+q` in diff mode must exit diff AND refocus terminal.** Otherwise user needs a second keypress.
- **`ctrl+d` exits agent view when session is dead.** Without this, Ctrl+D after agent exit is silently dropped.
- **`ctrl+p` opens PR URL (works while agent runs).** `o` also works when session is finished.
- **Escape in agent view:** Refocuses terminal from diff/files but does NOT exit agent view. Always returns `nil` to consume the event.
- **Mouse clicks must update `agentFocus`, not just tview focus.** Custom `MouseHandler` overrides needed.
- **In diff mode: Up/Down switch files, j/k scroll diff.**
- **Cmd+Up/Down navigates between tasks in agent view** via `ModCtrl|ModAlt` check.
- **tcell has no `KeyCtrlLeft`/`KeyCtrlRight`.** Check `event.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0`.

### Database Patterns

- **New columns use `ALTER TABLE ... ADD COLUMN ... DEFAULT ''` after `CREATE TABLE IF NOT EXISTS`.** Error for duplicate column silently ignored.
- **`taskColumns` is the canonical column list.** Update `taskColumns`, `scanTask`, `Add`, and `Update` in lockstep.
- **Backend default config must be self-healing.** `fixupBackends()` runs on every `Open()` to repair outdated configs. Any `DefaultConfig()` change must be mirrored there. The `--permission-mode plan` fixup **appends** to the existing command (preserving user customizations) rather than replacing. All Claude fixup checks use `name == "claude"` (not `strings.Contains(command, "claude")`) to avoid matching user-created backends.
- **Map lookups returning `*T` become non-nil interfaces.** `Get()` must check `if sess == nil { return nil }` before returning as interface.

### Go Patterns

- **Use `charmbracelet/x/term` for raw mode** (cross-platform). `TIOCGETA` is macOS-only, `TCGETS` is Linux-only.
- **`ansi.StringWidth` returns 0 for tabs.** Expand tabs before any width math.
- **Use `ansi.Hardwrap` not `ansi.Truncate` for wrappable content.** Cache wrapped lines; invalidate on content or width change.
- **Chroma resets after every token.** Use `injectBg(s, bgEsc)` to re-apply background after each `\033[0m`.
- **Keep daemon client test names short.** macOS Unix socket paths have 104-byte limit.
- **`filepath.Walk` must return error when root is inaccessible.** Check `err != nil && path == root`.
- **CRITICAL: Tests must NEVER operate on real `~/.argus/` paths.** All worktree paths and file operations in tests MUST use `t.TempDir()`. The `testGuard` in `internal/tui2/worktree.go` is a last-resort safety net, but tests should be designed correctly.

### Codex Integration

- **`codex resume --last` is unreliable for multi-session.** Use `codex resume --dangerously-bypass-approvals-and-sandbox <session-id>`.
- **Session ID captured post-exit from `~/.codex/state_5.sqlite`.** The `_5` suffix is codex's schema version.
- **`fixupBackends()` migrates old codex flags** (`--yolo`, `--full-auto`) to `--dangerously-bypass-approvals-and-sandbox`.

### Knowledge Base & MCP

- **FTS5 doesn't support UPDATE.** Upsert = DELETE+INSERT in transaction.
- **FTS5 `SanitizeQuery` must strip all operators:** `" * ( ) : ^ { } - +`.
- **FTS5 + metadata JOIN avoids N+1 under mutex.** Never issue per-row `QueryRow` inside `rows.Next()` while holding `d.mu`.
- **MCP server echoes client's `protocolVersion`** — Codex workaround.
- **All config file writes should be atomic** (temp + rename).
- **KB Indexer started/stopped by daemon.** Start after MCP, stop before MCP shutdown.

### Todo-Task Association

- **`TodoPath` links a task to its source vault `.md` file.** Set only during `handleLaunchToDoKey`. `TasksByTodoPath()` returns most-recent task per path (ORDER BY created_at ASC, last wins).
- **Ctrl+R cleanup on ToDos tab deletes vault files, not tasks.** Only `.md` files for todos with completed linked tasks are removed. Tasks remain in Argus history.
- **`taskColumns`/`scanTask`/`Add`/`Update` lockstep includes `todo_path`.** Column position is after `pr_url`, before `archived`. Missing any site causes runtime panics.

### PR & Reviews

- **PR URL detection: scan on tick + on agent exit.** Last regex match wins. Use `RecentOutputTail(32KB)`, not full buffer.
- **`gh search prs --json` doesn't support `reviewDecision`.** Use `gh pr list --json` per-repo.
- **`SetPRs` must sort review requests before "my PRs"** — visual order must match slice order.
- **PR list has 10min cooldown.** `SetPRs` preserves cursor/selection on background refresh.

### Task List & UI

- **`rowTask` is `iota` (zero value) — never use `rowKind != 0` as a sentinel.** Use a boolean `hasPrev` flag when checking whether a previous row exists. The zero-value trap silently skips the intended code path for the most common row kind.
- **`SetTasks` must preserve cursor position across rebuilds.** Save the current row before `buildRows()`, then call `restoreCursor()` after. Without this, status changes via `s`/`S` keys appear to not work because the cursor jumps to a different row on refresh.
- **`restoreCursor` must filter by archive section.** Cross-section project name collisions cause cursor jumps.
- **`buildRows()` separates tasks by `t.Archived` before grouping.** Projects with only archived tasks never appear in main section.
- **Task-list previews must render the latest visible emulator lines, not `CellAt(x,y)` from row 0.** For Codex and any long-running PTY output, useful content often lives in scrollback or lower rows; replay logic must trim to bottom-of-history like `TerminalPane.paintEmu`.
- **Stopped agent → `StatusInReview`, not Pending.** Stopped means "needs human review".
- **Idle+unvisited tasks visually promoted to InReview.** Cleared on entering agent view.
- **Enter on completed task is a no-op.**
- **Daemon process appears as "argusd"** via symlink in `AutoStart`.
- **Task rename is display-only.** Worktree dir and branch unchanged. Use `db.Rename(id, name)` (not `db.Update`) — the modal captures a task pointer at open time, and a background `refreshTasksAsync` can replace `a.tasks` while the modal is open, orphaning the pointer. `db.Update` on the stale pointer overwrites concurrent field changes (e.g., agent exit setting status=Complete).
- **`ensureCursorVisible` must reset scrollOffset when all lines fit.** Check `totalLines <= visibleLines` → reset to 0.
- **Tab indices shifted when `TabToDos` was added between Tasks and Reviews.** Numeric keys are now 1=Tasks, 2=ToDos, 3=Reviews, 4=Settings. All statusbar hints and test assertions must match.
- **Fork task `executeFork` must run worktree creation + context extraction in a background goroutine.** Git diff and session log reads are I/O that blocks the UI thread. The `QueueUpdateDraw` callback handles DB persistence and session start on the tview thread — same race-avoidance pattern as new task creation (use `refreshTasksLocal`, not `refreshTasksAsync`).
- **Task list filter must bypass global rune key handling in `handleGlobalKey`.** When `tasklist.Filtering()` is true, the `KeyRune` case must `break` before checking `q`/`1`-`4` shortcuts — otherwise typing those characters quits the app or switches tabs instead of appending to the filter.
- **`buildRows` must expand all projects when a filter is active.** Without `filterActive` check, filtered tasks in collapsed projects are invisible — the filter matches them but they're hidden behind a collapsed project header.
- **String slicing for backspace must use `utf8.DecodeLastRuneInString`, not `len()-1`.** `len()` counts bytes, not runes — slicing mid-rune corrupts multi-byte UTF-8 characters. Same applies to cursor column positioning: use `ansi.StringWidth()` for display width, not `len()`.
- **`drawTaskRow` cursor fill must not overwrite elapsed time.** The fill loop extends the highlight to the row edge, but elapsed time is drawn right-aligned first. Compute `elapsedCol` once and use it as the fill boundary — filling past it overwrites the duration indicator.

### Fork Context Capture

- **PTY session logs contain `\r` (carriage return) characters that must be normalized before filtering.** Claude Code uses `\r` to overwrite status indicators in-place. Without `\r→\n` normalization, multiple screen elements concatenate on one "line" and per-line noise filters fail to match.
- **PTY session logs contain `\u00a0` (non-breaking space) that must be normalized.** Claude Code uses NBSP in tool result formatting. Without normalization, `\s+` patterns may not match as expected.
- **Long terminal lines (>120 bytes) need inline noise stripping, not just per-line filtering.** VT cell rendering concatenates the content area, status bar, separators, and prompt onto a single line with whitespace padding. `cleanLongLine` removes these inline patterns before per-line `isNoiseLine` runs.

### Vault Watcher & Remote API

- **Vault watcher and API server cannot import `daemon` package (circular import).** Both use `HeadlessCreateTask` from `daemon`, but `daemon` imports them. Fix: inject a `TaskCreator` function via closure at daemon wiring time, breaking the cycle.
- **iCloud-synced vault files need debounce (500ms) before reading.** Files arrive partially written; `fsnotify.Create` fires before the full content lands. Also skip `.icloud` placeholder files (iCloud uses these for files not yet downloaded).
- **Headless task creation uses default 24x80 PTY dimensions.** Agents format initial output for the PTY size at launch. The TUI resizes the PTY when a user opens the agent view, so headless tasks auto-correct on attach.
- **API server binds to `0.0.0.0` (not `127.0.0.1`) for Tailscale access.** MCP server uses `127.0.0.1` (local-only), but the API must be reachable over Tailscale's network interface. Auth is via bearer token from `~/.argus/api-token`.
- **`HeadlessCreateTask` must revert task to Pending on `runner.Start` failure.** Clear SessionID and zero StartedAt — same revert pattern as `startSession` in `app.go`.

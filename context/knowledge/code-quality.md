# Code Quality Patterns

## Refactoring Session: 2026-03-14

### Key Duplication Patterns Found
- **Config key-value mapping** duplicated 3x across db.go and migrate.go — fixed with table-driven field mapping
- **Modal rendering** duplicated 3x across confirm dialogs — fixed with `renderCenteredModal` helper
- **SQL column scan** duplicated 5x — fixed with `scanTask`/`taskColumns` helpers
- **Modal width calc** duplicated in NewTaskForm and NewProjectForm — fixed with shared `clampModalWidth`
- **Project detection** tables duplicated between DetectIcon and DetectLanguage — merged into single `signatures` table

### Concurrency Bug Found
- `DB.Config()` released mutex before iterating rows cursor, allowing concurrent writes during iteration. Fixed by holding lock through full iteration.

### Performance Optimization
- `ringBuffer.Write` was byte-at-a-time loop; replaced with bulk `copy()` for the 256KB buffer.

### Structural Split
- Infrastructure functions (worktree discovery, process cleanup, git operations) extracted from root.go (1250 lines) into worktree.go, reducing root.go to ~1100 lines.

### Incremental vt10x Rendering (2026-03-14)
- Agent view was replaying the entire 256KB ring buffer through a fresh vt10x terminal every 100ms tick. Each keystroke echo invalidated the render cache, causing progressively worse input lag as buffer grew.
- Fixed by persisting a `vt10x.Terminal` on `AgentView` and feeding only new bytes (delta from `TotalWritten`). Full replay is now only used for scrollback mode.
- Reset triggers: task switch, terminal resize, ring buffer wrap (when delta exceeds buffer capacity).

### Polish Refactoring Session: 2026-03-14 (PR #90)
- **ScrollState extraction**: Shared cursor/scroll logic extracted from TaskList/ProjectList/FileExplorer into `scrollstate.go` — 3 identical CursorUp/CursorDown/visibleRows implementations → 1
- **VT10X rendering**: Shared terminal rendering (renderLine, buildSGR, sgrColor, stripANSI) extracted into `vtrender.go` with `replayVT10X()` helper. Preview uses it for full replay; AgentView uses it for scrollback mode (incremental path kept separate)
- **fgColor/bgColor → sgrColor**: Merged two near-identical functions into parameterized `sgrColor(c, base)` where base=30 for FG, base=40 for BG
- **File splits**: root.go views → root_views.go (1107→797 lines), key byte maps → keybytes.go, git commands → gitcmd.go
- **Confirm handler dedup**: handleConfirmDeleteKey/handleConfirmDestroyKey → shared `handleConfirmAction(msg, cleanup func)`
- **determinePostExitStatus**: Pure function extracted from handleAgentFinished for testability
- **borderedPanel helper**: Extracted repeated lipgloss border construction into `borderedPanel(w, h, focused, content)`
- **Idiom fixes**: `errors.Is(err, sql.ErrNoRows)` replacing `==` and string comparison; `io.Discard` replacing dead stderr buffer; named constants for terminal sizes and refresh intervals
- Net: -738 lines across 23 files, 3-reviewer unanimous APPROVE

### Value-vs-Pointer Bug in GitStatus (2026-03-14)
- `gitstatus` was stored as a value type `GitStatus` on `Model`. The `scheduleGitRefresh()` method (value receiver) called `m.gitstatus.SetTask(t.ID)`, but the mutation was silently lost because it modified a copy.
- When `GitStatusRefreshMsg` arrived, `gitstatus.taskID` was still `""`, so `Update` dropped the message — result: "No worktree" in tasks view even when worktrees existed.
- Fixed by changing `gitstatus GitStatus` to `gitstatus *GitStatus`, matching the existing `agentview *AgentView` pattern.
- **Rule:** Any sub-view struct mutated outside the direct `Update` body must be a pointer. Value types only work for read-only or directly-mutated-in-Update fields.

### Collapsible Project Folders in TaskList (2026-03-14)
- Flat task list replaced with grouped project folders. Tasks grouped by `task.Project` into a flattened `[]row` slice where each row is either `rowProject` (header) or `rowTask`.
- Only one project expanded at a time. `autoExpand()` called on every cursor move — if the cursor enters a different project, it sets `expanded`, rebuilds rows, and `restoreCursor()` repositions to the same logical item.
- `Selected()` on a project header returns the first task in that project (next row). Preserves the `*model.Task` return type contract so root.go needed zero changes.
- Projects sorted by activity tier (in-progress > pending > complete), alphabetical within tier, "Uncategorized" last.
- `SetFilter()` must reset `expanded` if the expanded project disappears from filtered results — otherwise the first visible project stays collapsed.
- `buildRows()` must reset `expanded` if the expanded project no longer exists in any group (e.g. all its tasks were pruned). Without this, the auto-expand-first-project logic (`if expanded == ""`) never fires, leaving all remaining projects collapsed and the screen appearing empty until a cursor move triggers `autoExpand()`.
- `ScrollState` gained `SetCursor(int)` and `SetOffset(int)` for cursor repositioning after row list rebuilds.
- Existing root_test.go tests needed updates: tasks must have a `Project` field set to control grouping, and cursor-down count must account for project header rows.

### Cursor Skip-Header Navigation (2026-03-14)
- Cursor in task list now skips project header rows entirely via `moveCursor(dir int)`. The cursor always lands on a `rowTask`, never a `rowProject`.
- Going down past the last task in a project: hits project header → autoExpand → `CursorDown` one more to first task.
- Going up from first task in a project: hits own project header → goes up to previous project header → autoExpand (expands it) → scans forward for last `rowTask` in that project.
- Edge case: at row 0 (top project header), restores cursor to previous position (stays on first task).
- `skipToFirstTask()` called from `SetTasks()` and `SetFilter()` so the cursor starts on a task after any row rebuild.
- Tests updated: `TestModel_CursorNavigation` no longer expects a "down to reach first task" step. New tests: `TestTaskList_CursorSkipsProjectHeaders` (exhaustive up/down scan), `TestTaskList_CursorUpAcrossProjects` (verifies landing on last task of previous project).

### Alt Modifier Bug in keyMsgToBytes (2026-03-14)
- `keyMsgToBytes` only checked `msg.Alt` for runes (prepend ESC) and arrows (use `altArrowMap`). For all other keys in `keyByteMap` (Backspace, Delete, Home, End, etc.), the Alt flag was silently dropped.
- Result: Option+Delete (Alt+Backspace) sent plain `0x7f` instead of `\x1b\x7f`, breaking "delete word backward" in readline/zsh. Same issue for Alt+Delete (forward word delete) and any other Alt+special-key combo.
- Fix: After looking up `arrowMap` or `keyByteMap`, check `msg.Alt` and prepend `0x1b` if true. The `altArrowMap` path is unchanged (it uses dedicated CSI modifier sequences like `\x1b[1;3D`).
- **Pattern:** When adding new key types to `keyByteMap`, the Alt-prepend logic is automatic. But any new key maps (like `altArrowMap`) that use dedicated modifier sequences need their own `msg.Alt` check before the generic prepend path.

### New Task Modal: textinput → textarea (2026-03-14)
- Replaced `textinput.Model` (single-line, horizontal scroll) with `textarea.Model` (multi-line, word wrap) for the prompt field in the new task modal.
- Textarea starts at height 1 and auto-resizes up to `maxPromptLines` (10) based on visual line count after each `Update()`. Modal grows vertically to fit.
- Enter key submits the form (newline insertion disabled via `key.NewBinding(key.WithDisabled())` on `KeyMap.InsertNewline`).
- Up/down arrows in prompt field pass through to textarea for multi-line cursor navigation instead of switching fields.
- Tab/shift+tab still switch between project selector and prompt field.
- **Zero-value trap:** `textarea.Model` has internal pointers (`viewport`, `style`) that panic on `SetWidth`/`SetHeight` when the struct is zero-valued. Root model calls `newtask.SetSize()` on `WindowSizeMsg` before the form is opened (constructed). Fixed with a nil guard checking `f.projects == nil` (always non-nil when constructed via `NewNewTaskForm`, nil at zero value).
- **Soft-wrap line count trap:** `textarea.LineCount()` only counts hard newlines (`\n`). A long single line that soft-wraps to 3 visual lines still reports `LineCount() == 1`. Auto-resize must use a custom `visualLineCount()` that divides each hard line's rune length by the textarea width to compute actual visual lines. Without this, the modal stays at height 1 while wrapped text scrolls internally.

### Worktree Removal Safety Guard (2026-03-14)
- `removeWorktree()` had an unsafe `os.RemoveAll` fallback: if `git worktree remove --force` failed (e.g., path was the main working tree, not a real worktree), it would delete the entire directory — potentially the root project.
- Three call sites funneled through `removeWorktree`: task delete (`handleConfirmDeleteKey`), task destroy (`handleConfirmDestroyKey` via `removeWorktreeAndBranch`), and prune.
- Fixed by adding `isWorktreeSubdir()` which checks the path contains `/.argus/worktrees/` or `/.claude/worktrees/` (legacy) before allowing any removal operation. If the path isn't inside the expected worktree directory structure, `removeWorktree` is a no-op.
- **Pattern:** Any cleanup function that uses `os.RemoveAll` on a path derived from user data (stored in DB, passed as argument) must validate the path is within the expected directory hierarchy before deletion.

### Self-Managed Worktrees (2026-03-14)
- Argus now creates worktrees itself via `git worktree add` instead of delegating to Claude Code's `--worktree` flag. This makes worktree support backend-agnostic (works with Codex, any agent).
- Worktree location: `~/.argus/worktrees/<project>/<task>` (centralized). Branch naming: `argus/<task>`.
- `removeWorktree(path, repoDir)` requires the main repo dir because `~/.argus/worktrees/` is outside the git repo — git can't find repo metadata from there. Without repoDir, `git worktree remove` fails silently and falls through to `os.RemoveAll`, leaving stale entries in `.git/worktrees/`.
- Full cleanup on delete/destroy: worktree removal + local branch delete + remote branch delete. Both `handleConfirmDeleteKey` and `handleConfirmDestroyKey` now do identical cleanup.

### Scroll Offset Chicken-and-Egg Bug (2026-03-14)
- `scrollUp()` clamped `scrollOffset` to `maxScroll` computed from `cachedLines`. When `cachedLines` was empty (incremental render mode — the default during live agent output), `maxScroll` was 0, so `scrollOffset` was immediately clamped back to 0.
- But `cachedLines` is only populated by `formatTerminalOutput()`, which is only called when `scrollOffset > 0`. Result: mouse wheel scrolling had zero effect — the offset could never escape 0.
- Fix: skip the max clamp when `cachedLines` is empty. Let `scrollOffset` grow freely; the next `View()` sees `scrollOffset > 0`, calls `formatTerminalOutput`, populates `cachedLines`, and subsequent scrolls clamp correctly. The `windowLines()` function already handles over-scroll gracefully.
- **Pattern:** When state A gates computation B, and computation B produces the data needed to validate state A, don't validate A before B has run. Let A be temporarily "wrong" so B can bootstrap, then validate on the next cycle.

### Diff Panel Line Wrapping (2026-03-14)
- The diff viewer was using `ansi.Truncate()` to clip long lines to panel width. This silently hid content — long lines (e.g., markdown tables, long strings) went off-screen with no indication.
- Fixed by switching to `ansi.Hardwrap()` from the same `charmbracelet/x/ansi` package. Hardwrap inserts newlines at the width boundary while preserving ANSI escape sequences.
- Wrapped lines are cached in `diffWrappedLines` with `diffWrapWidth` tracking the width used. Cache invalidated on: new diff load (`UpdateFileDiff`), exit diff mode (`exitDiffMode`), or width change (detected in `wrapDiffLines`).
- Scrolling (`diffScrollUp`/`diffScrollDown`) operates on wrapped visual lines, not source lines. `diffScrollDown` falls back to `diffLines` length if `diffWrappedLines` hasn't been computed yet.
- **Future:** Side-by-side diff view and syntax highlighting are planned. Will require parsing structured diffs (e.g., `go-gitdiff` or `sourcegraph/go-diff`) and per-language highlighting (e.g., `chroma`).

### Textarea Viewport Scroll Bug in Auto-Resize (2026-03-14)
- When the textarea's prompt input wraps to a new visual line, the first line disappears. The modal only grows after the 2nd wrap.
- Root cause chain: `textarea.Update()` calls `repositionView()` at the end (line 1087 of bubbles textarea.go). With viewport height=1, the cursor on visual line 2 causes `repositionView()` to scroll down (`YOffset=1`). Then `SetHeight(2)` expands `viewport.Height` but does NOT reset `YOffset`. Result: viewport shows lines 1-2 instead of 0-1, hiding the first line.
- Additional factor: the textarea's `wrap()` function (line 1445) uses `>=` instead of `>` for width comparison, so text that exactly fills the width wraps to the next line.
- Fix: Call `SetHeight(maxPromptLines)` BEFORE `textarea.Update()` so `repositionView()` has enough headroom (max=9 instead of max=0) and doesn't scroll. Then shrink back to the actual visual line count afterward.
- **Pattern:** For any auto-resizing textarea, always expand height before `Update()` and shrink after. `SetHeight()` never calls `repositionView()`, so the viewport scroll offset can become stale.

### Unicode Width Mismatch — ⌘ Symbol (2026-03-14)
- `⌘` (U+2318, PLACE OF INTEREST SIGN) renders as 2 cells in most terminal emulators (iTerm2, Ghostty, Terminal.app) but `go-runewidth` v0.0.19 reports `RuneWidth('⌘') == 1`.
- Lipgloss uses `go-runewidth` for `Width()`, so any layout math using `lipgloss.Width()` on strings containing `⌘` underestimates by 1 per occurrence.
- Fix: add `strings.Count(s, "⌘")` to the computed width. Applied in `renderStatusBar()` for the `right` hints string.
- **Pattern:** When adding Unicode symbols to TUI layouts, verify `runewidth.RuneWidth(r)` against actual terminal rendering. Common offenders: miscellaneous symbols block (U+2300–U+23FF), dingbats, and emoji.

### Tmux-Matched Tab Header (2026-03-14)
- Tab header restyled to blend with the user's tmux status bar. Colors sourced from `~/.dots/cmd/tmux-status/color/root.go`.
- **Key color mapping:** base background `colour236` (tmux C3), active tab `fg=236 bg=103` (tmux C1 — purple/lavender), inactive text `colour244` (tmux C3 fg).
- **Powerline separators:** `\ue0b0` (right-facing full chevron) for smooth active tab transitions. Defined in `~/.dots/cmd/tmux-status/separator/root.go`.
- **Pattern:** When styling Argus UI elements that sit adjacent to tmux chrome, use the tmux C1/C2/C3 palette to maintain visual continuity. The color constants are in `~/.dots/cmd/tmux-status/color/root.go`.

### Zero-Dimension View() Panic (2026-03-15)
- Bubble Tea calls `View()` before delivering the first `WindowSizeMsg`. At this point `m.width` and `m.height` are both 0.
- `renderTasksView` computed `contentHeight := m.height - 3` (= -3) and passed it to `padHeight()`, which did `lines[:h]` — panic on negative slice bound.
- Introduced by `bc55e7d` ("Add three-panel layout to task list view") which added `padHeight` calls without the guard that `padToBottom` already had.
- The zero-dimension test (`TestModel_ViewZeroDimensions`) also caught two latent panics: `NewTaskForm.View()` (textarea nil pointer at zero value) and `NewProjectForm.View()` (empty inputs slice at zero value).
- **Fix pattern:** Every function receiving a computed height/width must guard `<= 0` at the top. Every `View()` on a form struct must guard against zero-valued state (uninitialized by constructor).
- **Prevention:** `TestModel_ViewZeroDimensions` covers all 10 view paths with `width=0, height=0`. New views must add a subtest.

### vt10x Cursor Reverse-Bit Fix (2026-03-15)
- Cursor rendering used `cell.Mode | vtAttrReverse` (OR) which double-reversed already-reverse cells, making cursor invisible on them. Separately, replacing default colors with hardcoded black/white (colors 0/15) produced a black cursor on dark terminals instead of inheriting the terminal's theme.
- Fixed with `cell.Mode ^ vtAttrReverse` (XOR) — toggles reverse for both normal and reverse cells. No explicit colors needed; SGR reverse with defaults inherits the terminal's fg/bg, producing the expected white cursor on dark backgrounds.
- **Pattern:** When vt10x stores pre-swapped attributes, always toggle (XOR) rather than set (OR) to avoid double-application.

### vt10x CursorVisible Gate Removal (2026-03-16)
- Despite correct `\x1b[0;7m` cursor rendering logic, cursor was never visible because both `renderIncremental` and `replayVT10X` gated cursor rendering on `vt.CursorVisible()`.
- Claude Code (built with Ink) hides the hardware cursor (`\x1b[?25l`) — standard for TUI apps. vt10x correctly tracks this, so `CursorVisible()` returned `false`, and `cursorX` was always `-1` (no cursor rendered on any line).
- Fixed by removing the `CursorVisible()` check in both paths — cursor position is always passed to `renderLine()`.
- **Pattern:** When embedding a TUI app's output inside another TUI, ignore the child's cursor visibility state — the parent always wants to show cursor position. `CursorVisible()` is only meaningful when directly driving a physical terminal.

### Shared PanelLayout Extraction (2026-03-15)
- Agent view and task list view both had independent three-panel layout implementations with duplicated width-splitting logic. Agent used 60/20/20, task list used 30/40/30, with different compression strategies.
- Extracted `PanelLayout` struct to `panellayout.go`: configurable per-panel percentage + minimum width, right-to-left compression, remainder absorption for rounding, `Render()` handles `padHeight` + `JoinHorizontal`.
- Both views now use identical 20/60/20 ratios for visual consistency. The `padHeight` utility was also moved here from `agentview.go` since it's shared.
- Sub-views (`gitstatus`, `fileexplorer`, `taskdetail`) own their own borders via `borderedPanel()` — the layout struct does NOT wrap content in borders. `renderTerminal`/`renderDiffPanel` in agent view build their own borders inline.
- **Pattern:** When extracting shared layout, don't try to unify border rendering if sub-views already manage their own borders. The shared layer should only handle geometry (widths, heights, padding, joining).

### Worktree-First Task Creation Regression Fix (2026-03-15)
- Commit `58a6789` ("Self-managed worktrees") introduced a regression: `CreateWorktree` errors were silently swallowed in `startOrAttach`, so failed worktree creation fell through to running agents in the main project directory.
- Compounding bug: `ResolveTaskDirMsg` handler persisted the project directory path as `t.Worktree` in the DB (no validation). On restart, `startOrAttach` saw `t.Worktree != ""` and skipped worktree creation — permanently stuck.
- Fix: moved worktree creation from `startOrAttach` to `handleNewTaskKey`, BEFORE `db.Add()`. If creation fails, the task form stays open with the error message (new `SetError()` method on `NewTaskForm`). Task is never persisted without a valid worktree.
- `CreateWorktree` now returns `(wtPath, finalName, err)` and handles name conflicts by appending `-1`, `-2`, ... `-99` suffixes.
- `ResolveTaskDirMsg` handler now guards with `isWorktreeSubdir()` before persisting `msg.Dir` as `t.Worktree`.
- `BuildCmd` no longer falls back to `ResolveDir()` when `Worktree` is empty — every task must have a worktree. As of 2026-03-16, `BuildCmd` returns a hard error (`"task %q has no worktree set"`) when `Worktree` is empty.
- Defense-in-depth enforcement (2026-03-16): worktree requirement is now checked at four layers: (a) task creation (`CreateWorktree` before `db.Add`), (b) `Init()` resume path (revert to Pending if no worktree found), (c) `startOrAttach()` early guard with user-visible error, (d) `BuildCmd` hard error return. Each layer catches independently.
- `Init()` revert for worktree-less tasks: clears `SessionID`, `StartedAt`, and sets `StatusPending`. This differs from `DaemonRestartedMsg` (which preserves `SessionID`) because a missing worktree means the session cannot run at all.
- **Pattern:** Infrastructure prerequisites (worktree, branch) must be validated BEFORE persisting a record. Silent error swallowing on infrastructure setup creates subtle state corruption that compounds with async handlers.

### Remote Branch Resolution for Worktrees (2026-03-16)
- `git worktree add -b argus/task <path> master` fails with `fatal: not a valid object name: 'master'` when the repo has no local `master` branch (only `origin/master` or `upstream/master`). Common in fork-based workflows where users work on feature branches.
- Fix: `resolveStartPoint()` in `worktree.go` checks `git rev-parse --verify` on the configured branch. If it doesn't exist locally, tries `upstream/<branch>` then `origin/<branch>` as fallbacks (upstream preferred for fork workflows).
- New project form auto-detects remote default branch when user enters a repo path (via `git symbolic-ref refs/remotes/<remote>/HEAD` or `git ls-remote --symref`). Pre-fills with full ref like `upstream/master` so new projects store explicit remote refs.
- Auto-detection only overwrites the branch field if it's still at a generic default (`master`, `main`, or empty) — preserves user customization.
- **Pattern:** `git worktree add` start points must be fully resolved refs. Never assume a bare branch name like `master` exists locally — always validate with `rev-parse --verify` and fall back to remote-tracking refs.

### PanelLayout Width Enforcement Bug (2026-03-15)
- `PanelLayout.Render()` only pads height via `padHeight()` — it does NOT enforce column widths on panels.
- The task list view's left pane was rendering as raw text without `borderedPanel`, so it collapsed to content width instead of filling its 20% allocation.
- Fix: wrapped task list content in `borderedPanel(widths[0], contentHeight, false, ...)` in `renderTasksView()`, and adjusted `tasklist.SetSize()` to subtract 2 from each dimension for the border.
- **Pattern:** Every panel passed to `PanelLayout.Render()` must enforce its own width. `borderedPanel` does this internally (`Width(w-2)` + border = `w` total). Panels without borders need explicit `lipgloss.NewStyle().Width(w)`.

### Daemon Architecture Implementation (2026-03-15)
- **SessionProvider/SessionHandle interfaces** (`iface.go`): Decouples UI from concrete `*Runner`/`*Session`. UI code depends only on interfaces, enabling both in-process and daemon-backed implementations.
- **Multi-writer pattern** (`session.go`): Replaced single `attachW io.Writer` with `writers []io.Writer` slice. `readLoop` copies slice under lock, iterates outside lock. Failed writers auto-removed. `AddWriter()` sends replay BEFORE registering the writer to avoid duplicate bytes (replay-then-register, not register-then-replay). `Attach()`/`Detach()` use AddWriter/RemoveWriter internally. `AddWriter`/`RemoveWriter` are on the `SessionHandle` interface so daemon stream handler doesn't need type assertions.
- **Nil-interface gotcha**: `Runner.Get()` returns `SessionHandle` (interface). Map lookups on missing keys return `nil *Session`, which becomes a non-nil interface. Fixed with explicit nil check before returning.
- **RingBuffer exported** (`RingBuffer`/`NewRingBuffer`): Used by both in-process sessions and daemon client's local buffer.
- **Daemon IPC**: Unix socket with first-byte dispatch ('R' = JSON-RPC, 'S' = raw stream). `net/rpc/jsonrpc` codec for structured calls. Raw byte streaming for PTY output with ring buffer replay.
- **Client `SessionProvider`**: `RemoteSession` has a local `RingBuffer` populated by a stream reader goroutine. RPC calls for WriteInput/Resize/SessionStatus. `Done()` channel closed on stream EOF.
- **ExitInfo pattern**: Daemon caches `ExitInfo{Err, Stopped, LastOutput}` in `onFinish` callback. Client calls `Daemon.GetExitInfo` RPC (consume-once) when stream closes, then passes real values to `AgentFinishedMsg`. Without this, daemon mode silently marks crashed/stopped sessions as successful completions because `Err`/`Stopped` default to zero values.
- **onFinish ordering**: Runner's exit goroutine must fire `onFinish` BEFORE deleting the session from `r.sessions`. Otherwise there's a race: client's `connectStream` gets EOF → `removeSession` calls `GetExitInfo` RPC → but daemon's `onFinish` hasn't cached the info yet (session was deleted from runner before callback ran). The callback runs OUTSIDE `r.mu` to avoid deadlocking if it re-enters the runner. Two separate lock sections: first reads+clears `stopped`, second deletes session.
- **RPC timeout wrapper**: Go's `net/rpc.Client.Call()` has no timeout. When daemon dies, every `refreshTasks()` tick hangs the TUI indefinitely. Fixed with `c.call()` wrapper: goroutine + `select` + `time.After(5s)`. Buffered channel (`make(chan error, 1)`) prevents goroutine leak on timeout. All 12 `c.rpc.Call` sites replaced. `time.After` allocates a timer per call — acceptable for current call frequency, but `time.NewTimer` + `Stop()` would be cleaner for hot paths like `WriteInput`.
- **Daemon file logging**: Daemon runs detached (`Setsid: true`) with no terminal. Log output goes to `~/.argus/daemon.log` via `log.SetOutput(logFile)`. Must `os.MkdirAll(db.DataDir())` before `os.OpenFile` because the data dir may not exist on fresh install if daemon starts before TUI.
- **Test gotcha**: `db.OpenInMemory()` seeds the default "claude" backend. Tests that create sessions with custom backends must set `task.Backend` explicitly — otherwise `ResolveBackend` falls through to the default claude backend and launches a real Claude Code process.

### Chroma Background Color Compositing Fix (2026-03-15)
- Syntax-highlighted diff lines (added/removed) lost their red/green background color after the first token. Only the first word of each line had the correct background.
- Root cause: Chroma's `writeToken()` emits `\033[0m` (full SGR reset) after every token — by design, so pagers can render lines independently. When the highlighted string was wrapped with `lipgloss.Style.Render()` (which sets background at start, resets at end), the first internal `\033[0m` from chroma cleared the background for all subsequent tokens.
- Chroma has no option to preserve an outer background — `clearBackground()` in the formatter intentionally strips style background colors. The `terminal256` and `terminal16m` formatters both use the same `writeToken()` with full resets.
- Fix: `injectBg(s, bgEsc)` — prepends the background escape, replaces all `\033[0m` with `\033[0m<bgEsc>`, and appends `\033[0m`. Applied in `formatSideContent` (side-by-side) and `RenderUnifiedLines` (unified).
- Replaced `removedBgStyle`/`addedBgStyle` (lipgloss.Style) with raw escape strings (`removedBgEsc`/`addedBgEsc`) since lipgloss `Render()` can't handle this pattern.
- **Pattern:** When compositing ANSI backgrounds with syntax-highlighted text from chroma (or any formatter that resets between tokens), use `injectBg` to re-apply the background after each reset. Do NOT use lipgloss `.Render()` wrapping — it only sets the background once at the start.

### Tab Characters Break Width Math (2026-03-15)
- `ansi.StringWidth("\t")` returns **0** — tabs are zero-width in charmbracelet's width calculations (`ansi.StringWidth`, `ansi.Truncate`, `lipgloss.Width`). Terminals render them as 1-8 columns.
- This caused the side-by-side diff divider (`│`) to shift position between rows: lines with tabs got too much padding (width underestimated), lines without tabs were correct.
- **Fix:** `expandTabs()` in `diffparse.go` converts tabs to 2 spaces during parsing, before any width calculation or rendering.
- **Pattern:** Any UI panel that renders external text (diff content, file previews, terminal output) must expand tabs to spaces before computing widths. The `vt10x` terminal emulator handles its own tab stops, so this only applies to non-vt10x rendering paths.

### Deferred Items for Future Sessions
- Add error handling for silently ignored `_ = m.db.Update()` calls (~15 instances in root.go)
- Handle `os.UserHomeDir()` errors in db.go and config.go
- Remove dead `store` package
- Define interface for DB to improve testability (Runner interfaces done — `SessionProvider`/`SessionHandle` in `iface.go`)
- Add dedicated tests for ScrollState, borderedPanel, determinePostExitStatus (currently covered transitively)
- Goroutine leak in Session.Attach stdin copy (needs cancellation mechanism)
- Document Detect() ordering constraint in project/detect.go to prevent future signature reordering regressions
- Improve `internal/daemon` test coverage from 45% to ≥80% (missing: stream handler, WriteInput/Resize RPCs, error paths, concurrent stream/RPC, session exit notification)
- Improve `internal/daemon/client` test coverage to ≥80% (Get() race + StreamLost + DaemonDown tests added 2026-03-16; remaining: stream reconnection on live process, concurrent stream/RPC paths)
- Daemon session resume on startup: daemon should resume in-progress tasks with saved session IDs (port Init() logic from root.go)

## Sandbox Architecture (2026-03-15)

### Design Decisions
- **Tool choice:** `@anthropic-ai/sandbox-runtime` (srt) over raw Seatbelt or nono — battle-tested (powers Claude Code itself), cross-platform (macOS Seatbelt + Linux bubblewrap), simple CLI wrapper
- **Injection point:** `BuildCmd()` wraps the shell command string with `srt --settings <tempfile> -- <original>`. PTY, daemon, attach/detach unchanged.
- **Opt-in:** `cfg.Sandbox.Enabled` defaults to `false`. Toggle via Enter/Space on the sandbox row in settings.
- **Availability detection:** `IsSandboxAvailable()` is cached via `sync.Once` — first call probes `exec.LookPath("srt")` then `npx --no @anthropic-ai/sandbox-runtime --version` with 5s timeout. All subsequent calls return the cached result. Called unconditionally in `refreshSettings()` so the settings view always shows accurate install status. `ResetSandboxCache()` clears the cache after toggle so re-detection occurs.
- **Cleanup lifecycle:** `BuildCmd` returns `(cmd, cleanup, error)`. Cleanup called on `StartSession` failure OR on `session.Done()` in the exit-watch goroutine. No double-free, no leak.

### Key Bugs Caught in Review
1. **Triple-slash path:** `"//" + "/absolute/path"` → `"///absolute/path"`. Fix: `"//" + strings.TrimPrefix(path, "/")` in `normalizeSrtPath`.
2. **npx --yes at startup:** Original implementation used `npx --yes` (auto-installs packages). Fix: use `--no` flag, add 5s timeout, cache via `sync.Once`. Now called unconditionally in `refreshSettings()` since caching makes repeated calls free.
3. **Tests validated buggy format:** Test expected values matched the triple-slash bug, so tests passed despite incorrect behavior. Lesson: always validate test expectations against the external spec, not just internal consistency.
4. **Missing `allowPty: true`:** srt blocks PTY operations by default on macOS (`allowPty` defaults to `false` in `SandboxRuntimeConfigSchema`). Since Argus runs agents via PTY, the sandbox silently blocked terminal I/O — agent view showed "Waiting for output..." indefinitely. Fix: set `AllowPty: true` in `srtSettings` struct. Lesson: when integrating srt, read the full Zod schema in `sandbox-config.js` for security-default fields that need explicit opt-in.
5. **Settings cursor navigation off-by-one in tests:** Two sandbox config form tests used 2 `CursorDown()` calls instead of 3 (forgot UX logs row between daemon logs and sandbox). One panicked on `inputs[0]` access; the other silently tested a no-op path. Lesson: always assert cursor position before acting on it in settings tests.

### Config Persistence
- Sandbox config stored as `sandbox.enabled`, `sandbox.allowed_domains`, `sandbox.deny_read`, `sandbox.extra_write` in the `config` KV table
- List values stored as CSV (comma-separated). Known limitation: paths with commas would break.
- `SetSandboxEnabled(bool)` convenience method on DB; other values via `SetConfigValue`

## Daemon Restart Feature (2026-03-15)

### CLI Subcommands
- `argus daemon start` (also bare `argus daemon`) — starts daemon in foreground
- `argus daemon stop` — idempotent: prints "no daemon running" if not running (exit 0)
- `argus daemon restart` — stop + wait for socket cleanup + start in foreground
- `stopDaemon()` returns `(bool, error)` — the bool distinguishes "stopped" from "not running"

### TUI Restart Flow
- Settings tab → `r` key → `viewDaemonRestart` modal → `restartDaemonCmd()` (goroutine) → `DaemonRestartedMsg`
- `daemonRestarting` flag suppresses `refreshTasks()` and `scheduleGitRefresh()` during restart to avoid RPC timeouts against dead socket
- Handler swaps `m.runner`, `m.preview.runner`, `m.agentview.runner`, resets in-progress tasks to Pending
- **SessionID preserved on restart** — Claude Code's `--session-id` persists conversation state to disk. The handler clears `AgentPID` and `StartedAt` but keeps `SessionID` so re-launching uses `--resume` to continue the conversation

### Double-Pointer Pattern for Shared State
- `program **tea.Program` and `restartedClient **dclient.Client` use double-pointer indirection
- **Critical:** Must be allocated in `NewModel` (`new(*tea.Program)`) so the outer pointer exists before `tea.NewProgram` copies the model
- `SetProgram(p)` writes through: `*m.program = p` — all BT value copies share the same inner slot
- `RestartedClient()` getter lets `runTUI` close the post-restart client on exit

### AutoStart Extraction
- `autoStartDaemon` moved from `cmd/argus/main.go` to `dclient.AutoStart()` for reuse by TUI restart
- `daemonSysProcAttr` platform files moved from `cmd/argus/` to `internal/daemon/client/`
- `WaitForShutdown(sockPath, timeout)` polls until socket file disappears

### Stream Failure ≠ Process Exit Bug (2026-03-16)
- Tasks were being auto-completed when the TUI's stream connection to the daemon dropped, even though the agent processes were still running on the daemon side.
- Root cause: `connectStream` (stream.go) calls `removeSession` on any stream error/EOF. `removeSession` calls `Daemon.GetExitInfo` RPC — but if the process is still alive, `exitInfos[taskID]` doesn't exist (only populated by `onFinish`), so `GetExitInfo` returns empty `ExitInfo{Err: "", Stopped: false}`. The TUI's `onSessionExit` callback fires `AgentFinishedMsg{Err: nil, Stopped: false}`, and `determinePostExitStatus` sees a clean exit after >3 seconds → `StatusComplete`.
- **Fix (PR #155):** `connectStream` refactored into `streamOnce` + retry loop. On stream EOF/error, calls `Daemon.SessionStatus` to check if the process is still alive. If alive, retries stream connection up to `maxStreamRetries` (3) with 500ms backoff. Only calls `removeSession` when the process has actually exited. Introduced three residual bugs fixed in the next round (see below).
- Daemon logs showed no restarts — confirming this is a TUI-side issue, not a daemon issue.
- **Test gotcha:** Unix socket paths on macOS have a 104-byte limit. Test names that include `t.TempDir()` can exceed this — keep test names short (e.g., `TestAlive_Dead` not `TestIsSessionAlive_DeadSession`). Symptom: `connect: invalid argument` error on `net.Dial("unix", ...)`.

### Residual Stream/Daemon Connectivity Fixes (2026-03-16)

Three bugs remained after PR #155:

**1. Retry exhaustion auto-completed tasks.** After 3 failed stream retries, `connectStream` called `removeSession` → `GetExitInfo` returned empty (process still alive) → task marked `Complete`. Fix: on retry exhaustion, call `removeSessionStreamLost` instead of `removeSession`. This fires `AgentFinishedMsg{StreamLost: true}`, and `handleAgentFinished` returns early keeping the task `InProgress`.

**2. Daemon crash auto-completed tasks.** `isSessionAlive()` returned `false` on RPC failure (daemon unreachable) → `streamOnce` returned `processExited=true` → auto-complete. Fix: `isSessionAlive()` now returns `(alive bool, daemonReachable bool)`. When `daemonReachable=false`, `streamOnce` returns `(false, true)` (daemonDown). `connectStream` routes to `removeSessionStreamLost` on daemon down — can't confirm process exit, so keep `InProgress`.

**3. `client.Get()` race created ghost `RemoteSession`.** During the narrow window between `onFinish()` firing and `delete(r.sessions, taskID)` in the runner, `SessionStatus` returns `{Alive: false, PID: non-zero}`. The original condition `!info.Alive && info.PID == 0` failed → `Get()` created a new `RemoteSession` with its own `connectStream` goroutine → second `AgentFinishedMsg` for the same task. Fix: use `!info.Alive` alone — PID is irrelevant for `Get()`.

**`StreamLost` flag** added to both `daemon.ExitInfo` and `ui.AgentFinishedMsg`. `handleAgentFinished` short-circuits on `StreamLost=true`: logs, sets status bar error ("stream lost for task X — press Enter to reconnect"), calls `refreshTasks()`, returns without touching task status.

**Daemon health check** added to TickMsg handler: type-asserts `m.runner` to `*dclient.Client` and calls `Ping()` each tick. Three consecutive failures (`m.daemonFailures >= 3`) trigger `restartDaemonCmd()`. `daemonFailures` resets on successful ping or `DaemonRestartedMsg`.


### Daemon Cleanup Race & Zombie Prevention (2026-03-16)

Three bugs discovered in daemon lifecycle management:

1. **Zombie daemons**: `Shutdown()` ran on a goroutine (signal/RPC handler). After closing `d.done` and the listener, `Serve()` returned on the main goroutine → `main()` exited → Shutdown goroutine killed mid-cleanup. `StopAll()` never completed. Old daemons stayed alive, blocked in `Accept()` on a deleted socket inode — unreachable but consuming resources. 11 zombie daemons observed.

2. **Socket theft**: Old daemon's `Shutdown()` unconditionally called `os.Remove(DefaultSocketPath())`. If the old daemon was slow to die, it could delete the new daemon's socket file.

3. **SIGTERM swallowed**: After Shutdown via RPC, the signal handler goroutine exited (saw `d.done`), but `signal.Notify` was still active. Go caught subsequent SIGTERMs into the buffered `sigCh` channel that nobody read. `killExistingDaemon`'s SIGTERM was silently ignored → 2s timeout → SIGKILL escalation every time.

**Fixes:**
- `cleanup()` runs on Serve's goroutine (main goroutine), not Shutdown's. `Shutdown()` only signals (closes `d.done` + listener). Serve's accept-loop exit path calls `d.cleanup()` which does `StopAll` + `removeIfOwnedByPID`. This ensures cleanup completes before `main()` returns.
- `signal.Stop(sigCh)` called after signal handler goroutine exits, restoring default SIGTERM behavior so `killExistingDaemon` works.
- `killExistingDaemon(pidPath)` at start of `Serve()` kills the PID-file daemon before binding.
- `removeIfOwnedByPID(sockPath, pidPath, ourPID)` checks PID file ownership before removing files.
- `sockPath` and `pidPath` stored on Daemon struct, derived from the `sockPath` parameter to `Serve()`. This prevents tests from touching `~/.argus/` — the PID path is `filepath.Dir(sockPath)/daemon.pid`, so temp dirs in tests stay isolated.

**Key invariant**: `killExistingDaemon` waits for the old daemon to die before returning, so the new daemon never writes its PID while the old daemon is alive. This makes the TOCTOU window in `removeIfOwnedByPID` unexploitable.

**Full flow documentation**: `context/research/daemon-lifecycle-flows.md`

### UX Debug Logging (2026-03-16)
- Added `internal/uxlog` package — file-based logger writing to `~/.argus/ux.log`, separate from daemon's `daemon.log`.
- Thread-safe (mutex-guarded), no-op if `Init()` not called, idempotent init.
- Log points cover: `startOrAttach` (entry/failure/success), `handleAgentFinished` (all msg fields + status decision), `handleSessionResumed`, `DaemonRestartedMsg`, `Init()` session resume, daemon client `Start`/`removeSession`/stream connect/disconnect, RPC timeouts.
- Viewable in Settings → UX Logs row (same modal viewer pattern as Daemon Logs).
- **Pattern:** When adding a new log viewer row to Settings, the `rebuildRows()` row order determines cursor navigation. Tests that navigate past log rows need `CursorDown()` calls for each new row.

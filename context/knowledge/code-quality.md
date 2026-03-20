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

### Incremental Terminal Rendering (2026-03-14)
- Agent view was replaying the entire 256KB ring buffer through a fresh terminal emulator every 100ms tick. Each keystroke echo invalidated the render cache, causing progressively worse input lag as buffer grew.
- Fixed by persisting a terminal emulator instance and feeding only new bytes (delta from `TotalWritten`). Full replay is now only used for scrollback mode.
- Reset triggers: task switch, terminal resize, ring buffer wrap (when delta exceeds buffer capacity).
- Now uses x/vt (`charmbracelet/x/vt`) with native scrollback buffer and damage tracking via `Touched()`.

### Polish Refactoring Session: 2026-03-14 (PR #90)
- **ScrollState extraction**: Shared cursor/scroll logic extracted from TaskList/ProjectList/FileExplorer into `scrollstate.go` — 3 identical CursorUp/CursorDown/visibleRows implementations → 1
- **VT10X rendering**: Shared terminal rendering (renderLine, buildSGR, sgrColor, stripANSI) extracted into `vtrender.go` with `replayVT10X()` helper. Preview uses it for full replay; AgentView uses it for scrollback mode (incremental path kept separate)
- **fgColor/bgColor → sgrColor**: Merged two near-identical functions into parameterized `sgrColor(c, base)` where base=30 for FG, base=40 for BG
- **File splits**: root.go views → root_views.go (1107→797 lines), key byte maps → keybytes.go, git commands → gitcmd.go
- **Confirm handler dedup**: handleConfirmDeleteKey/handleConfirmDestroyKey → shared `handleConfirmAction(msg, cleanup func)`
- **determinePostExitStatus**: Pure function extracted from handleAgentFinished for testability
- **borderedPanel helper**: Extracted repeated border construction into `borderedPanel(w, h, focused, content)`
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
- Any layout math using `go-runewidth` on strings containing `⌘` underestimates by 1 per occurrence.
- **Pattern:** When adding Unicode symbols to TUI layouts, verify `runewidth.RuneWidth(r)` against actual terminal rendering. Common offenders: miscellaneous symbols block (U+2300–U+23FF), dingbats, and emoji.

### Tmux-Matched Tab Header (2026-03-14)
- Tab header restyled to blend with the user's tmux status bar. Colors sourced from `~/.dots/cmd/tmux-status/color/root.go`.
- **Key color mapping:** base background `colour236` (tmux C3), active tab `fg=236 bg=103` (tmux C1 — purple/lavender), inactive text `colour244` (tmux C3 fg).
- **Powerline separators:** `\ue0b0` (right-facing full chevron) for smooth active tab transitions. Defined in `~/.dots/cmd/tmux-status/separator/root.go`.
- **Pattern:** When styling Argus UI elements that sit adjacent to tmux chrome, use the tmux C1/C2/C3 palette to maintain visual continuity. The color constants are in `~/.dots/cmd/tmux-status/color/root.go`.

### Zero-Dimension Rendering Panic (2026-03-15)
- Layout code can be called before the terminal size is known. At this point width and height are both 0.
- Height computations like `height - 3` produce negative values — passing to slice expressions or layout functions causes panics.
- **Fix pattern:** Every function receiving a computed height/width must guard `<= 0` at the top. Every render path on a form/view struct must guard against zero-valued state (uninitialized by constructor).

### Cursor Rendering: Always Show Regardless of CursorVisible (2026-03-16)
- TUI agents like Claude Code (built with Ink) hide the hardware cursor (`\x1b[?25l`) — standard for TUI apps. The terminal emulator correctly tracks this, so `CursorVisible()` returns `false`.
- When embedding a TUI app's output inside another TUI (as Argus does), gating cursor rendering on `CursorVisible()` makes the cursor invisible.
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
- `CreateWorktree` now returns `(wtPath, finalName, branchName, err)` and handles name conflicts by appending `-1`, `-2`, ... `-99` suffixes. The `branchName` return (e.g., `"argus/fix-bug"`) must be stored on `task.Branch` — previously the branch was not returned, so `task.Branch` retained the base branch (e.g., `master`), causing `removeWorktreeAndBranch` to delete the wrong branch.
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
- **Pattern:** Every panel in a multi-panel layout must enforce its own width. `borderedPanel` does this internally. Panels without borders need explicit width enforcement.

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
- Root cause: Chroma's `writeToken()` emits `\033[0m` (full SGR reset) after every token — by design, so pagers can render lines independently. Setting a background color only applies to the first token — subsequent tokens lose it after the first `\033[0m` reset.
- Chroma has no option to preserve an outer background — `clearBackground()` in the formatter intentionally strips style background colors. The `terminal256` and `terminal16m` formatters both use the same `writeToken()` with full resets.
- Fix: `injectBg(s, bgEsc)` — prepends the background escape, replaces all `\033[0m` with `\033[0m<bgEsc>`, and appends `\033[0m`. Applied in `formatSideContent` (side-by-side) and `RenderUnifiedLines` (unified).
- Uses raw escape strings (`removedBgEsc`/`addedBgEsc`) to re-inject the background color.
- **Pattern:** When compositing ANSI backgrounds with syntax-highlighted text from chroma (or any formatter that resets between tokens), use `injectBg` to re-apply the background after each reset.

### Tab Characters Break Width Math (2026-03-15)
- `ansi.StringWidth("\t")` returns **0** — tabs are zero-width in charmbracelet's width calculations (`ansi.StringWidth`, `ansi.Truncate`). Terminals render them as 1-8 columns.
- This caused the side-by-side diff divider (`│`) to shift position between rows: lines with tabs got too much padding (width underestimated), lines without tabs were correct.
- **Fix:** `expandTabs()` in `diffparse.go` converts tabs to 2 spaces during parsing, before any width calculation or rendering.
- **Pattern:** Any UI panel that renders external text (diff content, file previews, terminal output) must expand tabs to spaces before computing widths. The x/vt terminal emulator handles its own tab stops, so this only applies to non-emulator rendering paths (diff views, file previews).

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

### UX Regression Fixes (2026-03-18)
- **Backend inheritance in new task form:** Removing the synthetic default/inherit option from the backend selector changed task semantics, not just presentation. Because `ResolveBackend` prefers `task.Backend` over `project.Backend`, preselecting and persisting the global default backend forced every new task to override project-level backend settings. Fixed by restoring an explicit `(inherit)` option that stores an empty task backend and rendering a resolved hint (`→ <backend>`) so users still see what will run.
- **Autocomplete refresh after async skill discovery:** The `/skill` dropdown is populated asynchronously via `skillsLoadedMsg`. If a user typed a slash command prefix before the scan finished, the dropdown stayed closed because only `m.newtask.skills` was updated. Fixed by immediately calling `m.newtask.updateAutocomplete()` in the `skillsLoadedMsg` handler so the current prompt is re-evaluated as soon as skill data arrives.

## Sandbox Architecture (2026-03-16, updated from srt to sandbox-exec)

### Design Decisions
- **Tool choice (current):** macOS `sandbox-exec` (`/usr/bin/sandbox-exec`) — always available, no install. Originally srt was chosen but found incompatible with argus's PTY-based sessions (PR #165). sandbox-exec is macOS-only (acceptable since argus is single-user macOS).
- **Injection point:** `BuildCmd()` wraps the shell command string: `sandbox-exec -D 'HOME=...' -D 'WORKTREE=...' -f /tmp/profile.sb sh -c 'original cmd'`. The double-sh (outer from `exec.Command("sh", "-c", ...)` + inner from `WrapWithSandbox`) is intentional — standard argus invocation pattern.
- **Opt-in:** `cfg.Sandbox.Enabled` defaults to `false`. Toggle via Enter on the sandbox row in settings.
- **Availability detection:** `IsSandboxAvailable()` is cached via `sync.Once` — checks `os.Stat("/usr/bin/sandbox-exec")` then `exec.LookPath` fallback. Instant (syscall only). Called unconditionally in `refreshSettings()`. `ResetSandboxCache()` clears for testing.
- **Cleanup lifecycle:** `BuildCmd` returns `(cmd, cleanup, error)`. Cleanup removes the temp `.sb` profile. Called on `StartSession` failure OR on `session.Done()` in the exit-watch goroutine. No double-free, no leak.

### SBPL Profile Gotchas
1. **`/tmp` symlink breaks deny rules.** macOS `/tmp` → `/private/tmp`. A deny rule like `(deny file-read* (subpath "/tmp/foo"))` does NOT block access to `/private/tmp/foo` because the kernel resolves the symlink before matching. Always use real paths in deny rules. The credential deny rules use `(string-append (param "HOME") "/.ssh")` where HOME = `/Users/username` — a real, non-symlinked path that works correctly.
2. **`(allow file-read*)` + `(deny file-read* (subpath X))` works correctly for real paths.** The broad allow does NOT override the subpath deny when using real paths. This was empirically verified. The pattern in argus's `sandboxProfileBase` is correct and effective for credential dir protection.
3. **No domain-level network filtering.** `(allow network*)` permits all outbound connections. srt used a proxy-based domain allowlist; sandbox-exec has no equivalent. This is an intentional tradeoff — write isolation and credential read protection are achieved; network egress is not restricted.
4. **Profile must allow `file-ioctl`.** Without this, PTY operations fail. Claude Code inherits the PTY fd before the sandbox is applied, so no `open()` on the PTY device is needed — but ioctl for terminal control still requires the rule.
5. **Profile must allow writes to `~/.claude.json` and `~/.claude/`.** Claude Code writes `~/.claude.json` (auth/session state) on every startup. Blocking this write causes a silent hang: the agent emits ~41 bytes of terminal init sequences (`\x1b[?25h\x1b[<u...`) then stops — no TUI renders, agent view appears blank with no output or error message. Also allow `(subpath "~/.claude")` for conversation history (needed for `--resume`). Rules: `(allow file-write* (literal (string-append (param "HOME") "/.claude.json")))` and `(allow file-write* (subpath (string-append (param "HOME") "/.claude")))`.
6. **Symlink write rules use resolved paths.** If `~/.claude/skills` is symlinked to `~/.dots`, the `(subpath "~/.claude")` write rule does NOT cover writes to resolved `~/.dots/...` paths (kernel resolves symlinks before matching). Reads are unaffected (global `(allow file-read*)` applies to resolved paths). Only add `~/.dots` to write rules if agents actually need to write there.

### Daemon Binary Staleness — Sandbox Changes Require Restart

**Rebuilding the binary does NOT update the running daemon.** The daemon loads the binary image at startup and runs it in memory indefinitely. `sandboxProfileBase` is a compiled-in Go constant — recompiling changes it on disk but leaves the daemon's in-memory copy unchanged. Any tasks started after a rebuild still use the OLD profile.

**When daemon restart is required:**
- Any change to `sandboxProfileBase` in `internal/agent/sandbox.go`
- Any change to `GenerateSandboxConfig()` or `WrapWithSandbox()` logic
- Any other change in the daemon's code path (not just sandbox — all daemon-side code)

**To restart:** `kill -TERM $(cat ~/.argus/daemon.pid)` — the TUI auto-restarts via `autoStartDaemon()`.

**To diagnose staleness:** Compare `ps -p $(cat ~/.argus/daemon.pid) -o lstart` (daemon start time) against `ls -la /path/to/argus` (binary mtime). If binary is newer, daemon is stale.

**To verify active profile:** The daemon logs the `.sb` path in `~/.argus/daemon.log` as `-f '/var/folders/.../T/argus-sandbox-NNNN.sb'`. While the sandboxed process is running, `cat <path>` shows the exact SBPL rules in effect. Compare against `sandboxProfileBase` to confirm match.

### Git Worktree .git Dir Write Access (2026-03-16)
- **Problem:** Git worktrees store metadata (index.lock, objects, refs) in the main repo's `.git/worktrees/<name>/` directory, not in the worktree itself. The sandbox allowed writes to `WORKTREE` but not to the main repo's `.git` dir, so `git add`, `git commit`, and `git push` all failed with `Operation not permitted`.
- **Fix:** `resolveGitDir(worktreePath)` reads the worktree's `.git` file (which is a file, not a directory, containing `gitdir: <path>`), walks up two levels from the gitdir path (`.git/worktrees/<name>` → `.git`), and returns the `.git` dir. `GenerateSandboxConfig()` calls this and adds `(allow file-write* (subpath "<gitdir>"))` to the profile. Falls back gracefully (no-op) for non-worktree dirs.
- **Key detail:** The gitdir path in the `.git` file can be relative — `resolveGitDir` handles both absolute and relative paths via `filepath.Join` + `filepath.Clean`.

### Config Persistence
- Sandbox config stored as `sandbox.enabled`, `sandbox.deny_read`, `sandbox.extra_write` in the `config` KV table
- `sandbox.allowed_domains` key was used by srt; now orphaned in existing DBs but harmlessly ignored
- List values stored as CSV (comma-separated). Known limitation: paths with commas would break.
- `SetSandboxEnabled(bool)` convenience method on DB; other values via `SetConfigValue`
- **`sandbox.extra_write` garbage causes broken SBPL rules.** Each CSV value becomes `(allow file-write* (subpath "..."))`. A partial entry like `"e"` produces `(allow file-write* (subpath "e"))` — valid syntax, no effect. Clear via: `sqlite3 ~/.argus/data.sql "UPDATE config SET value='' WHERE key='sandbox.extra_write'"`

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
- **Daemon process naming (2026-03-17):** `AutoStart` creates a symlink `~/.argus/argusd -> exe` and launches via the symlink so macOS Activity Monitor shows "argusd" instead of the generic binary name. Symlink is updated when `os.Executable()` differs from the current target. Falls back to direct exe path on symlink failure. Imports `internal/db` for `DataDir()`.

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

### Agent View File Explorer: Merge Committed + Uncommitted (2026-03-16)
- **Problem**: `UpdateGitStatus` used if/else: show uncommitted files (`git status --short`) if any, *else* show committed branch files (`git diff --name-status base..HEAD`). Caused blank right-panel whenever the working tree was momentarily clean between agent commits — committed changes existed but the else branch was never reached when uncommitted changes were absent.
- **Fix**: `MergeChangedFiles(base, overlay []ChangedFile)` in `fileexplorer.go` — merges two file lists, overlay wins on path conflict, result sorted by path. `UpdateGitStatus` calls `MergeChangedFiles(ParseGitDiffNameStatus(msg.BranchFiles), ParseGitStatus(msg.Status))` unconditionally.
- **Rule**: The files panel should always show ALL changes on the branch (committed + uncommitted). Never use if/else to choose one source over the other — always merge.

### textarea ColumnOffset vs Hard Line Position (2026-03-17)
- **Bug**: `textareaAbsCursorPos()` in `wordboundary.go` used `LineInfo().ColumnOffset` to compute the cursor's absolute rune position within the full textarea value. But `ColumnOffset` is relative to the current *visual row* (after soft wrapping), not the start of the hard line. On the second visual row of a wrapped line, `ColumnOffset` returned a small number (e.g., 2) instead of the true position (e.g., 32+2=34), causing word navigation to think the cursor was near position 0.
- **Fix**: Use `li.StartColumn + li.ColumnOffset`. `StartColumn` is the cumulative rune count of all visual rows before the current one. Their sum equals `m.col` — the true rune position within the hard line.
- **Root cause**: Existing tests used `SetWidth(200)` (no wrapping) so `ColumnOffset == StartColumn + ColumnOffset` trivially. Added `TestApplyWordNavTextarea_SoftWrap` with `SetWidth(30)` to exercise the wrapped path.
- **Rule**: When testing textarea cursor position logic, always include a narrow-width test case that forces soft wrapping. `ColumnOffset` and `CharOffset` in `LineInfo()` are visual-row-relative, not hard-line-relative.

### PR URL Detection & Task Association (2026-03-17)

**Feature**: When an agent runs `gh pr create`, the resulting PR URL is automatically detected, persisted to the task, and openable with `'o'` from the task list.

**Data model**: `Task.PRURL string` + `pr_url TEXT NOT NULL DEFAULT ''` column on the `tasks` table. Column added with `ALTER TABLE tasks ADD COLUMN pr_url TEXT NOT NULL DEFAULT ''` (error silently ignored) to cover existing databases — same pattern as sandbox columns on `projects`.

**Detection flow**:
1. `TickMsg` handler calls `runner.Running()`, then for each session: `prURLRe.FindAllString(string(sess.RecentOutputTail(32*1024)), -1)`. Only scans the last 32KB to avoid copying the entire ring buffer every tick. Takes `matches[len(matches)-1]` (latest match wins — handles agent opening PR #1, then PR #2).
2. Guard `t.PRURL != url` prevents redundant DB writes when URL hasn't changed.
3. On match: fires `PRDetectedMsg{TaskID, URL}` as a `tea.Cmd`.
4. `PRDetectedMsg` handler: re-checks `t.PRURL != msg.URL` (idempotent), updates DB, refreshes task list.

**Fast-exit edge case**: `runner.Running()` excludes sessions that have already exited. An agent that creates a PR and exits within 1 second would be missed by the tick scan. Fix: `handleAgentFinished` also scans `msg.LastOutput` when `t.PRURL == ""`, updating `t.PRURL` in-place before `db.Update(t)`.

**Regex**: `https://github\.com/[a-zA-Z0-9_.\-]+/[a-zA-Z0-9_.\-]+/pull/\d+`. Works for both plain URLs and OSC 8 hyperlinks (where the URL appears twice as target + display text; the last match is the display text).

**Key binding**: `'o'` in `handleTaskListKey` — `exec.Command("open", url).Start()` wrapped in a `tea.Cmd` (must not block Update). Not in KeyMap; uses `msg.String() == "o"` (consistent with agent view's inline switch pattern).

**Display**: `TaskDetail.View()` shows `PR: <url>` when `t.PRURL != ""`, truncated to fit panel width.

**Rule**: Always scan `LastOutput` in `handleAgentFinished` for any feature that detects content in session output — the tick scan only covers actively-running sessions.

### Reviews Tab: 2026-03-17

**Feature**: Three-panel GitHub PR review interface (`internal/ui/reviews.go` + `internal/github/github.go`). Left panel shows PR list (review requests + my PRs), center shows unified diff, right shows comments and compose box.

**gh CLI field gotcha**: `gh search prs --json` uses the GitHub Search API which returns `SearchResultItem` — a limited field set that does NOT include `reviewDecision`. Available search fields: `number`, `title`, `author`, `isDraft`, `repository`, `updatedAt`, `url`, `state`, `labels`, `body`, `commentsCount`, `createdAt`, `closedAt`, `id`, `isLocked`, `isPullRequest`, `authorAssociation`, `assignees`. In contrast, `gh pr list --json` and `gh pr view --json` both use the GraphQL `PullRequest` type which DOES support `reviewDecision`.

**Fix**: `FetchPRList()` fetches the cross-repo list via `gh search prs` (without `reviewDecision`), then `enrichReviewDecisions()` groups PRs by repo and calls `gh pr list -R owner/repo --json number,reviewDecision` per unique repo — O(repos) not O(PRs). Failures are silently ignored (badges just won't show).

**Logging rule added**: All async message handlers in root.go for the reviews tab now use `uxlog.Log("[reviews] ...")` for both success and error paths. This was missing at launch, making failures invisible. New `### Logging Requirements` section added to CLAUDE.md Development Rules to enforce this for all future features.

### Task Rename Feature (2026-03-17)

**Data model**: Rename is display-only — only updates `Name` on the existing `Task` struct. Branch and worktree directory are unchanged.

**Flow**: 'r' key in task list → `viewRenameTask` → `RenameTaskForm` (single `textinput.Model` pre-filled with current name) → on submit: update `t.Name` in DB → return to task list. Works even while an agent is running since no filesystem or git state is touched.

**Key binding**: `'r'` in `handleTaskListKey` via `msg.String() == "r"`. Also added to `KeyMap.Rename` for help display. Does not conflict with `RestartDaemon` ('r' in settings tab) since they're in different tab handlers.

## Task Archive Feature: 2026-03-17

**Data model**: `Task.Archived bool` persisted as `archived INTEGER NOT NULL DEFAULT 0` in SQLite. Standard ALTER TABLE migration pattern.

**Flow**: Press `'a'` on task list → toggles `t.Archived` → `db.Update(t)` → `refreshTasks()`. Archive section appears at the bottom of the task list when any archived tasks exist.

**UI structure**: `buildRows()` separates `filtered` tasks into `activeTasks` / `archivedTasks` by `t.Archived`. Active tasks build the main project groups. Archived tasks build a separate "Archive" section: `rowArchiveHeader` row followed by project sub-groups (only when `archiveExpanded == true`). The archive has its own project expansion state (`archiveProject` field, independent of `expanded`).

**Navigation**: `rowArchiveHeader` is a navigable row — cursor can land on it (unlike project headers which are skipped). Enter on the archive header toggles `archiveExpanded`. `isInArchiveSection(idx)` walks backward to detect if a row is after the archive header — used by `autoExpand()` to dispatch to `archiveProject` vs `expanded`, and by `renderProjectHeader()` to pick the correct chevron state.

**Key entities**: `rowArchiveHeader`, `archiveExpanded`, `archiveProject`, `ToggleArchive()`, `CursorOnArchiveHeader()`, `isInArchiveSection()`, `groupByProject()` (extracted helper), `projectTasksFiltered()`.

### Codex Backend Support: 2026-03-17 (updated 2026-03-17)

**Feature**: Full Codex CLI support as an Argus backend, with session-ID-based resume, backend selector in new task form, and default backend management in settings.

**Data model**: `Backend.ResumeCommand` field was removed. Resume behavior is encoded in argus via `IsCodexBackend`. The `resume_command` DB column remains for backward compat but is never read/written. Both Claude and Codex now use `task.SessionID` — codex's ID is captured post-exit (see below).

**Codex CLI differences from Claude Code**:
- Auto-approve: `--dangerously-bypass-approvals-and-sandbox` (replaces deprecated `--full-auto`/`--yolo`)
- Resume: `codex resume [SESSION_ID]` — a subcommand, not a `--resume` flag. `--last` picks globally most recent session (NOT cwd-filtered) — unreliable for multi-session argus. Always use `<session-id>` explicitly.
- No `--session-id` equivalent — cannot pin a session ID at new-session start. ID is captured from `~/.codex/state_5.sqlite` after exit.
- Model selection: `-m <MODEL>` (e.g., `-m o3`).

**Backend detection** (`IsCodexBackend` in `agent.go`): `filepath.Base(firstWord) == "codex"` — handles absolute paths (`/usr/local/bin/codex`) correctly. Bare name `codex` also matches. `my-codex-wrapper` does NOT match.

**Resume logic** (`BuildCmd` in `agent.go`):
- If `resume && IsCodexBackend`: `codexResumeCmd + " " + shellQuote(task.SessionID)` (constant: `codex resume --dangerously-bypass-approvals-and-sandbox`)
- If `resume && !IsCodexBackend`: append `--resume <sessionID>` to base command (Claude)
- If `!resume && !IsCodexBackend`: append `--session-id <sessionID>` (Claude only)
- If `!resume && IsCodexBackend`: no session pinning (codex)

**Resume signal** (`startOrAttach` in `root.go`): `resume = t.SessionID != ""` for both backends. Codex tasks start with empty SessionID (set after first exit); Claude generates SessionID upfront.

**Codex session ID capture** (`CaptureCodexSessionID` in `agent.go`): After clean codex exit, `handleAgentFinished` fires a `tea.Cmd` → queries `SELECT id FROM threads WHERE cwd=? ORDER BY updated_at DESC LIMIT 1` from `~/.codex/state_5.sqlite` (constant `codexStateDB`; `_5` is codex schema version). Result dispatched as `CodexSessionCapturedMsg` → `handleCodexSessionCaptured` stores in `task.SessionID`. IDs validated as UUID regex before storage and before use in command construction. Known TOCTOU: if a new codex session starts in the same worktree before capture completes, the new session's ID may be stored against the old task.

**fixupBackends**: Migrates old codex flags (`--yolo`, `--full-auto`) to `--dangerously-bypass-approvals-and-sandbox`. Scoped to `name == "codex"` — users who renamed their codex backend must update manually.

**New task form**: Three fields — project → backend → prompt. Backend selector shows sorted backend names only (no `(default)` entry). The configured default backend is pre-selected by name; `Task().Backend` is always an explicit backend name.

**Settings**: BACKENDS section header shows `(default: <name>)`. Backend detail panel shows `★ Default backend`. Keys: `[e]` edit, `[n]` new, `[d]` set as default.

**Backend form** (`backendform.go`): 3 textinput fields (Name, Command, Prompt Flag). `ResumeCommand` field removed — resume behavior is encoded in argus.

## Cursor Navigation Refactor: 2026-03-17

### Bug: Cursor stuck on archive header when pressing up from first archived task

**Root cause:** `moveCursor` had a project-header skip path (going up) that called `CursorUp()` to skip the project header but didn't handle landing on an `rowArchiveHeader`. The cursor got parked on the archive header — a non-task row. The next press of up then hit the archive header handler which properly exited.

**Fix:** Extracted two helpers from `moveCursor`:
- `skipUpPastHeader(prev int)` — moves up past any header (project or archive), chaining through consecutive headers. Handles the project→archive header sequence in one call.
- `landOnLastTask(idx, prev int)` — finds the last consecutive task row after a project header at `idx`, sets cursor + adjusts scroll offset. Falls back to `prev` if no tasks follow.

These replaced 4 duplicate instances of the "find last task in project + set cursor + scroll adjust" pattern.

**Archive header indent:** Added 2-space prefix to "Archive" label in `renderArchiveHeader` for visual alignment with project headers.

## File Explorer Auto-Expand: 2026-03-17

### Pattern: Auto-expand directories on cursor movement

**What:** File explorer directories auto-expand when cursor enters them and collapse when cursor leaves, matching the task list's one-expanded-at-a-time pattern. Replaced the manual Enter-to-toggle behavior.

**Data flow:** `CursorUp()`/`CursorDown()` → `autoExpand()` → returns dir path needing fetch (or `""`) → agent view issues `fetchDirChildren()` as `tea.Cmd`.

**Key gotcha — cursor position shift on row rebuild:** When collapsing a directory, child rows are removed and all subsequent row indices shift up. `autoExpand()` saves `cursorPath` before rebuild, then finds the same path in the new rows and calls `SetCursor(i)`. Without this, the cursor drifts to the wrong row after any collapse.

**`parentDir()` helper:** Walks backward from a child row index to find the parent directory row (indent 0, IsDir true). Used when cursor is on a child to determine which directory should stay expanded.

## Reviews Tab: PR List Sort Order Bug — 2026-03-17

**Problem:** `FetchPRList()` appends "my PRs" (`IsReviewRequest: false`) before "review requests" (`IsReviewRequest: true`) in the slice. But `renderPRList()` visually renders "Review Requests" first and "My Open PRs" second. Since `prCursor` navigates the flat slice sequentially, the cursor started in the "My Open PRs" section (at the bottom) and couldn't reach "Review Requests" (at the top) without scrolling through all "My PRs" first.

**Root cause:** Visual render order and data order were decoupled — `renderPRList` separates PRs into two groups and renders review requests first, but the cursor index is against the unsorted flat slice.

**Fix:** `SetPRs()` now calls `sort.SliceStable` to sort review requests to the front of the slice before storing it. This makes the flat cursor index order match the visual top-to-bottom render order. Test: `TestReviewsView_SetPRs_SortOrder`.

**Invariant:** Any view that renders items in a different order than the backing slice must either (a) sort the slice to match, or (b) use an indirection layer (display index → data index). Option (a) is simpler when the sort is stable and the grouping is fixed.

## Reviews Tab Caching: 2026-03-17

### Pattern: Background refresh with cached data display

**What:** PR list has a 10-minute cooldown (`prListCooldown`). Tab entry (all three paths: `"2"`, TabLeft, TabRight) checks `canFetchPRList()` before fetching. When cached data exists, the UI shows it during background refresh with a dimmed "refreshing…" indicator instead of replacing with "Loading PRs...".

**Key design decisions:**
- `SetPRs()` distinguishes first-load (resets all state) from background refresh (preserves cursor, scroll offset, selection, files, diff, comments). `hadData` flag checks `len(rv.prs) > 0 || rv.selectedPR != nil`.
- Both `prCursor` and `prScrollOff` are clamped when the list shrinks on refresh.
- `View()` only shows "Loading PRs..." / error when no cached data exists. Background errors appear as dimmed "refresh failed: ..." appended to the cached list.
- `'R'` key forces manual refresh subject to the same cooldown, showing remaining seconds if blocked.

## Knowledge Base + Obsidian Integration: 2026-03-18

### Architecture

**Packages**: `internal/kb/` (types, search sanitization, indexer, document parsing), `internal/mcp/` (HTTP MCP server), `internal/inject/` (Claude MCP config), `internal/inject/codex/` (Codex TOML config). `internal/kb/` NEVER imports `internal/db/` — circular import. Only `db` imports `kb`.

**Two-vault design**: "Metis" vault (KB indexing, `kb.metis_vault_path`) and "Argus" vault (task creation, `kb.argus_vault_path`). Default paths resolve to iCloud Obsidian: `~/Library/Mobile Documents/iCloud~md~obsidian/Documents/<VaultName>`. Settings panel has 4 rows: KB enabled toggle, Metis vault path, Argus vault path, task sync toggle. All default OFF (`KBConfig.Enabled = false`).

**FTS5 storage**: `kb_documents` virtual FTS5 table (path, title, body, tags, tier), `kb_metadata` regular table (modified_at, ingested_at, word_count, tier as integers). Upsert = DELETE+INSERT in transaction (FTS5 doesn't support UPDATE). `kb_pending_tasks` table for Obsidian-sourced tasks awaiting approval.

**MCP server** (`internal/mcp/server.go`): Streamable HTTP JSON-RPC 2.0 on `POST /mcp`. Named `argus-kb` in server info. Port 7742 default, auto-increments to 7750 on conflict. Four tools: `kb_search`, `kb_read`, `kb_list`, `kb_ingest`. Codex workaround: echoes back client's `protocolVersion` rather than asserting `2024-11-05` (Codex v0.47 sends `2025-06-18` and requires echo).

**Daemon wiring**: MCP server starts in `Serve()` when `cfg.KB.Enabled`. KB Indexer starts in a goroutine after MCP is up. Both shut down in `cleanup()` — `d.kbIndexer.Stop()` then `d.mcpServer.Shutdown(ctx)` with 5-second timeout.

### Key Patterns & Gotchas

**FTS5 `SanitizeQuery` must strip all FTS5 operators**: `" * ( ) : ^ { } - +`. Missing `-` (NOT operator) or `+` (proximity) allows injection of FTS5 query syntax into the index. Full set in `internal/kb/search.go`.

**FTS5 + metadata JOIN — no N+1**: `KBSearch` uses `LEFT JOIN kb_metadata km ON km.path = kb_documents.path` with `COALESCE(km.modified_at, 0)` — all metadata fetched in one query. Never issue a nested `d.conn.QueryRow` inside a `rows.Next()` loop while the mutex is held (deadlock risk if the nested query also needs the mutex).

**MCP server Shutdown method**: `Server` stores `httpSrv *http.Server`. `Shutdown(ctx context.Context) error` calls `s.httpSrv.Shutdown(ctx)`. Daemon stores `mcpServer *mcp.Server` and calls it in `cleanup()` with a 5-second context timeout.

**Atomic config writes**: `injectCodexTOML` (and `inject/claude.go`) writes via `os.CreateTemp` + `os.Rename` — never `os.WriteFile` directly. Prevents partial reads if the process crashes mid-write.

**Explicit configKey parameter**: `NewKBVaultForm(theme, vaultName, configKey, currentPath)` accepts the DB config key explicitly (e.g., `"kb.metis_vault_path"`). Never derive configKey from a human-readable label string — fragile to localization or wording changes.

**`filepath.Walk` vault root error propagation**: When `err != nil && path == idx.vaultPath`, return the error (vault root inaccessible). For sub-paths, `return nil` (skip). Without this, `FullScan` returns `nil` when the vault directory doesn't exist — silently does nothing instead of reporting the misconfiguration.

**`path/filepath.IsAbs` + `strings.Contains(path, "..")` path validation**: The `kb_ingest` MCP tool validates incoming paths before calling `KBUpsert`. Absolute paths and paths with `..` components are rejected. This prevents agents from injecting arbitrary paths into the FTS5 index.

### Config Keys
- `kb.enabled` — `"true"` / `""` (default `""` = off)
- `kb.http_port` — integer string (default `"7742"`)
- `kb.metis_vault_path` — Obsidian vault path for KB indexing
- `kb.argus_vault_path` — Obsidian vault path for task creation
- `kb.auto_create_tasks` — `"true"` / `""` (default off)

### Deferred Items
- Phase 5: fsnotify watcher in `kb.Indexer.watch()` (currently placeholder goroutine)
- Phase 6: Obsidian → task creation (parser exists in `internal/import/obsidian.go`, UI approval flow not wired)
- Settings: `'v'` key to edit vault path uses `KBVaultForm` modal; actual DB write happens in root.go `viewKBVaultPath` handler

## Terminal Passthrough Phase 2: tcell/tview App Shell (2026-03-18)

### Data Model & Flow
- `internal/tui2/app.go` — `App` struct owns `tview.Application`, `*db.DB`, `agent.SessionProvider`, all sub-views. `New()` builds the widget tree, `Run()` starts the event loop + tick goroutine.
- `internal/tui2/header.go` — `Header` (tab bar): custom `tview.Box` widget, `SetTab(t)` / `ActiveTab()`.
- `internal/tui2/statusbar.go` — `StatusBar`: task counts + keybinding hints, changes hints per active tab.
- `internal/tui2/tasklist.go` — `TaskListView`: flattened row model with `rowKind` (rowTask/rowProject/rowArchiveHeader), cursor navigation skipping headers, auto-expand, archive section.
- `internal/tui2/agentpane.go` — `AgentPane`: Phase 2 placeholder showing PTY tail output. Takes `agentview.TerminalAdapter` for session display.
- `internal/tui2/sidepanel.go` — `SidePanel`: bordered panel with title for git/files.
- `internal/tui2/theme.go` — tcell color constants for the 256-color palette.

### Key Patterns
- **Custom tview widgets** extend `tview.Box` and implement `Draw(screen tcell.Screen)` directly.
- **Async updates** via `tapp.QueueUpdateDraw()` from the tick goroutine.
- **Key routing** via `tapp.SetInputCapture()` — global handler dispatches by mode (taskList vs agent).
- **PTY key forwarding** via `tcellKeyToBytes(event)` — maps `tcell.EventKey` to raw bytes including alt modifier.
- **View switching** via `tview.Pages.SwitchToPage()` — mirrors BT's `current view` enum.

### Bordered Panel Consolidation (2026-03-18)

**Problem:** Border drawing was duplicated across 8+ panels with two inconsistent patterns — "border outside" (`drawBorder(x-1, y-1, w+2, h+2)`) used by agent view panels, and "border inside" (`drawBorder(x, y, w, h)` with manual `innerX/Y/W/H` computation) used by reviews and task list panels. Title rendering was also duplicated (4 lines each time).

**Fix:** `drawBorderedPanel(screen, x, y, w, h, title, style) innerRect` in `agentpane.go`. Draws border + optional title, returns `innerRect{X, Y, W, H}` for content area. All bordered panels (TaskListView, TaskDetailPanel, TaskPreviewPanel, GitPanel, TerminalPane, FilePanel, 3x ReviewsView sub-panels) now use it.

**All panels use inside borders:** Every panel passes `(x, y, w, h)` (border inside allocated rect) and uses the returned `innerRect` for content rendering. This ensures consistent rounded borders across all views.

**Rule:** Any new panel that needs a bordered frame should call `drawBorderedPanel`, not `drawBorder` directly. `drawBorder` remains as the low-level primitive.

### Gotchas
- `tview.Application.SetRoot()` must be called before `Run()` — the root Flex is built eagerly in `buildUI()`.
- `QueueUpdateDraw(func(){})` from the tick goroutine is the idiomatic way to trigger a redraw from a non-event goroutine. The empty func is intentional — state was already updated under `a.mu`.
- `tcellKeyToBytes` must handle `tcell.KeyBackspace` AND `tcell.KeyBackspace2` — different terminals send different variants.
- **tview `GetInnerRect()` is not thread-safe** — calling it from a non-main goroutine (e.g., tick goroutine) races with `Draw()` on the main goroutine. Use a pending-resize pattern: `Draw()` computes desired PTY size under mutex and stores it; the tick goroutine consumes and performs the RPC.
- **Never call daemon RPC while holding `a.mu`** — `runner.Running()` does an RPC with up to 5s timeout. Holding the mutex blocks all `QueueUpdateDraw` callbacks (including redraws) for the duration. Extract RPC calls outside the lock, then re-acquire for state mutation.
- **Daemon session exit callback must be wired for tui2 runtime.** Without `client.OnSessionExit()`, agent processes that finish are never detected — tasks stay `InProgress` forever. The callback must be registered before `tui2.New()` with a nil guard (`if a := appRef; a != nil`) to handle the initialization window.

## Task Delete & Prune: 2026-03-19

### Data Model
- `ConfirmDeleteModal` — tview Box widget with `confirmed`/`canceled` bools and a `*model.Task` reference.
- `modeConfirmDelete` — new `viewMode` constant, intercepts all keys in `handleGlobalKey`.
- Worktree helpers (`removeWorktreeAndBranch`, `isWorktreeSubdir`, etc.) ported to `internal/tui2/worktree.go`.

### Flow
- **Ctrl+D**: `handleGlobalKey` → `openConfirmDelete(task)` → shows modal via `pages.AddPage("confirmdelete", ...)` → Enter triggers `deleteTask(t)` → stop session, cleanup worktree/branch, delete session log, `db.Delete(id)`, refresh.
- **Ctrl+R**: `handleGlobalKey` → `pruneCompletedTasks()` → `db.PruneCompleted()` (atomic fetch+delete) → stop sessions → worktree cleanup in background goroutine → refresh.
- Both guarded by `a.mode == modeTaskList && a.header.ActiveTab() == TabTasks`.

### Gotchas
- Worktree cleanup is unconditional — worktree, local branch, and remote branch are always removed on task delete/prune. The old `ShouldCleanupWorktrees()` config gate was removed.
- `isWorktreeSubdir` safety check prevents `os.RemoveAll` on non-worktree paths.
- Prune runs worktree cleanup in a goroutine to keep TUI responsive; calls `QueueUpdateDraw` on completion.

## Mouse Focus & Diff File Navigation Fix: 2026-03-19

### Problem
Clicking on the Files panel in the agent view didn't switch keyboard focus — Up/Down arrows continued routing to the PTY instead of navigating files. tview's default `Box.MouseHandler` updates tview's internal focus but Argus uses `agentFocus` for key routing. Also, Up/Down in diff mode only scrolled the diff — no way to switch files.

### Fix
- Added `MouseHandler()` overrides to `FilePanel` and `TerminalPane` with `OnClick` callbacks
- `FilePanel.MouseHandler` also positions cursor on clicked row and handles scroll wheel
- `TerminalPane.MouseHandler` handles scroll wheel for scrollback
- Callbacks wired in `buildUI` to update `agentFocus` and `updateFocusIndicators()`
- Up/Down in diff mode now navigate to prev/next file's diff; j/k scroll the diff

### Data Model
- `FilePanel.OnClick func()` — callback fired on mouse click
- `TerminalPane.OnClick func()` — callback fired on mouse click

## Syntax Highlighting in Diff Views (2026-03-19)

### Data Model
- `styledChar{ch rune, style tcell.Style}` — single character with tcell style
- `highlightedLine{cells []styledChar}` — one syntax-highlighted line
- `renderedDiffLine{cells []styledChar}` — fully assembled diff line (numbers + prefix + highlighted content + BG)

### Files
- `highlight.go` — Chroma tokenizer → `styledChar` cells. `highlightLines(lines, filename)` batch-highlights, `tokenToStyle` maps Chroma tokens to tcell styles via monokai palette.
- `diffrender.go` — `buildUnifiedDiffLines` and `buildSideBySideDiffLines` produce `[]renderedDiffLine` with line numbers, +/- prefix, syntax-highlighted content, and diff background colors.
- `terminalpane.go` — `EnterDiffMode` pre-renders unified lines; split lines are lazily cached per width (`diffSplitWidth` invalidation). `renderDiff` paints via `drawStyledLine`.
- `reviews.go` — `applyFileDiff` pre-renders unified lines into `diffRendered` field.

### Gotchas
- Per-line tokenization loses cross-line context (multi-line strings, block comments) — accepted tradeoff since diff content is inherently fragmented.
- Diff backgrounds use fixed RGB (`#3d1012` removed, `#0d3317` added) for consistent tinting; foregrounds use palette indices to adapt to terminal themes.
- `applyDiffBG` unconditionally overlays the diff background on all cells — Chroma token backgrounds are overwritten by the diff background.

## Agent View Header: 2026-03-19

### Data Model & Flow
- `AgentHeader` widget (`internal/tui2/agentheader.go`): 1-row `tview.Box` rendering a centered powerline segment with the task name.
- Uses the same color palette as the root `Header` (`headerActiveBG`, `headerActiveFG`, `headerBaseBG`, `powerlineSep`).
- `SetTaskName(name)` is called from `onTaskSelect()` in `app.go` when entering the agent view.
- Agent page layout changed from a flat `FlexColumn` to a `FlexRow` wrapping: agent header (1 row fixed) + agent panels (flex, 3-column).
- PTY size fallback in `startSession` updated: subtracts 3 rows (root header + agent header + statusbar) instead of 2.

## Infinite Scrollback: 2026-03-19

### Data Model
- `TerminalPane` new fields: `anchorTotalLines int` (anchor-lock tracking), `replayEmu`/`replayEmuBytes`/`replayEmuCols`/`replayEmuRows`/`replayEmuLogSize` (replay emulator cache).
- `readLogTail(size int64) ([]byte, int64)` — seeks from EOF in session log file, returns data + file size.

### Flow
- **Live follow-tail** (`scrollOffset == 0`): Unchanged — uses ring buffer + incremental vt10x feed. Fast path.
- **Scrollback** (`scrollOffset > 0`): `Draw()` reads from session log file via `readLogTail()` with estimated byte count `(scrollOffset + height) * cols * 3`, minimum 1MB. Falls back to ring buffer if log unavailable.
- **Anchor-lock**: `paintEmu()` tracks `anchorTotalLines`. When total lines grow while scrolled up, bumps `scrollOffset` by delta. Reset on scroll-to-bottom.
- **Replay caching**: `renderReplay()` caches the `xvt.SafeEmulator` keyed by `(logSize, ptyCols, ptyRows)` or `(len(raw), ptyCols, ptyRows)`. Only rebuilds on data/dimension change.

### Gotchas
- Session log is concurrently appended by `readLoop` — use `os.Open` + `ReadAt`, not `ReadFile`.
- vt10x handles truncated escape sequences at read boundaries gracefully (partial sequences ignored).
- `readLogTail` returns `(nil, 0)` if no log file exists; callers fall back to ring buffer.
- Anchor reset happens both in `ScrollDown` (when hitting 0) and `ResetScroll` to avoid stale anchors in follow-tail mode.

## Project Status Icons & Idle Wiring: 2026-03-19

### What Was Missing
The tui2 migration (Phase 11) ported individual task status icons but dropped:
1. **Project header status icons** — `drawProjectRow` rendered only the project name, no aggregated icon
2. **Idle state wiring** — `SetIdle()` existed on `TaskListView` but `app.go` never called `runner.Idle()`
3. **Icon animation** — no `tickEven` toggle for alternating in-progress icons
4. **Auto-navigate on completion** — `handleSessionExitUI` cleared the session but didn't exit the agent view

### Data Model & Flow
- `TaskListView.tickEven bool` — toggles each tick for status icon animation (Nerd Font \uF10C circle-o ↔ \uF192 dot-circle-o)
- `TaskListView.Tick()` — called from `refreshTasksWithIDs` on every refresh cycle
- `projectStatusIcon(tasks) (rune, tcell.Style)` — computes aggregated icon with priority: in_progress > in_review > all_complete > mixed(✓ dimmed) > all_pending. Idle detection: when all in-progress tasks are idle, shows moon (☾).
- `drawProjectRow` now renders: 2-char indent + status icon + chevron (▸/▾) + project name + count `(N)`
- `refreshTasksWithIDs(runningIDs, idleIDs []string)` — signature expanded to accept idle IDs. All three call sites updated: `onTick`, `handleSessionExitUI` goroutine, `refreshTasks`.
- `handleSessionExitUI` now calls `exitAgentView()` when viewing a completed task's agent pane (not stopped — stopped tasks stay on agent view with cleared session).

### Task Status Handling (2026-03-19)

**Restored pre-tcell behavior for task status transitions and visual feedback:**

- **Stopped agent → InReview**: `handleSessionExitUI` now sets `StatusInReview` (not Pending) when `stopped == true`. Matches the Bubble Tea `determinePostExitStatus` behavior where explicit stop = "needs human review".
- **Idle+unvisited visual promotion**: `App` struct gains `idleUnvisited` and `viewedWhileAgent` maps. `refreshTasksWithIDs` diffs newly-idle tasks against `TaskListView.IdleSet()` to populate `idleUnvisited`. Entering agent view clears the flag via `onTaskSelect`. `drawTaskRow` renders idleUnvisited InProgress tasks with InReview icon (◎, cyan). `projectStatusIcon` counts them as InReview at project level.
- **Manual status cycling**: `s`/`S` keys in task list call `Status.Next()`/`Prev()` via `OnStatusChange` callback → `db.Update` + `refreshTasks`.
- **Task row animation**: `drawTaskRow` now checks `tickEven` for running InProgress tasks, alternating Nerd Font \uF10C (circle-o) and \uF192 (dot-circle-o). Idle (visited) tasks show moon (☾). Idle+unvisited show ◎.

**New fields on `TaskListView`**: `idleUnvisited map[string]bool`, `OnStatusChange func(task)`.
**New methods**: `SetIdleUnvisited(ids)`, `IdleSet() map[string]bool`.
**New fields on `App`**: `idleUnvisited map[string]bool`, `viewedWhileAgent map[string]bool`.

### Gotchas
- Chevron state checks both `tl.expanded` and `tl.archiveProject` — same-named projects in main and archive sections will both show expanded chevrons. Acceptable given the BT code had the same behavior.

## New Task Form Polish: 2026-03-19

### Changes
Three visual fixes to the `NewTaskForm` modal:

1. **Word wrapping** — `wrapPrompt(width)` now breaks at word boundaries (last space within width) instead of hard character positions. Hard-breaks only when a single word exceeds width. `cursorWrappedPos` updated from simple division (`pos/width`, `pos%width`) to linear search through variable-length wrapped lines. `moveCursorUp`/`moveCursorDown` use actual `wrappedLine.start` offsets instead of `line*width`.

2. **Modal background consistency** — Modal uses `Color235` background. Input field area uses `Color237` (slightly lighter) to create visual depth. Both focused and unfocused input states render against proper backgrounds. Placeholder text also uses the input background.

3. **Cursor visibility** — Changed from `Foreground(Color(17)).Background(Color(153))` to `Foreground(ColorBlack).Background(Color252)` — high-contrast block cursor. Empty cells in the input area now have the `Color237` background, so the cursor block is visible even at end-of-line.

### Data Model
- No new fields or DB columns — purely rendering changes
- `wrappedLine` struct unchanged: `{start, length}` still indexes into `f.prompt`
- `wrapPrompt` returns variable-length lines (previously all lines were `width` except the last)

### Gotchas
- `cursorWrappedPos` can no longer use division — must iterate `wrapPrompt` result. This means `wrapPrompt` is called more often (once per cursor position query), but the prompt is small so this is negligible.
- Word wrap includes trailing space on the broken line (e.g., "hello " not "hello"). This keeps cursor positions contiguous across the prompt rune slice with no gaps.

## Enter Guard on Completed Tasks: 2026-03-19

### Flow
- `TaskListView.InputHandler()` in `tasklist.go` checks `t.Status != model.StatusComplete` before calling `OnSelect`
- Single-line guard — no new types, fields, or DB changes

### Gotchas
- The guard is on the tasklist side, not `onTaskSelect` — so any programmatic calls to `onTaskSelect` (e.g., from new task form) are unaffected

## Worktree Cleanup Fix: 2026-03-19

### Problem
`Ctrl+R` prune and `Ctrl+D` delete left behind stale worktree directories and `argus/*` branches. Three root causes:
1. `git worktree remove --force` can exit 0 but leave empty dirs — `os.RemoveAll` fallback only ran on error
2. Stale worktree refs in `.git/worktrees/` prevented `git branch -D` from deleting the branch
3. Tasks created before the `CreateWorktree` branch-name fix stored the base branch (`origin/master`) in `task.Branch` instead of the actual worktree branch (`argus/<name>`)

### Data Model
No changes — same `task.Worktree` and `task.Branch` fields.

### Flow
- `removeWorktree()` in `internal/tui2/worktree.go`: runs `git worktree remove --force`, then ALWAYS checks `dirExists` and calls `os.RemoveAll` if the directory persists
- `removeWorktreeAndBranch()`: runs `git worktree prune` before branch deletion; if `task.Branch` doesn't start with `argus/`, infers the correct branch from `"argus/" + filepath.Base(worktreePath)`
- All functions now log to uxlog with `[worktree]` prefix for debugging

### Gotchas
- `git worktree prune` must run BEFORE `git branch -D` — git tracks worktree→branch associations in `.git/worktrees/` and refuses to delete a branch with a (possibly stale) worktree reference
- Branch inference from directory name only works when the worktree dir was created by `CreateWorktree` (which uses the sanitized task name). Manual worktrees would need the correct branch stored on the task

## PR URL Detection Restoration: 2026-03-20

### What was missing
PR URL detection was lost during the Bubble Tea → tcell/tview migration. The data model (`Task.PRURL`, `pr_url` DB column), display (`SetPRURL`, `OpenPR`), and agent view key bindings (`ctrl+p`, `o`) all existed, but the scanning loop that populates `PRURL` was never ported.

### Flow
1. **Tick scan**: `onTick()` iterates `runner.Running()`, calls `sess.RecentOutputTail(32KB)`, matches `prURLRe`, persists to DB, updates agent pane (guarded by `agentState.TaskID == taskID`)
2. **Exit scan**: Both `NotifySessionExit` (in-process) and `HandleSessionExit` (daemon) call `scanAndStorePRURL(taskID, lastOutput)` to catch fast-finishing agents
3. **Key bindings**: `p` in task list via `OnOpenPR` callback, `ctrl+p` in task list via same callback, `ctrl+p` in agent view (existing), `o` in agent view when dead (existing)

### Data model
- `prURLRe` — package-level compiled regex in `internal/tui2/app.go`
- `scanAndStorePRURL(taskID, lastOutput)` — shared helper for exit paths, goroutine-safe
- `OnOpenPR` callback on `TaskListView` — same pattern as `OnArchive`, `OnStatusChange`

### Gotchas
- `NotifySessionExit` signature changed to accept `lastOutput []byte` — callers in `main.go` must pass it through (was previously discarded with `_`)
- `SetPRURL` in `QueueUpdateDraw` must guard on `agentState.TaskID` to avoid setting the wrong PR on the visible agent pane
- Both `p` and `ctrl+p` in the task list route through `OnOpenPR` for testability and consistency

## TDD Infrastructure: 2026-03-20

### Data Model
- `internal/testutil/testutil.go` — assertion helpers: `Equal[T]`, `DeepEqual[T]` (go-cmp), `NotEqual[T]`, `Nil`/`NotNil` (reflection-based for nil-interface trap), `NoError`/`Error`/`ErrorIs`, `True`/`False`, `Contains`
- Dependency: `github.com/google/go-cmp` for `DeepEqual` struct diffs

### Flow
- Import `"github.com/drn/argus/internal/testutil"` → call `testutil.Equal(t, got, want)` etc.
- All assertions use `t.Errorf` (not `t.Fatalf`) so multiple failures surface per run
- `Nil`/`NotNil` use `reflect.ValueOf(got).IsNil()` to handle the nil-interface trap (nil `*T` assigned to `any` is non-nil at interface level)

### Build Targets (Makefile)
- `make test` — `go test -race -count=1 ./...`
- `make test-watch` — `gotestsum --watch` (checks for install)
- `make test-cover` — coverage profile + summary
- `make test-pkg PKG=./internal/db/` — single package verbose

### CI Changes
- Go 1.24 → 1.25, added `-coverprofile=coverage.out`, coverage summary step, artifact upload

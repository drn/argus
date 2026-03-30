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
6. **SBPL has no abstract PTY operation — use file-write rules on device nodes.** `(allow pseudo-terminal*)` is not a valid SBPL operation and causes `sandbox-exec: unbound variable` at runtime. PTY access requires `(allow file-write* (regex #"^/dev/ptmx$"))` and `(allow file-write* (regex #"^/dev/ttys[0-9]+$"))`. The `TestGenerateSandboxConfig_ProfileValid` test validates the generated profile by running `sandbox-exec /usr/bin/true` — any invalid operation names fail the test immediately.
7. **Nested sandbox-exec is blocked by macOS.** If the process is already sandboxed (e.g., inside Claude Code's sandbox), `sandbox_apply` fails with exit 71 (`Operation not permitted`). `sandboxExecFunctional()` detects this; `IsSandboxAvailable()` only checks binary existence. All enforcement tests (`TestSandbox_*`) use `sandboxExecFunctional(t)` and skip when nested. Enforcement tests cover: write inside/outside worktree, /tmp writes, custom deny-read paths, extra write paths, git dir writability, and credential directory blocking with SSH known_hosts exception.
7. **Symlink write rules use resolved paths.** If `~/.claude/skills` is symlinked to `~/.dots`, the `(subpath "~/.claude")` write rule does NOT cover writes to resolved `~/.dots/...` paths (kernel resolves symlinks before matching). Reads are unaffected (global `(allow file-read*)` applies to resolved paths). Only add `~/.dots` to write rules if agents actually need to write there.

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
- Per-project sandbox stored in `projects` table: `sandbox_enabled` (""=inherit, "true", "false"), `sandbox_deny_read`, `sandbox_extra_write` (CSV)
- `sandbox.allowed_domains` key was used by srt; now orphaned in existing DBs but harmlessly ignored
- List values stored as CSV (comma-separated). Known limitation: paths with commas would break.
- `SetSandboxEnabled(bool)` convenience method on DB; other values via `SetConfigValue`
- **`sandbox.extra_write` garbage causes broken SBPL rules.** Each CSV value becomes `(allow file-write* (subpath "..."))`. A partial entry like `"e"` produces `(allow file-write* (subpath "e"))` — valid syntax, no effect. Clear via: `sqlite3 ~/.argus/data.sql "UPDATE config SET value='' WHERE key='sandbox.extra_write'"`

### Per-Project Sandbox UI (2026-03-24)
- **Project form**: 5th field (`pfFieldSandbox`) is a ◀/▶ selector with options: Inherit, Enabled, Disabled. Maps to `ProjectSandboxConfig.Enabled`: nil, &true, &false.
- **Settings detail**: `renderProjectDetail` shows sandbox mode (Inherit/Enabled/Disabled) with color coding, plus per-project deny_read and extra_write paths.
- **Resolution**: `ResolveSandboxConfig()` in `internal/agent/agent.go` merges per-project on top of global: Enabled overrides, paths append.

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

## Worktree Creation Stale-Ref Fix: 2026-03-20

### Problem
`CreateWorktree` failed with exit status 255 when a previous worktree directory was deleted without `git worktree remove`. Git retained a stale worktree→branch reference, causing both `git worktree add -b` (branch exists) and `git worktree add` (branch locked to stale entry) to fail. The error was also unreadable — `fmt.Errorf("...%s\n%s", ...)` produced a newline in the error string, but `drawText` renders on a single row, hiding the actual git fatal message.

### Fix
1. `git worktree prune` runs at the top of `CreateWorktree` (best-effort, errors ignored) to clean stale entries
2. After each `git worktree add` failure, `isValidWorktreeDir(wtDir)` checks for `.git` file inside the directory — catches post-checkout hook failures where the worktree was created despite non-zero exit
3. Error message uses `cleanGitOutput()` which extracts `fatal:` lines and collapses newlines for single-line display
4. uxlog calls at every decision point: entry, cmd1 fail, partial success, cmd2 fail/success, final success

### Key entities
- `isValidWorktreeDir(dir)` — checks `filepath.Join(dir, ".git")` exists (stronger than bare `os.Stat(dir)`)
- `cleanGitOutput(outputs ...[]byte)` — combines git output, extracts `fatal:` lines, collapses to single line
- `[worktree]` uxlog prefix for all `CreateWorktree` logging

### Gotchas
- `os.Stat(wtDir)` alone is insufficient — a partial failure can leave an empty directory. Must check `.git` file presence
- Error format `%s\n%s` is invisible in `drawText` (single-row renderer) — always use single-line error messages for form display

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

## Escape Key Agent View Fix: 2026-03-20

### Problem
Pressing Escape in agent view (terminal focused) exited back to the task list instead of being forwarded to the PTY. The `case tcell.KeyEscape:` block had a comment-only fallthrough for the terminal-focused case, letting the event reach the generic "Forward to PTY" block gated by `sess != nil && sess.Alive()`. When the session was dead or nil, the event returned unhandled to tview, which exited the view.

### Fix
Escape is now explicitly handled in the `case tcell.KeyEscape:` block: forwards `0x1b` to PTY when alive (with `ResetScroll()` to snap back from scrollback, matching the generic forward block's behavior), and always returns `nil` to consume the event. Location: `internal/tui2/app.go` lines 795-801.

### Gotchas
- Must call `ResetScroll()` after writing escape to PTY — the generic forward block does this for all keys, so escape must match
- The `return nil` is unconditional — dead/nil sessions silently consume escape rather than leaking it to tview

## Prune Worktree Fix: 2026-03-20

### Problem
`Ctrl+R` prune deleted DB records first, then ran worktree/branch cleanup in a background goroutine. If the TUI exited before cleanup finished (each `git push origin --delete` takes ~1.5s), branches were left behind with no way to retry.

### Data model
- `db.WorktreePaths()` — returns `(map[string]bool, error)` of all worktree paths in the DB (used for orphan detection)
- `PruneModal` (`prunemodal.go`) — `tview.Box` widget with animated dots, `Increment()` for progress, absorbs all keys
- `countOrphanedWorktrees(knownPaths)` / `sweepOrphanedWorktrees(knownPaths, projects)` in `worktree.go` — scan `~/.argus/worktrees/` for dirs not in DB
- `modePruning` view mode in `app.go` — absorbs all keys during cleanup

### Flow
1. `PruneCompleted()` fetches+deletes completed tasks from DB
2. Stop sessions, remove session logs
3. Count task worktrees + orphan worktrees
4. Show `PruneModal` with total count
5. `sync.WaitGroup` with parallel goroutines: one per task cleanup + one orphan sweep. Each goroutine calls `QueueUpdateDraw` → `pruneModal.Increment()` on completion (thread-safe: all increments serialized on tview main goroutine).
6. `wg.Wait()` → `QueueUpdateDraw` → `closePruneModal` + `refreshTasks`
7. Display transitions: static "Cleaning up N worktree(s)..." → iterative "(current/total)" as each finishes

### Gotchas
- Orphan sweep infers branch as `"argus/" + filepath.Base(worktreePath)` since no DB record exists
- `walkOrphanedWorktrees` with nil projects map just counts; with non-nil map it removes
- Empty project directories are cleaned up after sweep
- Modal absorbs ALL keys during cleanup — prevents premature exit
- `WorktreePaths()` returns error; caller skips orphan sweep on failure to avoid false positives

## Terminal Style Conversion Completeness: 2026-03-20

### Data Model
- `uvCellToTcellStyle()` in `terminalpane.go` converts `uv.Cell.Style` → `tcell.Style`
- Ultraviolet `Style` struct: `Fg`, `Bg`, `UnderlineColor` (all `color.Color`), `Underline` (enum), `Attrs` (uint8 bitmask)
- Ultraviolet `Cell` also carries `Link` (OSC 8 hyperlink with URL + params)

### Flow
PTY bytes → x/vt emulator → `paintEmu()` iterates cells → `uvCellToTcellStyle()` per cell → `screen.SetContent()`

### Attribute Mapping (must stay in sync with `uv.Attr*` constants)
| uv constant | tcell method | SGR code |
|---|---|---|
| `AttrBold` | `Bold(true)` | 1 |
| `AttrFaint` | `Dim(true)` | 2 |
| `AttrItalic` | `Italic(true)` | 3 |
| `AttrBlink` | `Blink(true)` | 5 |
| `AttrReverse` | `Reverse(true)` | 7 |
| `AttrStrikethrough` | `StrikeThrough(true)` | 9 |

### Underline styles mapped to `tcell.UnderlineStyle*`
Single→Solid, Double, Curly, Dotted, Dashed. Underline color via `style.Underline(ulStyle, color)`.

### Gotchas
- Missing `AttrFaint→Dim` caused Ink-based CLIs (Codex) to lose highlight contrast — dimmed text rendered at full brightness
- `AttrConceal` (SGR 8) and `AttrRapidBlink` (SGR 6) not mapped — rarely used, tcell has no direct support for conceal
- Hyperlinks (`cell.Link.URL`) forwarded via `style.Url()` — tcell has no getter for URL so not directly testable
- Old code used `Underline(true)` for all styles — lost curly/dotted/dashed distinction used by Claude Code diagnostics

## Session Resume Wiring: 2026-03-20

### Data Model
- `task.SessionID` (string, persisted in SQLite `session_id` column) — UUID for conversation pinning
- Claude backends: UUID generated via `model.GenerateSessionID()` before first `runner.Start`
- Codex backends: UUID captured post-exit from `~/.codex/state_5.sqlite` via `agent.CaptureCodexSessionID(worktreePath)`

### Flow
1. **First run (Claude):** `startSession` → `GenerateSessionID()` → `db.Update` → `BuildCmd` uses `--session-id <uuid>` → `runner.Start`
2. **First run (Codex):** `startSession` → no ID → `BuildCmd` uses bare command → `runner.Start`
3. **Session exit (Codex):** `handleSessionExitUI` → background goroutine → `CaptureCodexSessionID` → `QueueUpdateDraw` → `db.Update`
4. **Resume (both):** `startSession` → `resume = task.SessionID != ""` → `BuildCmd` uses `--resume <id>` (Claude) or `codex resume ... <id>` (Codex)
5. **Enter-to-restart:** `handleAgentKey` → `KeyEnter` when session dead → `db.Get(taskID)` → `startSession`

### Gotchas
- `CaptureCodexSessionID` opens SQLite — must not run on tview main goroutine (blocking I/O)
- `resume` flag derived from `task.SessionID != ""` — if SessionID is never set, resume never triggers
- Enter key in agent view had no handler for dead sessions — fell through to PTY write (no-op)

## renderLive Buffer Copy Optimization: 2026-03-20

### Problem
Every tview redraw (including keystroke-triggered redraws) called `sess.RecentOutput()` which copies the full 256KB ring buffer. When typing in the agent view, each keystroke triggers a draw before the PTY echo arrives — copying 256KB with 0 new bytes, causing perceptible input lag.

### Fix
1. **`renderLive` checks `TotalWritten()` vs `emuFedTotal` before copying.** When no new bytes exist and the emulator dimensions haven't changed, the buffer copy is skipped entirely and the cached emulator is repainted directly.
2. **`startAgentRedrawLoop` tracks `lastTotalWritten`.** Only queues `QueueUpdateDraw` when new output has arrived. Keystroke and resize events trigger their own tview redraws.

### Invariants
- `emuFedTotal` must only advance when bytes are actually fed to the emulator (not on empty `raw`)
- `needRebuild` (emu nil or dimensions changed) must always trigger a full buffer copy + replay
- The `else if tp.emuFedTotal == 0` guard handles the "no data ever" case when the fast path would otherwise paint an uninitialized emulator

## Bracket Paste Support: 2026-03-20

### Problem
Pasting large text into any input (agent terminal, new task form, settings forms) was extremely slow. Without `EnablePaste`, tview delivers paste as individual `EventKey` events — each triggering a full screen redraw. A 5KB paste = ~5000 redraws.

### Fix
1. **`tapp.EnablePaste(true)` in `initUI()`** — enables bracket paste mode on the tcell screen. tview buffers all pasted chars internally (zero per-key redraws) and calls `PasteHandler()` once at the end.
2. **`PasteHandler()` on all 5 text input widgets:**
   - `TerminalPane` — writes entire paste to PTY in one `WriteInput()` call, wrapped in bracket paste escape sequences (`\x1b[200~`/`\x1b[201~`)
   - `NewTaskForm` — inserts all runes at cursor in a single slice operation
   - `ProjectForm`, `BackendForm`, `RenameTaskForm` — same single-operation insert into focused field

### Invariants
- Any new custom widget with text input MUST implement `PasteHandler()` — without it, paste is silently dropped when `EnablePaste` is on (tview bypasses `InputCapture` for paste events)
- PTY paste must include bracket paste sequences so the agent's readline handles it correctly
- Edit-mode read-only fields (name field in ProjectForm/BackendForm) must reject paste just like they reject keystrokes

## Fix: UI Freeze from Blocking RPC on Main Goroutine — 2026-03-20

### Problem
`refreshTasks()` called `runner.Running()` + `runner.Idle()` — both daemon RPC calls with up to 5s timeout. Seven call sites invoked this directly on the tview main goroutine, freezing all UI input until RPC completed. Additionally, `Running()` and `Idle()` each made a separate `ListSessions` RPC, doubling overhead.

### Data Model
- `SessionProvider` interface gains `RunningAndIdle() (running, idle []string)` — single-call variant
- `Runner.Sessions()` returns a snapshot map for the daemon's `ListSessions` RPC (avoids N+1 lock acquisitions)
- `App` caches `idleIDs []string` alongside existing `runningIDs` for `refreshTasksLocal()`

### Flow
Three refresh methods for three use cases:
1. **`refreshTasks()`** — blocking, for init only (before event loop)
2. **`refreshTasksAsync()`** — goroutine + QueueUpdateDraw, for UI-thread call sites (status change, archive, new task, daemon restart)
3. **`refreshTasksLocal()`** — reuses cached IDs, no RPC, for DB-only changes (delete, prune)

### Gotchas
- `net/rpc` serializes calls on a single connection — head-of-line blocking makes separate `Running()`/`Idle()` calls doubly expensive
- RPC timeout reduced from 5s to 2s — generous for local Unix socket, halves worst-case freeze
- `refreshTasksLocal` must use cached `idleIDs`, not call `runner.Idle()` (which is RPC)

### Paint Cache for Keystroke Redraws (2026-03-20)

**Problem:** Typing in the agent terminal had visible lag because every keystroke triggered a full `paintEmu` cycle: 10K+ `CellAt` calls (each acquiring RLock/RUnlock), style conversions, rune allocations, and `SetContent` calls — all producing identical output since PTY echo hasn't arrived yet. Compounded by tview's `screen.Clear()` which defeats tcell's dirty tracking, forcing full terminal I/O for every cell on every frame.

**Fix:** `paintEmu` now builds a `[]cachedCell` slice alongside its normal rendering. When `renderLive` detects no new bytes (`newBytes == 0`) and the viewport hasn't changed, it replays the cached cells via `replayPaintCache()` — a tight loop of `SetContent` calls with pre-computed values. Skips all emulator access, mutex operations, allocations, and style conversion.

**Cache invalidation:** `paintCacheValid` is set to `false` on: scroll (up/down/reset), `ResetVT()`, `SetSession()`. New bytes arriving naturally take the `newBytes > 0` path which rebuilds the cache. Viewport position/size changes are detected by comparing `paintCacheX/Y/W/H`.

**Data model:** `cachedCell{x, y int; ch rune; style tcell.Style}` — 40 bytes per cell. For a 200×50 viewport: ~400KB, reusing the backing array across frames. The cache lives on `TerminalPane` alongside the existing emulator cache fields.

### lazyScreen: Skip Clear() for PTY Keystrokes (2026-03-22)

**Problem (residual):** The paint cache eliminated CPU work (emulator access, style conversion) but did not eliminate terminal I/O. tview's `draw()` calls `screen.Clear()` → `CellBuffer.Fill(' ', StyleDefault)` which changes `currStr` for ALL cells. When widgets redraw identical content, `Put()` sees `cl != c.currStr` → `setDirty(true)` (sets `lastStr=""`). Then `Show()` → `drawCell()` finds every cell dirty → full terminal write (~10K cells per keystroke). The paint cache just made the redraw faster, not free.

**Fix:** `lazyScreen` wraps `tcell.Screen` and overrides `Clear()`. When `skipClear` is set, `Clear()` is a no-op. Without Clear, cells retain previous values → `SetContent` writes identical content → `Put()` sees `cl == c.currStr` → no dirty flag → `Show()` writes zero bytes. Flag is set in `handleAgentKey` right before returning nil for PTY-forwarded keystrokes. Consumed (reset to false) on the next `Clear()` call, so normal draws are unaffected.

**Threading:** Both `skipClear` set (InputCapture) and `Clear()` read (draw) happen on the tview main goroutine — sequential in the same event loop iteration. No synchronization needed.

**Data model:** `lazyScreen{tcell.Screen, skipClear bool}` in `lazyscreen.go`. Created in `App.Run()`, stored on `App.screen`. `tapp.SetScreen()` injects it before `tapp.Run()`.

## To Dos Tab (2026-03-20)

### Data Model & Flow
- `ToDoItem{Name, Path, Content, ModTime}` — scanned from Obsidian vault `.md` files in `ArgusVaultPath`
- `ToDosView` — three-panel layout (ToDoListPanel | ToDoPreviewPanel | ToDoDetailPanel) matching TaskPage structure
- `LaunchToDoModal` — project selector modal, creates a task with note content as prompt
- Vault path resolved from `KBConfig.ArgusVaultPath` (falls back to `DefaultArgusVaultPath()`)

### Flow
1. Tab switch to "To Dos" calls `Refresh()` → `ScanVaultToDos(vaultPath)` reads `.md` files sorted by mod time
2. Enter on a to-do opens `LaunchToDoModal` with project selector
3. Confirm creates worktree + task (same flow as `handleNewTaskKey`) and enters agent view

### Gotchas
- Tab indices shifted: 1=Tasks, 2=ToDos, 3=Reviews, 4=Settings — all hint text and test assertions updated
- `LaunchToDoModal` does not have a `PasteHandler` — it has no text input fields, only a selector

## Fix: Daemon Crash Incorrectly Marks Tasks Complete — 2026-03-20

### Problem
When the daemon crashed, one task was incorrectly marked Complete despite its agent still being live. Root cause: a race in `connectStream`'s retry loop. When `Client.Close()` fires during daemon restart, it closes `rs.done`. The `<-rs.done` case called `removeSession` (not `removeSessionStreamLost`), which tried `GetExitInfo` RPC on the closed client → zero-value `ExitInfo{StreamLost: false}` → `HandleSessionExit` treated it as normal exit → task marked Complete.

### Fix
1. Changed `removeSession` to `removeSessionStreamLost` in `connectStream`'s `<-rs.done` case — external close means session fate is unknown, must treat as stream lost
2. Added `!a.daemonRestarting` guard to reconciliation in `refreshTasksWithIDs` — belt-and-suspenders defense against new daemon having empty session list during restart
3. Reverted `refreshTasksLocal()` back to `refreshTasksAsync()` in task creation — the `daemonRestarting` guard eliminates the original race, so the overly-cautious local-only refresh is no longer needed

### Gotchas
- `removeSession` with a failed RPC produces zero-value `ExitInfo{StreamLost: false}` — silently treated as normal exit
- The `<-rs.done` path only triggers when `streamOnce` returns `(false, false)` (daemon briefly reachable, session alive), then `Client.Close()` fires between retries
- `rs.close()` is a no-op when `done` is already closed, but should be called for consistency with other exit paths

### Live Scrollback Cache Optimization (2026-03-20)

**Problem:** Scrolling up on a live agent session was laggy. The prior optimization (stat-based cache validity check) only worked for dead sessions — for live sessions, the log file grows continuously so `logSize != replayEmuLogSize` on every `Draw()`, causing 1MB+ file reads and emulator rebuilds per frame.

**Data Model:**
- `replayEmuMaxScroll int` — the scrollOffset the replay emulator was built for; used to detect when the user scrolls beyond cached data
- `paintCacheScroll int` — the scrollOffset when paint cache was built; enables full cell-cache replay when scroll offset is unchanged

**Flow:**
1. Live session, scrolled up (`alive && scrollOffset > 0`): cache is valid if dimensions match AND `scrollOffset <= replayEmuMaxScroll` — skips log I/O entirely
2. If `scrollOffset > replayEmuMaxScroll`: cache miss, rebuild with more data from log tail
3. If viewport + scroll offset unchanged: replay `paintCacheCells` directly (no emulator access)
4. Scroll to bottom (`scrollOffset == 0`): exits replay path, uses `renderLive` with live emulator

**Gotchas:**
- The log is append-only, so cached data remains valid for the viewed region even as new bytes arrive below viewport
- `ScrollUp`/`ScrollDown` set `paintCacheValid = false`, ensuring scroll offset changes trigger `paintEmu` (but not log re-reads)
- `replayEmuMaxScroll` must be reset in `ResetCache()` alongside other replay state

### Scroll Acceleration & Anchor-Lock Fix (2026-03-21)

**Bug:** First Shift+Up in agent view caused a half-page jump. Root cause: `renderReplay` builds a replay emulator from 1MB+ of log tail, which has far more scrollback than the live emulator's 256KB ring buffer. The anchor-lock mechanism in `paintEmu` interpreted the totalLines difference as "new output arrived" and bumped `scrollOffset` by the delta.

**Fix:** `renderReplay` now resets `anchorTotalLines = 0` on rebuild. With `anchorTotalLines == 0`, the anchor-lock guard (`tp.anchorTotalLines > 0`) is false, so no spurious scrollOffset bump occurs. `paintEmu` sets `anchorTotalLines = totalLines` on the first paint, establishing the correct baseline.

**Scroll acceleration:** Keyboard scrolling (Shift+Up/Down) now uses `AccelScrollUp()`/`AccelScrollDown()` instead of fixed `ScrollUp(1)`. Time-based acceleration tracks `lastScrollTime` — key repeats within `scrollAccelWindow` (120ms) ramp the step from 1 to `scrollAccelMax` (12). Pause resets to 1. `ResetScroll()` clears the acceleration state.

**Data Model:**
- `lastScrollTime time.Time` — when last keyboard scroll happened
- `scrollAccel int` — current acceleration multiplier (1-based)
- `scrollAccelWindow` (120ms) — time window for key repeat detection
- `scrollAccelMax` (12) — cap on acceleration multiplier

**Flow:**
1. `AccelScrollUp()` → `nextAccelStep()` checks elapsed time since `lastScrollTime`
2. If within window: increment `scrollAccel` (capped at max)
3. If outside window: reset to 1
4. Return step as scroll amount; update `lastScrollTime`
5. Mouse scroll unchanged (fixed `mouseScrollStep = 3`)

## Fork Task Feature: 2026-03-21

### Data Model & Flow
- **Ctrl+F** in task list opens `ForkTaskModal` (confirmation dialog)
- On confirm: `executeFork` runs in background goroutine:
  1. `extractForkContext` reads session log tail (last 32KB, ANSI-stripped) + `git diff HEAD` from source worktree
  2. `agent.CreateWorktree` creates new worktree from project's base branch (not source's argus/* branch)
  3. `writeForkContextFiles` writes `.context/fork-source.md`, `.context/fork-output.md`, `.context/fork-diff.patch`
  4. `QueueUpdateDraw` callback: creates task in DB, enters agent view, starts session
- New task prompt references `.context/` files and includes original prompt

### Files
- `forkmodal.go` — confirmation dialog (same pattern as `ConfirmDeleteModal`)
- `forkcontext.go` — context extraction, ANSI stripping, file writing, prompt generation
- `app.go` — `modeForkTask`, `openForkModal`, `handleForkTaskKey`, `closeForkModal`, `executeFork`

### Gotchas
- Fork execution is async (background goroutine) because git diff + log reads block UI thread
- Uses `refreshTasksLocal` not `refreshTasksAsync` to avoid reconciliation race (same as new task creation)
- `.context/` files skipped when empty (no output file for tasks that never ran, no diff for clean worktrees)

### Todo-Task Association & Cleanup (2026-03-21)

**Data Model:**
- `Task.TodoPath string` — links a task to its source Obsidian vault `.md` file path. DB column `todo_path` with index `idx_tasks_todo_path`.
- `LaunchToDoModal` — extended with a prompt input field (focused by default). User prompt + note content combined via `buildToDoPrompt()` using `<context>` XML tags.
- `ConfirmCleanupToDosModal` — confirmation dialog for Ctrl+R cleanup on ToDos tab.

**Flow:**
1. User presses Enter on a todo → `LaunchToDoModal` shows with project selector + prompt field
2. On confirm: `task.TodoPath = item.Path`, prompt = `buildToDoPrompt(userPrompt, noteContent)`, worktree created, task persisted
3. `refreshTasksWithIDs` calls `a.db.TasksByTodoPath()` on every tick → `ToDosView.SyncTasks()` propagates to list panel
4. `ToDoListPanel.Draw` renders status-aware bullets: `○` pending, `●` in progress, `◎` in review, `✓` complete
5. Ctrl+R on ToDos tab → `cleanupCompletedToDos()` → confirmation modal → `executeToDoCleanup()` removes vault `.md` files, refreshes async

6. `s`/`S` keys on ToDos tab toggle linked task status via `OnStatusChange` callback → `db.Update` + `refreshTasksAsync`. No-op if todo has no linked task.

**Default ToDo Project (2026-03-22):**
- `Defaults.TodoProject` — config field persisted as `defaults.todo_project` in the config table
- `openLaunchToDoModal` passes `cfg.Defaults.TodoProject` to pre-select the project in the launch modal
- Settings UI: `srToDoProject` row in KB section, cycles through projects with Enter/Left/Right via `cycleTodoProject(dir)`
- `cycleTodoProject` prepends an empty string ("none") option; stale project names silently reset to "none" on first cycle

**Gotchas:**
- `handleLaunchToDoKey` must use `refreshTasksLocal()` (not `refreshTasks()`) between `db.Add` and `startSession` — same reconciliation race as new task form
- `executeToDoCleanup` validates `item.Path` starts with `vaultPath + PathSeparator` before `os.Remove` — prevents path traversal
- `executeToDoCleanup` uses `RefreshAsync` (not `Refresh`) to avoid blocking the UI thread on disk I/O
- `TasksByTodoPath()` query uses `ORDER BY created_at ASC` so the last entry per path is the most recent task (map overwrite = most recent wins)
- `todo_path` column has a DB index for the tick-frequency query

**Scroll Mode Transition Fix (2026-03-22):**
- `invalidateReplayCache()` — private helper that nils `replayEmu`, zeros `replayEmuBytes`, `replayEmuLogSize`, `replayEmuMaxScroll`, and `anchorTotalLines`
- Called by `ScrollUp` and `AccelScrollUp` on the `scrollOffset == 0 → >0` transition (entering scroll mode from live mode)
- Two bugs fixed: (1) stale replay emu from a previous scroll had content that didn't match current live output; (2) stale `anchorTotalLines` from `renderLive` caused `paintEmu` anchor-lock to bump `scrollOffset` by the totalLines difference (~half screen jump)
- Only fires on the 0→>0 transition — continued scrolling while already scrolled reuses the cached replay emu (correct behavior)
- All 0→>0 transitions verified to go through `ScrollUp` or `AccelScrollUp` (mouse wheel, Shift+Up, Shift+PgUp)

---

## 2026-03-22 — Rename Task Feature

**Data model:** No new columns. `task.Name` is updated in-place via `db.Rename(id, name)` — a targeted `UPDATE tasks SET name=? WHERE id=?` that only touches the name column.

**Flow:** `r` key on task list → `OnRename` callback → `openRenameModal(task)` creates `RenameTaskForm` (pre-populated) → modal captures all keys via `modeRenameTask` in `handleGlobalKey` → Enter validates via `sanitizeTaskName` (strips control chars, collapses whitespace) → `db.Rename` persists → `closeRenameModal` → `refreshTasksLocal` rebuilds UI.

**Gotchas:**
- `db.Rename` must be used instead of `db.Update` — the modal captures a task pointer at open time; background `refreshTasksAsync` can orphan it, and `db.Update` on the stale pointer would overwrite concurrent field changes (e.g., agent exit changing status)
- `sanitizeTaskName` converts `\n\r\t` to spaces and strips other control chars (`< 0x20`) to prevent `drawText` rendering glitches from pasted content
- `done` flag must be reset to `false` on empty-name validation failure, otherwise every subsequent keypress re-triggers the validation check (the new task form has this latent bug but rename avoids it)

### 2026-03-22: Task List Filter (`/` key)

**Data Model:**
- `TaskListView.filtering bool` — true while filter input is focused
- `TaskListView.filter string` — current filter text (case-insensitive substring match against task name or project name)

**Flow:**
1. User presses `/` on task list → `filtering = true`, filter input shown at bottom of panel
2. Typing appends to `filter`, `buildRows` filters tasks via `matchesFilter()`, all matching projects auto-expand
3. Arrow keys navigate filtered results while typing
4. Enter confirms filter (keeps filter active, exits input mode); Escape clears filter entirely
5. When filter is active but input not focused, Escape clears it from normal mode
6. `PasteHandler` supports paste into filter input

**Gotchas:**
- `handleGlobalKey` must bypass rune shortcuts (`q`, `1-4`) when `tasklist.Filtering()` is true
- `buildRows` expands all projects and archive when `filter != ""` — otherwise matched tasks in collapsed projects are invisible
- Filter title shown in panel border: `" Tasks [/query] "`

## Bug Fix: Cursor Highlight Overwriting Elapsed Time — 2026-03-22

**Problem:** `drawTaskRow` cursor fill loop iterated from end-of-name to edge-of-row, overwriting the right-aligned elapsed time indicator that was already drawn.

**Fix:** Compute `elapsedCol` once before both the elapsed time draw and cursor fill blocks. Use it as the fill boundary so the loop stops before the elapsed time region.

**Gotchas:**
- `elapsedCol` was computed identically in two places — deduplicated to a single computation site to prevent drift
- The `-1` in `elapsedCol = x + w - len(elapsed) - 1` is a 1-cell right margin

## Fork Output Sanitization: 2026-03-22

**Data Model:** `sanitizeForkOutput(string) string` in `forkcontext.go` — called after `stripANSI()` in `readSessionLogTail()`.

**Flow:** Raw PTY log bytes → ANSI stripping → `\r`/NBSP normalization → inline noise removal (long lines >120 bytes) → per-line noise filtering → blank line collapsing → trimming.

**Noise categories (15 regex patterns):**
- Spinner chars (✳✶✻✽✢·), thinking/Warping/Clauding indicators, status bar chrome (⏵), separator lines (─), bare prompts (❯), partial character renders, timing hints, Shell cwd resets, Running markers, No output markers, Baked for status, expand hints, lone digits, empty assistant markers (⏺), keybind hints.

**Inline patterns (9 regex patterns in `cleanLongLine`):** Handle VT cell concatenation where content area + status bar + separator + prompt are all on one terminal row.

**Gotchas:**
- PTY logs have `\r` (carriage return) throughout — must normalize to `\n` before line-based filtering
- NBSP (`\u00a0`) appears in tool result formatting — must normalize to regular space
- `len(line) > 120` uses byte length, which works because noise-concatenated lines are typically 600-1100 bytes
- `partialRenderRe` allows up to 4 chars (`{0,4}`) because partial renders include fragments like "rpng"
- `inlineWarpClaudRe` uses `[^⏺⎿]*` to consume noise without eating into the next content boundary

## Bug Fix: EnablePaste/EnableMouse Regression from lazyScreen — 2026-03-22

**Problem:** Commit `9479777` introduced `lazyScreen` wrapping `tcell.Screen` via `tapp.SetScreen()` in `Run()`, but `EnablePaste(true)` and `EnableMouse(true)` remained in `initUI()` which runs during `New()` — before the screen exists. tview's `EnablePaste` checks `a.screen != nil` before calling `screen.EnablePaste()`, so the flag was stored but never applied. And `Run()` only auto-enables when it creates its own screen (`a.screen == nil`), which it doesn't when `SetScreen` was called first. Result: bracketed paste silently broken in both agent view and new task form.

**Fix:** Moved `EnableMouse(true)` and `EnablePaste(true)` from `buildUI()` to `Run()`, after `SetScreen()`.

**Smoke test infrastructure added (`smoke_test.go`):**
- `simApp(t)` — creates `lazyScreen`-wrapped `SimulationScreen` with correct Enable ordering
- `wireApp(t, app)` — replaces an `App`'s tview with a SimulationScreen (sets `app.screen` to match production)
- `runApp(t, app)` — manages event loop lifecycle with `syncUI` readiness check
- `syncUI(t, app)` — waits for injected events to propagate (eventSettle + QueueUpdate round-trip)
- `readUI(t, app, fn)` — executes fn on tview goroutine to avoid data races on UI state reads

**Tests added:**
- `TestEnablePasteAfterSetScreen` — end-to-end paste via SimulationScreen
- `TestEnableMouseAfterSetScreen` — end-to-end mouse via SimulationScreen
- `TestLazyScreen_EnableDisableDoesNotPanic` — embedding passthrough verification
- `TestSmoke_TabSwitching` — numeric key tab switching through all 4 tabs
- `TestSmoke_NewTaskFormPaste` — open form, paste text, verify prompt
- `TestSmoke_AgentViewEnterExit` — Enter to agent view, Ctrl+D to exit
- `TestSmoke_NewTaskFormEscape` — open form, Esc to close

**Gotchas:**
## Keystroke Echo Redraw: 2026-03-22

**Problem:** PTY keystrokes had up to 200ms visible lag. The keystroke-triggered tview draw fires before the PTY echo arrives (~1-5ms). The 200ms `startAgentRedrawLoop` poll was the only path to paint the echo.

**Fix:** 16ms delayed `QueueUpdateDraw` goroutine after each `WriteInput`, guarded by `TotalWritten` snapshot comparison.

**Key interactions:**
- `skipClear` is set for the immediate draw (no-op, correct) but NOT for the 16ms follow-up — `TotalWritten` guard prevents wasted `Clear()` when echo hasn't arrived
- The 200ms poll loop and 16ms goroutine complement each other: whichever fires first consumes the `TotalWritten` delta, the other sees no change and skips
- Rapid typing (~20 chars/sec) creates ~20 goroutines alive at once (each 16ms lifetime), `QueueUpdateDraw` calls coalesce in tview
- `TotalWritten()` is local in both session types (mutex-protected counter, no RPC) — safe from background goroutine

**Gotchas:**
- SimulationScreen's `PostEvent(EventPaste)` bypasses real bracket paste mode — negative test (broken ordering) is not possible in simulation
- `QueueUpdate` and SimulationScreen event injection use separate channels — `syncUI` needs `eventSettle` sleep before the QueueUpdate round-trip to let events propagate
- UI state reads (`header.ActiveTab()`, `app.mode`) must happen inside `readUI`/`QueueUpdate` to avoid data races with the tview goroutine

## Scrollback Performance Fix: 2026-03-22

**Problem:** Scrolling up in long agent sessions was slow — every scroll step beyond the initial build-time offset triggered a full replay emulator rebuild (log file read + x/vt feed).

**Root cause:** `replayEmuMaxScroll` was set to `tp.scrollOffset` at build time (typically 1-2 lines with acceleration), not the emulator's actual scrollback capacity. The cache validity check `tp.scrollOffset <= tp.replayEmuMaxScroll` failed on nearly every subsequent scroll.

**Fix — three parts:**
1. After building the replay emulator, compute actual max scroll from `emu.ScrollbackLen() + lastContentRow + 1 - viewportHeight` instead of using the build-time scroll offset
2. Introduced `newDrainedReplayEmulator` with 50K-line scrollback buffer (vs x/vt default 10K) for deep scrolling
3. Increased minimum log read from 1MB to 8MB to populate ~60-70% of the larger scrollback buffer on first scroll

**Data model:** New `newDrainedReplayEmulator()` function and `newTrackedReplayEmulatorWithCallback()` method — used only by `renderReplay`, not by the live emulator path.

**Gotchas:**
- `SetScrollbackSize` must be called on `emu.Emulator`, not the `SafeEmulator` wrapper
- The actual scrollback capacity depends on content density — 4MB of ANSI-heavy output may produce fewer scrollback lines than 4MB of plain text

---

## 2026-03-22 — Remote Control (Vault Watcher + HTTP API)

### Data Model
- `config.APIConfig{Enabled, HTTPPort}` — new config section, DB keys `api.enabled`, `api.http_port`
- `~/.argus/api-token` — bearer token file, auto-generated on first daemon start with API enabled

### Vault Watcher (`internal/vault/watcher.go`)
- Watches `KB.ArgusVaultPath` via fsnotify for new `.md` files
- Debounces 500ms after Create/Write events (iCloud sync latency)
- Initial scan on Start() catches files created while daemon was down
- Deduplicates via `db.TasksByTodoPath()` — skips files that already have linked tasks
- Uses `Defaults.TodoProject` for project resolution; skips if not configured
- Accepts `TaskCreator` function to avoid circular import with daemon package

### HTTP API (`internal/api/`)
- REST API server on configurable port (default 7743), binds 0.0.0.0 for Tailscale
- Bearer token auth via `Authorization: Bearer <token>` header
- CORS enabled for cross-origin mobile browser access
- Port probing (tries port, port+1..port+8) same as MCP server pattern
- Endpoints: status, tasks CRUD, output (live + log fallback), input, SSE stream
- Task creation via `TaskCreator` function injection (same pattern as vault watcher)

### Headless Task Creation (`internal/daemon/headless.go`)
- Shared function `HeadlessCreateTask(db, runner, name, prompt, project, todoPath)` used by both vault watcher and API
- Same flow as TUI's `handleLaunchToDoKey`: CreateWorktree → db.Add → GenerateSessionID → runner.Start
- Default PTY size 24x80 (TUI resizes on attach)
- Reverts task to Pending on runner.Start failure (clear SessionID, zero StartedAt)

### Gotchas
- Circular import: `api` → `daemon` → `api` and `vault` → `daemon` → `vault`. Broken by injecting `TaskCreator` closures at daemon wiring time
- API binds 0.0.0.0 (not 127.0.0.1 like MCP) because it must be reachable over Tailscale
- iCloud `.icloud` placeholder files must be filtered out (they appear before the real file syncs)

## Performance Optimization Pass: 2026-03-23

### Changes
- **`RingBuffer.total` → `atomic.Uint64`** — lock-free `TotalWritten()` on Session and RemoteSession. Eliminates mutex contention from 16ms follow-up goroutine, 200ms redraw loop, 1s tick, and every `Draw()`.
- **`readLoop` per-read alloc eliminated** — `data := tmp[:n]` aliases into reusable buffer instead of `make([]byte, n)` per read. All consumers copy synchronously.
- **`readLoop` writer snapshot uses stack array** — `[4]io.Writer` avoids heap alloc for typical 0-2 writers; falls back to heap for >4.
- **`refreshPreview` TotalWritten guard** — skips 256KB `RecentOutput()` copy when output hasn't changed. Fields protected by `a.mu` (concurrent tick + cursor-change goroutines).
- **PR URL scan TotalWritten guard** — `prScanTW` map tracks per-session TotalWritten; skips 32KB tail + regex when output unchanged.
- **`rowHasContentEmu` column-0 fast path** — checks col 0 first (content there ~90% of the time), then scans remaining columns.

### Gotchas
- `len(raw)` is NOT a valid cache key for ring buffer content — once full (256KB), length is constant but content changes on every wrap. Use `TotalWritten()` instead.
- `refreshPreview` is called from both tick goroutine and `onTaskCursorChange` goroutine — any shared state must be mutex-protected.
- `readLoop` data alias (`tmp[:n]`) requires all writers to copy synchronously. An async writer storing a reference to `p` would corrupt data.

## API Settings Restart Hint: 2026-03-23

### Data Model
- `apiEnabledAtBoot bool` — snapshot of `cfg.API.Enabled` on first `Refresh()` after daemon (re)start
- `apiBootRecorded bool` — true after boot value captured; reset to false by `SetDaemonRestarting(false)`

### Flow
1. First `Refresh()` → captures `apiEnabledAtBoot`, sets `apiBootRecorded = true`
2. User toggles API → `apiEnabled` changes → `rebuildRows` appends "(restart required)" when `apiEnabled != apiEnabledAtBoot`
3. Double-toggle back to boot value → hint disappears (values match again)
4. Any daemon restart completion → `SetDaemonRestarting(false)` resets `apiBootRecorded = false` → next `Refresh()` re-anchors

### Gotchas
- `SetDaemonRestarting(false)` is the single reset point for both manual and auto-restart paths — do not add a redundant reset in `handleEnter`
- `rebuildRows` guards with `apiBootRecorded &&` to suppress the hint during the window between restart and next `Refresh()`

## Async Replay Rebuild: 2026-03-23

### Problem
`TerminalPane.Draw()` blocked the main goroutine with up to 8MB file I/O (`readLogTail`) + VT emulation (`safeEmuWrite`) when transitioning from live→replay mode. Since tview's `QueueUpdateDraw` is synchronous, all goroutines calling it (tick loop, redraw loop, git status, session exit) were frozen until Draw completed.

### Changes
- **`asyncReplayRebuild`** — background goroutine performs file I/O + VT emulation. Draw() paints stale cache while rebuild is in flight.
- **`readLogTailForTask`** — standalone function accepting taskID parameter, safe for background goroutine use (avoids race with `SetTaskID`).
- **`replayRebuildPending` flag** — signals main goroutine to reset `anchorTotalLines` and `paintCacheValid` (owned by main goroutine, written by `paintEmu` without lock).
- **Preview caching for dead sessions** — `statSessionLog` + `lastPreviewLogSize` skips redundant `os.ReadFile` of up to 95MB log files on every tick.
- **RPC goroutine leak fix** — `client.call()` timeout path now drains the channel in a background goroutine, tracked by `leakedCalls` counter.
- **Mutex protection for replay fields** — `ResetVT`, `invalidateReplayCache` now hold `tp.mu` when writing fields shared with `asyncReplayRebuild`.

### Data flow
1. `Draw()` acquires `tp.mu`, checks replay cache (fast path) or triggers `asyncReplayRebuild` (slow path)
2. Background goroutine: `readLogTailForTask` → `safeEmuWrite` → stores emulator under `tp.mu` → sets `replayRebuildPending` → `OnNeedRedraw()`
3. Next `Draw()`: consumes `replayRebuildPending` (resets anchor + paint cache), finds cache hit, paints from new emulator

### Gotchas
- `readLogTail` (method) reads `tp.taskID` without lock — only safe from main goroutine. Background goroutines must use `readLogTailForTask` with an explicit taskID parameter.
- `anchorTotalLines` and `paintCacheValid` are written by `paintEmu` (main goroutine) without lock. The background goroutine must not write them directly — use `replayRebuildPending` flag instead.
- `replayBuilding` must be reset in `invalidateReplayCache` and `ResetVT` to allow new rebuilds after cache invalidation.
- First scroll-up uses `tp.emu` (live emulator, 10K scrollback) as fallback while async replay builds. Fallback priority: stale replay emu > live emu > placeholder. Must save/restore `scrollOffset`/`anchorTotalLines` around the fallback `paintEmu` call since the smaller scrollback may clamp them.

## Remote API Skills Autocomplete: 2026-03-23

### Data Model & Flow
- `GET /api/skills?project={name}&prefix={prefix}` — new endpoint in `internal/api/handlers.go`
- Reuses `skills.LoadSkills(extraDirs)` and `skills.FilterSkills()` from `internal/skills/skills.go`
- Per-project skill dir resolved via `s.db.Projects()` → `filepath.Join(p.Path, ".claude", "skills")`
- Returns `{"skills": [{"name": "...", "description": "..."}]}` — `skillJSON` struct with `omitempty` on description

### Dashboard Integration
- Skills fetched per-project on project dropdown load/change, cached client-side in `cachedSkills[project]`
- Autocomplete triggers on `/` prefix with no spaces (same trigger logic as TUI `newtaskform.go:acTrigger`)
- Client-side filtering from cache — no per-keystroke API calls
- Keyboard nav: ArrowUp/Down, Enter to select, Escape to dismiss
- Selection inserts `/{skillName} ` into prompt textarea

## Shared PTY Sanitization: 2026-03-23

### Summary
Extracted ANSI stripping and terminal noise filtering from `internal/tui2/forkcontext.go` into `internal/sanitize/` shared package. Used by both the web API (`?clean=1` output endpoint) and fork context extraction.

### Key entities
- `sanitize.StripANSI` — comprehensive ANSI regex handling CSI (including DEC private mode `?`-prefixed), OSC, charset, keypad mode, DEC line attributes
- `sanitize.CleanPTYOutput` — full pipeline: StripANSI → normalize `\r`/NBSP → filter noise lines → clean long concatenated lines → collapse blanks
- `sanitize.cleanLongLine` (unexported) — strips inline noise from long lines where VT cell rendering concatenates content area + status bar + prompt
- `sanitize.isNoiseLine` (unexported) — 16 regex patterns for spinners, thinking, warping/clauding, status bar, separators, timing hints, etc.
- API `handleGetOutput` `?clean=1` query param — server-side sanitization for web clients
- JS `stripAnsi()` in `index.html` — defense-in-depth client-side stripping (server does the heavy lifting)

### Gotchas
- The ANSI regex must include `\x1b\[\??` (optional `?` after `[`) to catch DEC private mode sequences like `\x1b[?25l` (cursor hide) and `\x1b[?2026h` (synchronized update). The original `forkAnsiRe` had this as a separate alternation; the new `ansiRe` uses `\[\??` in a single pattern.
- `partialRenderRe` is intentionally over-broad (matches short words like "Go", "OK") because real agent content is always longer than 4 chars. Documented with a comment.
- `noOutputRe` is intentionally unanchored (matches `(No output)` anywhere in a line) for inline matching. Other noise patterns are line-anchored.

---

## Auto-Resume on Agent View Entry (2026-03-23)

When entering agent view for a task that has a preserved `SessionID` but no running session, `onTaskSelect` automatically calls `startSession` to resume the conversation. This eliminates the extra Enter press after daemon restart.

### Data model
- No new fields. Uses existing `task.SessionID` and `task.Status` as the trigger condition.

### Flow
1. User presses Enter on task → `onTaskSelect`
2. `runner.Get(taskID)` returns nil (no session) or dead session
3. Condition: `(sess == nil || !sess.Alive()) && task.SessionID != "" && task.Status != StatusComplete && !task.Archived`
4. Auto-calls `startSession(task)` which uses `--resume <sessionID>` for Claude backends

### Gotchas
- Call sites that do `onTaskSelect` + explicit `startSession` (new task, todo launch, fork) are safe only because new tasks never have a SessionID. Adding a SessionID before calling `onTaskSelect` at those sites would double-start.
- Archived tasks are excluded — an archived task with a stale SessionID should not auto-spawn an agent.
- Dead-but-not-nil sessions (`sess != nil && !sess.Alive()`) are included in the condition — the handle is stale from a previous run.

## Modal Close Focus Restoration Fix (2026-03-26)

All modal close functions must call `a.tapp.SetFocus(a.tasklist)` after removing the modal page. Without this, tview's internal focus remains on the deleted modal widget, silently dropping focus-dependent key events.

### Bug
`closeLinkPickerModal`, `closeLaunchToDoModal`, `closeCleanupToDosModal`, and `closeDeleteToDoModal` were all missing `SetFocus` calls. The link picker was most visible because it opens a browser — when the user tabs back, up/down navigation was broken. Left/right (tab switching) still worked because it's handled in `handleGlobalKey` before tview's focus-based routing.

### Fix
Added `a.tapp.SetFocus(a.tasklist)` to all four ToDo-related modal close functions, matching the pattern used by every other modal (`closeConfirmDelete`, `closeForkModal`, `closeRenameModal`, `closePruneModal`, `closeNewTaskForm`).

### Gotchas
- `a.tasklist` is always a valid focus target regardless of active tab. ToDos/Reviews/Settings handle keys via global input capture, so focusing `tasklist` is safe even when those tabs are active.

## MCP Task Management Tools: 2026-03-26

### Data Model
- `Server` gains optional `TaskCreator`, `TaskQuerier`, `TaskStopper` fields set via `SetTaskManager()`
- Same `TaskCreator` function signature as API/vault: `func(name, prompt, project, todoPath string) (*model.Task, error)`
- Task tools only appear in `tools/list` when `taskMgmtEnabled()` confirms all three deps are non-nil (graceful degradation)

### Flow
- `task_create`: validates project+prompt → rate-limited (max 5 concurrent) → calls injected `TaskCreator` → returns task ID/name/status/branch
- `task_list`: reads from `TaskQuerier.Tasks()` → filters by status/project → excludes archived
- `task_get`: reads from `TaskQuerier.Get(id)` → returns full task details including prompt
- `task_stop`: calls `TaskStopper.Stop()` directly (no TOCTOU status pre-check) → sends stop signal

### Gotchas
- Task tools reuse the `HeadlessCreateTask` path — same worktree-first, revert-on-failure semantics as API/vault
- `SetTaskManager` uses the same closure injection pattern to avoid daemon↔mcp circular import
- `task_stop` does NOT update DB status — the daemon's `onFinish` callback handles that via reconciliation

## Mouse Click Focus Guard (2026-03-29)

### Problem
tview's default `Box.MouseHandler()` calls `setFocus(self)` on any `MouseLeftDown` event. Page wrappers (TaskPage, ToDosView) contain non-interactive child panels (GitPanel, TaskPreviewPanel, TaskDetailPanel, ToDoPreviewPanel, ToDoDetailPanel) that have no `InputHandler`. Clicking these panels silently steals focus, making all keyboard input unresponsive. Users must switch tabs and back to regain focus.

### Fix
Page wrappers override `MouseHandler` with a `guardedSetFocus` that redirects all child focus requests to the interactive panel:
- `TaskPage.MouseHandler` → redirects to `tp.tasklist`
- `ToDosView.MouseHandler` → redirects to `v.list`

### Pattern
```go
func (p *PageWrapper) MouseHandler() ... {
    return p.WrapMouseHandler(func(action, event, setFocus) {
        guardedSetFocus := func(_ tview.Primitive) { setFocus(p.interactiveChild) }
        innerHandler := p.inner.MouseHandler()
        if innerHandler != nil { return innerHandler(action, event, guardedSetFocus) }
        return false, nil
    })
}
```

### Testing
Smoke test `TestSmoke_ClickNonInteractivePanelKeepsFocus` injects mouse clicks on center/right panel areas and verifies focus stays on the task list. Test confirmed the bug exists without the fix and passes with it.

### Not Vulnerable
- ReviewsView: monolithic Box, keys route through `handleGlobalKey`, no children to steal focus
- SettingsPage: custom MouseHandler that never calls setFocus
- Agent page: GitPanel/FilePanel have intentional interactive MouseHandlers

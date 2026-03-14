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

### Deferred Items for Future Sessions
- Add error handling for silently ignored `_ = m.db.Update()` calls (~15 instances in root.go)
- Handle `os.UserHomeDir()` errors in db.go and config.go
- Remove dead `store` package
- Define interfaces for DB and Runner to improve testability
- Add dedicated tests for ScrollState, borderedPanel, determinePostExitStatus (currently covered transitively)
- Goroutine leak in Session.Attach stdin copy (needs cancellation mechanism)
- Document Detect() ordering constraint in project/detect.go to prevent future signature reordering regressions

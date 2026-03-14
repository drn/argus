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

### Deferred Items for Future Sessions
- Extract shared cursor/scroll logic from TaskList/ProjectList/FileExplorer
- Add error handling for silently ignored `_ = m.db.Update()` calls
- Handle `os.UserHomeDir()` errors in db.go and config.go
- Remove dead `store` package
- Extract shared vt10x rendering from preview.go and agentview.go (note: agentview now has its own incremental path; preview still uses full replay which is fine for its read-only use case)
- Define interfaces for DB and Runner to improve testability

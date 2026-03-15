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
- Textarea starts at height 1 and auto-resizes up to `maxPromptLines` (10) based on `LineCount()` after each `Update()`. Modal grows vertically to fit.
- Enter key submits the form (newline insertion disabled via `key.NewBinding(key.WithDisabled())` on `KeyMap.InsertNewline`).
- Up/down arrows in prompt field pass through to textarea for multi-line cursor navigation instead of switching fields.
- Tab/shift+tab still switch between project selector and prompt field.
- **Zero-value trap:** `textarea.Model` has internal pointers (`viewport`, `style`) that panic on `SetWidth`/`SetHeight` when the struct is zero-valued. Root model calls `newtask.SetSize()` on `WindowSizeMsg` before the form is opened (constructed). Fixed with a nil guard checking `f.projects == nil` (always non-nil when constructed via `NewNewTaskForm`, nil at zero value).

### Deferred Items for Future Sessions
- Add error handling for silently ignored `_ = m.db.Update()` calls (~15 instances in root.go)
- Handle `os.UserHomeDir()` errors in db.go and config.go
- Remove dead `store` package
- Define interfaces for DB and Runner to improve testability
- Add dedicated tests for ScrollState, borderedPanel, determinePostExitStatus (currently covered transitively)
- Goroutine leak in Session.Attach stdin copy (needs cancellation mechanism)
- Document Detect() ordering constraint in project/detect.go to prevent future signature reordering regressions

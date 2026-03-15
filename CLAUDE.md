# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Argus is a terminal-native LLM code orchestrator built with Go + Bubble Tea. It manages multiple Claude Code / Codex sessions with task tracking, git worktree isolation, and keyboard-driven workflow.

## Build & Run

```bash
go build ./...              # build all packages
go build -o argus ./cmd/argus/  # build binary
go vet ./...                # lint
go test ./...               # run all tests
go test ./internal/db/      # run tests for a single package
```

## Architecture

**Elm Architecture (Model → Update → View)** via Bubble Tea. The entire UI is a single `tea.Program` with view switching.

- `cmd/argus/main.go` — Entry point. Parses subcommands (`daemon`, `daemon stop`), opens SQLite database. In TUI mode: tries daemon client first, falls back to in-process runner. Starts `tea.Program` with alt screen and mouse cell motion.
- `internal/ui/root.go` — **Top-level Bubble Tea model**. Owns all sub-views and routes key events based on current view state (`viewTaskList`, `viewNewTask`, `viewHelp`, `viewPrompt`, `viewConfirmDelete`). This is the orchestration hub.
- `internal/ui/worktree.go` — Git worktree discovery, cleanup, and process management helpers. Extracted from root.go to separate infrastructure concerns from UI logic.
- `internal/ui/tasklist.go` — Task list with collapsible project folders, cursor, scrolling, filtering. Tasks are grouped by project name into a flattened row list (project headers + task rows). Only one project is expanded at a time — auto-expands when the cursor enters a project, auto-collapses others. Cursor navigation skips project header rows entirely (`moveCursor`) — the cursor always lands on a task row, never a header. When navigating up across projects, cursor lands on the *last* task of the previous project. Not a `tea.Model` itself — it's a plain struct that `root.Model` drives.
- `internal/ui/panellayout.go` — Shared `PanelLayout` struct for horizontal multi-panel layouts. Handles percentage-based width splitting with minimums, right-to-left compression when narrow, height padding, and horizontal joining. Used by both `AgentView` and the task list view with identical 20/60/20 ratios to ensure visual consistency.
- `internal/ui/newtask.go` — New task form using `bubbles/textinput`. Has its own `Update` method but is called by root.
- `internal/model/` — Core domain types. `Task` struct and `Status` enum with `pending → in_progress → in_review → complete` workflow. Status implements `encoding.TextMarshaler` for JSON serialization.
- `internal/db/` — SQLite-backed persistence at `~/.argus/data.sql`. Stores tasks, projects, backends, and config in a single database. Thread-safe with mutex. Seeds defaults on first run.
- `internal/config/config.go` — Config struct types and defaults. Struct types (`Config`, `Backend`, `Project`, `Keybindings`, `UIConfig`) are used throughout the codebase as value types. The `db.DB.Config()` method assembles a `Config` from the database.
- `internal/agent/` — Agent process management with PTY:
  - `agent.go` — Backend resolution and command building (`BuildCmd`). Supports `--session-id` for conversation pinning.
  - `worktree.go` — Git worktree creation under `~/.argus/worktrees/<project>/<task>` with `argus/<task>` branch naming.
  - `iface.go` — `SessionProvider` (manages sessions) and `SessionHandle` (single session) interfaces. UI code depends only on these interfaces, enabling both in-process and daemon-backed implementations.
  - `session.go` — PTY-backed process session via `creack/pty`. Single `readLoop` goroutine tees output to ring buffer + all attached writers. Multi-writer support via `AddWriter`/`RemoveWriter` for fan-out to multiple consumers. Supports attach/detach without stopping the process.
  - `runner.go` — Multi-session manager keyed by task ID. Implements `SessionProvider`. Start/Stop/Get/Attach/Detach. Auto-cleans up on process exit, fires `onFinish` callback.
  - `attach.go` — `AttachCmd` implements `tea.ExecCommand` for Bubble Tea integration. Sets raw terminal mode, resizes PTY, uses detachReader to intercept `ctrl+q` for detach.
  - `ringbuffer.go` — Exported `RingBuffer` — fixed-size circular buffer for output replay on reattach. Used by both in-process sessions and daemon client's local buffer.
  - `errors.go` — Sentinel errors.
- `internal/daemon/` — Daemon architecture for persistent agent sessions:
  - `daemon.go` — `Daemon` struct: owns Runner, accepts Unix socket connections, dispatches RPC vs stream (first byte 'R'/'S'). PID file at `~/.argus/daemon.pid`. Signal handling (SIGTERM/SIGINT → graceful shutdown).
  - `types.go` — Shared RPC request/response types (`StartReq`, `SessionInfo`, `StreamHeader`, etc.).
  - `rpc.go` — `RPCService` implementing JSON-RPC methods: Ping, StartSession, StopSession, StopAll, SessionStatus, ListSessions, WriteInput, Resize, Shutdown.
  - `stream.go` — Output streaming handler. Client sends `StreamHeader` JSON, daemon calls `AddWriter(conn)` on the session. Raw bytes flow until session exit or client disconnect.
- `internal/daemon/client/` — TUI-side daemon client:
  - `client.go` — `Client` implementing `SessionProvider` via JSON-RPC to daemon. Manages `RemoteSession` lifecycle.
  - `handle.go` — `RemoteSession` implementing `SessionHandle`. Local `RingBuffer` populated by stream reader. RPC calls for WriteInput, Resize, PTYSize, etc.
  - `stream.go` — Goroutine reads raw bytes from daemon stream connection into local ring buffer.

**Key pattern:** Sub-views (`TaskList`, `StatusBar`, `HelpView`) are plain structs with `View() string` methods — not independent `tea.Model`s. Only `NewTaskForm` has its own `Update` because it manages text input focus. Root model coordinates everything.

**Agent pattern:** A single `readLoop` goroutine is the sole reader of the PTY master fd. It always writes to the ring buffer, and tees output to all attached writers (via `session.writers` slice). Writers are copied under lock before iterating; errored writers are removed automatically. `AddWriter(w)` replays the ring buffer then registers for live output. `Attach()`/`Detach()` use AddWriter/RemoveWriter internally. The detach key (`ctrl+q`) is intercepted by `detachReader` wrapping stdin.

**Daemon pattern:** The daemon (`argus daemon`) owns the Runner and PTY sessions. The TUI connects via Unix socket (`~/.argus/daemon.sock`). First byte on each connection selects the protocol: 'R' for JSON-RPC (request/response), 'S' for output streaming (raw bytes). The TUI's `Client` implements `SessionProvider` so the UI code is identical whether running in-process or via daemon. Sessions survive TUI restarts — the daemon keeps PTY fds alive until explicit stop or shutdown.

**Task/worktree lifecycle:** New task form submission → `handleNewTaskKey` creates worktree BEFORE persisting the task: `agent.CreateWorktree(projDir, project, taskName, branch)` creates worktree at `~/.argus/worktrees/<project>/<task>` with branch `argus/<task>`. If worktree creation fails, the task is NOT created — the form stays open with the error message. On name conflict (directory exists), `CreateWorktree` auto-suffixes with `-1`, `-2`, etc. Only after worktree succeeds: `db.Add(task)` → `startOrAttach` generates session ID → `runner.Start` builds command with `cmd.Dir = t.Worktree` → captures PID in DB. On delete/destroy: stops agent → `removeWorktreeAndBranch(path, branch, repoDir)` removes worktree (via `git worktree remove` from repoDir) → deletes local branch → deletes remote branch → removes from DB.

**Git status pattern:** Git operations (worktree discovery, diff, status) must **never** run synchronously on the UI thread. The `resolvedDirs` cache on `Model` stores task ID → worktree dir mappings. On cache hit, `scheduleGitRefresh()` kicks off `FetchGitStatus` as a `tea.Cmd`. On cache miss, it fires `resolveTaskDirAsync()` which returns a `ResolveTaskDirMsg` — only then does the git status fetch begin. This two-phase async pattern keeps the UI responsive. Maps are reference types in Go, so the cache works correctly even through Bubble Tea's value-receiver `Update` method.

## Config & Persistence

- Data dir: `~/.argus/`
- Database: SQLite (`data.sql`) via `modernc.org/sqlite` (pure Go, no CGO)
- Backends are command templates with prompt flag interpolation, not SDK integrations

## Breaking Changes Policy

- Only one user (the author) — breaking changes are fine, no backwards compatibility needed
- No legacy migration code — if a schema change requires data migration, write a one-off script
- `internal/store/` (legacy JSON persistence) and `config.toml` support have been removed

## Key Learnings

- PTY child processes need a real terminal size at launch (`pty.StartWithSize`), not 0x0. TUI apps like claude won't render with zero dimensions.
- Use `charmbracelet/x/term` for raw mode (cross-platform) instead of platform-specific ioctls (`TIOCGETA` is macOS-only, `TCGETS` is Linux-only).
- `tea.ExecCommand.SetStdin/SetStdout` must capture and use Bubble Tea's `p.input`/`p.output` — don't hardcode `os.Stdin`/`os.Stdout`.
- The single-reader-tee pattern (one goroutine reads PTY, tees to buffer + optional writer) is critical. Two goroutines reading the same fd causes data loss.
- **Never run git commands or filesystem discovery synchronously in `Update()`.** Even fast git commands take 50-500ms which freezes the entire TUI. Use `tea.Cmd` to run them in background goroutines and deliver results via messages. Cache resolved paths in a `map` on the model to avoid repeated lookups.
- **Backend default config must be self-healing.** The default claude backend command MUST include `--dangerously-skip-permissions`, MUST NOT use `-p` as the prompt flag, and MUST NOT include `--worktree` (Argus manages worktrees itself via `git worktree add`). `-p` puts Claude in non-interactive print mode (process prompt → exit), which silently breaks agent sessions. The `fixupBackends()` method in `internal/db/migrate.go` runs on every `Open()` to detect and repair outdated backend configs. Any change to `DefaultConfig()` backend values must be mirrored in `fixupBackends()` detection logic, or existing databases will retain stale values.
- **Use incremental vt10x feeding, not full replay.** The agent view's terminal panel uses a persistent `vt10x.Terminal` that receives only new bytes each tick (`renderIncremental`), not the entire 256KB ring buffer. Full replay is O(buffer_size) and causes input lag when the buffer is large. The persistent terminal is reset on resize, task switch, or ring buffer wrap. The full-replay path (`formatTerminalOutput`) is only used for scrollback mode and finished sessions.
- **Sub-view structs that need mutation must be pointers.** Bubble Tea's `Update` uses a value receiver, so helper methods with value receivers (like `scheduleGitRefresh`) get a copy of the model. Mutations to value-type fields inside those helpers are silently lost. Any sub-view struct that is mutated outside the direct `Update` method body must be stored as a pointer (e.g., `gitstatus *GitStatus`, `agentview *AgentView`). Fields that are only read (or only mutated directly in `Update`) can stay as values. Maps (`resolvedDirs`) are already reference types and work correctly.
- **`keyMsgToBytes` must preserve the Alt modifier for all key types.** When converting `tea.KeyMsg` to raw terminal bytes, check `msg.Alt` and prepend ESC (`0x1b`) for every key category — runes, arrows, and special keys (`keyByteMap`). Dropping Alt silently breaks macOS Option+Delete (word delete), Option+arrows (word movement), and other Alt-modified shortcuts. The `altArrowMap` handles arrows with dedicated CSI sequences; all other keys use ESC-prefixed encoding.
- **Use `msg.Type` not `msg.String()` for Bubble Tea key matching.** String comparison fails when terminals encode modifier keys differently (e.g., urxvt sends `\x1b[Od` for ctrl+left, which Bubble Tea parses as `KeyCtrlLeft` with `Alt=true` → `String()` returns `"alt+ctrl+left"` instead of `"ctrl+left"`). Always match on `msg.Type` and check `msg.Alt` separately. For agent view pane switching, Cmd+left/right (sent as Alt+left/right by macOS terminals) is the binding — the git status panel is not focusable, so focus toggles between terminal and files only. Plain left/right arrows are passed through to the agent process.
- **`textarea.Model` panics when zero-valued; `textinput.Model` does not.** The `textarea.Model` has internal pointers (`viewport`, `style`) that are nil at zero value — calling `SetWidth` or `SetHeight` on an uninitialized textarea causes a nil pointer dereference. Any form struct using `textarea.Model` must guard `SetSize`-like methods against being called before the constructor runs (e.g., check a non-nil field like `projects`). This matters because root model's `WindowSizeMsg` handler calls `SetSize` on all sub-views, including zero-valued ones that haven't been opened yet.
- **`removeWorktree` must validate paths before `os.RemoveAll`.** The `os.RemoveAll` fallback in `removeWorktree` will nuke any directory if `git worktree remove` fails — including the root project if `t.Worktree` is incorrectly set. The `isWorktreeSubdir` guard ensures the path contains `/.argus/worktrees/` or `/.claude/worktrees/` before any removal. Any new cleanup code that calls `os.RemoveAll` on user-provided paths must have a similar safety check.
- **Use `msg.Button` not `msg.Type` for Bubble Tea mouse events.** The `tea.MouseMsg.Type` field and constants like `tea.MouseWheelUp` are deprecated in bubbletea v1.3+. Use `msg.Button` with `tea.MouseButtonWheelUp`/`tea.MouseButtonWheelDown` instead. Mouse events are routed through root `Update()` as `tea.MouseMsg` — the root dispatches to the appropriate sub-view based on `m.current`.
- **Don't clamp scroll offset against empty cached state.** The agent view's `scrollUp` must not clamp `scrollOffset` to 0 when `cachedLines` is empty. In incremental render mode, `cachedLines` is only populated by `formatTerminalOutput`, which is only called when `scrollOffset > 0`. Clamping to 0 when the cache is empty creates a chicken-and-egg deadlock where scrolling can never start. Let the offset grow freely when the cache is empty — the render path handles over-scroll gracefully via `windowLines`, and subsequent scrolls will clamp correctly once `cachedLines` is populated.
- **`textarea.LineCount()` only counts hard newlines, not soft wraps.** A long line that visually wraps to 3 lines still reports `LineCount() == 1`. For auto-resize modals with single hard lines (enter disabled), use `LineInfo().Height` which returns the exact wrapped line count computed by the textarea's own internal `memoizedWrap` — no reimplementation needed. Do NOT use character-count division (`runeLen/width`); the textarea uses word wrapping (breaks at word boundaries), so character-based counting underestimates. For multi-line pasted content, fall back to `maxPromptLines`. Also use `LineInfo().RowOffset` and `.Height` to detect when the cursor is at the first/last visual line for arrow-key navigation out of the textarea.
- **Use `ansi.Hardwrap` not `ansi.Truncate` for panel content that should wrap.** The `charmbracelet/x/ansi` package provides both: `Truncate` clips lines at a width (good for single-line labels), while `Hardwrap` inserts newlines at the width boundary preserving ANSI escape sequences (good for multi-line content panels like diff views). When using `Hardwrap`, pre-compute and cache the wrapped lines (`diffWrappedLines`/`diffWrapWidth` pattern) since wrapping is O(content_size) and the width only changes on resize. Invalidate the cache when content changes or width changes. Scrolling must operate on visual (wrapped) lines, not source lines.
- **`textarea.SetHeight()` does not reset the viewport scroll offset.** The textarea's `Update()` calls `repositionView()` at the end, which scrolls the viewport to keep the cursor visible based on the *current* viewport height. If the viewport height is too small (e.g., 1), it scrolls down to follow the cursor. `SetHeight()` only changes `viewport.Height` — it does NOT call `repositionView()`, so the stale `YOffset` persists. For auto-resizing textareas, always expand the height to the maximum BEFORE calling `textarea.Update()`, then shrink it back to the actual needed height afterward. This prevents `repositionView()` from scrolling unnecessarily. Also note: the textarea's `wrap()` function uses `>=` (not `>`) for width comparison, so text that exactly fills the width will wrap to the next line.
- **`⌘` (U+2318) is double-width in terminals but `go-runewidth` reports 1.** Any status bar or layout that includes `⌘` must add `strings.Count(s, "⌘")` to the `lipgloss.Width()` result to compensate. Without this, gap math underestimates the rendered width and the bar overflows. Check for similar mismatches when adding other Unicode symbols to the UI — test with `runewidth.RuneWidth()` and compare against actual terminal rendering.
- **All `View()` paths must handle zero dimensions without panicking.** Bubble Tea calls `View()` before delivering the first `WindowSizeMsg`, so `m.width` and `m.height` are both 0 on the initial render. Any arithmetic like `m.height - N` produces a negative value — passing that to slice expressions or layout functions causes panics. Every function that receives a computed height/width must guard `<= 0` at the top (return empty string or no-op). The `padToBottom` helper already had this guard; `padHeight` and any future layout helpers must too.
- **XOR the reverse bit for vt10x cursor rendering, not OR.** The `vt10x` library pre-swaps FG/BG in `setChar()` for reverse-video cells, so adding SGR 7 via OR on an already-reverse cell double-reverses back to normal (invisible cursor). Using XOR (`cell.Mode ^ vtAttrReverse`) toggles reverse off for those cells — since stored FG/BG are already visually swapped, removing reverse produces the correct inverted appearance. For normal cells, XOR adds reverse as expected. Don't hardcode explicit cursor colors (e.g., black-on-white) — use SGR reverse with default colors so the cursor inherits the terminal's theme.
- **Self-perpetuating tick chains must have exactly one starting point.** A `tea.Tick` handler that schedules the next tick creates a self-sustaining chain. If *any other* handler also schedules that same tick type (e.g., a slower parent tick, a view-enter function), you get N parallel chains accumulating over time — each one triggers a full `View()` re-render, causing progressive lag that grows linearly with time spent in the view. **Rule:** each tick message type should be started from exactly one place (typically view entry), and the self-perpetuating handler is the only thing that reschedules it. Audit all `tea.Tick` calls in the codebase when adding a new tick type to ensure no accidental duplication. The symptom of this bug is progressive input lag that temporarily clears when switching views (accumulated tick messages drain from the queue).

- **All panels in `PanelLayout.Render()` must enforce their own width.** `PanelLayout.Render()` only pads height and joins horizontally — it does NOT enforce column widths. If a panel's content is narrower than its allocated width, the panel collapses to content width and the layout breaks visually. Every panel passed to `Render()` must use `borderedPanel(width, height, ...)` or `lipgloss.NewStyle().Width(w)` to fill its allocation. The agent view panels already do this via `borderedPanel`; the task list view's left pane was missing it.
- **Worktree creation must succeed before task persistence.** Never `db.Add(task)` without a valid worktree. If `CreateWorktree` fails, keep the new task form open with the error — do NOT fall back to running in the project directory or delegating to `--worktree`. The `ResolveTaskDirMsg` handler must only persist paths that pass `isWorktreeSubdir()` to prevent project directories from being saved as `t.Worktree`. `CreateWorktree` returns `(wtPath, finalName, err)` — the `finalName` may differ from the input if name conflicts required a `-1`, `-2` suffix.
- **Map lookups returning `*T` become non-nil interfaces.** When a method returns an interface (e.g., `Get(id) SessionHandle`) and the underlying implementation uses a map (`sessions[id]`), a missing key returns `nil *Session`. Assigning this to an interface gives a **non-nil interface** with a nil concrete value — `== nil` checks fail. The `Get` method must explicitly check `if sess == nil { return nil }` before returning. This applies to any method on Runner/Client that returns `SessionHandle`.
- **`AddWriter` must replay before registering.** When adding a writer to a session, send the ring buffer replay BEFORE appending the writer to the `writers` slice. If you register first, `readLoop` can deliver live bytes to the writer before replay is sent, causing duplicate data. The correct order is: snapshot replay → send replay → register writer. This creates a small gap (bytes produced during replay are missed) rather than duplicates. Gaps are handled gracefully by vt10x (partial escape sequences ignored); duplicates cause visible rendering corruption.

## Development Rules

### Testing Requirements

- **Every change must include tests.** When adding new functionality, fixing bugs, or refactoring, always add or update corresponding tests. Run `go test ./...` to verify all tests pass before considering work complete.
- **Run `go test ./... -cover` after writing tests** to verify coverage improved. Aim for ≥80% coverage on any package you touch.
- **Test file placement:** Tests go in `*_test.go` files in the same package (not `_test` suffix packages). Use the existing `testModel(t)`, `testDB(t)`, and similar helpers.
- **What to test:**
  - All exported functions and methods
  - Pure logic functions (parsers, state transitions, builders) — these are easy to test, no excuses
  - View/render functions — verify they return non-empty output and contain expected content strings
  - Edge cases: nil inputs, empty collections, boundary values, error paths
  - State machines: status transitions, cursor navigation, focus cycling
  - **Zero-dimension View() invariant:** When adding a new view or modifying a `View()` code path, add a subtest to `TestModel_ViewZeroDimensions` in `root_test.go`. This test calls `View()` with `width=0, height=0` (before `WindowSizeMsg`) and must not panic.
- **What's okay to skip:** Functions requiring a real terminal (raw mode, ioctl), functions that shell out to external processes (git commands, process management), and `cmd/argus/main.go` (entry point)
- **Testing patterns in this codebase:**
  - `db.OpenInMemory()` for database tests (no filesystem needed)
  - `agent.NewRunner(nil)` for UI tests that need a runner but don't start processes
  - `exec.Command("echo", "hello")` or `exec.Command("sleep", "10")` for agent/session tests
  - `DefaultTheme()` for any UI component tests
  - Table-driven tests with `[]struct{ input, expected }` for functions with many cases

## Context Directory

- `context/` stores durable cross-session knowledge checked into git
- `context/knowledge/index.md` is the knowledge graph index — read it when you need project history or domain context
- `context/research/` holds investigation notes and spike results
- `context/plans/` holds strategic plans and proposals

## Planned but Not Yet Implemented

- Task import from markdown/JSON (`internal/import/`) — Phase 4

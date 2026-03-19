# Plan: Native Terminal Passthrough for Agent View

**Date:** 2026-03-18
**Source:** Inline request: "research well-maintained libraries that do this cleanly. then write an implementation plan"
**Status:** In Progress
**Current Phase:** Phase 6 (x/vt migration + Bubble Tea removal)

## Goal

Replace the current replay-and-repaint terminal path in Argus with a native terminal surface for the live agent pane so upstream PTY UX, cursor behavior, and prompt styling render directly without manual row highlighting or cursor synthesis, using `tcell` for screen ownership and `tview` for higher-level shell primitives.

## Background

Argus currently runs agent sessions over a PTY and then re-renders the screen inside Bubble Tea:

- `internal/agent/session.go` starts the PTY and stores a ring buffer of raw output.
- `internal/ui/agentview.go` reads recent bytes, replays them through `vt10x`, and converts the resulting screen back into ANSI strings for the center panel.
- `internal/ui/vtrender.go` injects Argus-owned semantics such as the active input row tint, cursor repainting, and trimming of parked/empty rows.

That architecture works for preview and normalized display, but it is not a true terminal passthrough. The live pane is a reconstructed view, not the agent's own terminal surface.

Library research from primary sources points to the following:

- Bubble Tea is mature and well-maintained, but its core model remains string-rendered (`View() string`), which makes true embedded-terminal passthrough awkward rather than natural.
- `github.com/gdamore/tcell/v2` is the most mature low-level Go screen/event library. It gives the right primitives for screen ownership, cursor control, mouse handling, and event polling.
- `github.com/rivo/tview` is a higher-level widget library built on `tcell` with layouts, lists, forms, tables, pages, and modals. It is the closest conservative replacement for the Bubble Tea shell primitives Argus uses today.
- `github.com/charmbracelet/x/vt` is a modern Charm terminal emulator with `Write`, `Draw`, cursor, damage tracking, and alt-screen support. It is promising for emulation and preview, but by itself it is not a full application runtime.
- `github.com/creack/pty` remains the right PTY layer and should stay.
- `github.com/hinshun/vt10x` is serviceable for replay, but it is older and should not be the long-term live-pane foundation.

Recommendation:

- Do not attempt a "true passthrough" center pane while Bubble Tea still owns the final screen rendering path for that pane.
- Keep Bubble Tea only if we accept a semantic renderer, not real passthrough.
- If true passthrough is the goal, migrate the UI runtime to `tcell` and use `tview` for the non-terminal shell while preserving existing Argus domain packages (`agent`, `daemon`, `db`, `github`).

## Requirements

### Must Have

- Preserve raw PTY semantics in the live agent pane: upstream colors, cursor, wrapped rows, alt-screen behavior, and prompt styling.
- Remove manual prompt-row background injection from the live-pane path.
- Preserve existing task/session behavior in `internal/agent`, `internal/daemon`, and `internal/db`.
- Preserve the current three-panel workflow: git status, agent terminal, file explorer.
- Preserve task switching, detach flow, resize handling, and key forwarding to the PTY.
- Keep session persistence and post-exit log replay.

### Should Have

- Retain the existing preview/task-list ANSI rendering path with a smaller replay stack.
- Keep current keybindings and focus behavior as close as possible during migration.
- Ship behind a runtime flag so Bubble Tea remains available as fallback during rollout.
- Improve terminal correctness around cursor parking, scrollback, and wide-character handling.

### Won't Do (this iteration)

- Rebuild the daemon, agent session model, or worktree model.
- Replace `creack/pty`.
- Attempt a mixed ownership model where Bubble Tea and a live terminal pane both paint the same screen region.
- Port every non-agent view to a new UI runtime in the first milestone.

## Technical Approach

The clean path is a runtime split and staged migration:

1. Extract a UI-agnostic application layer from the current Bubble Tea-heavy presentation code.
2. Introduce a second UI runtime built on `tcell`, using `tview` for high-level application chrome and a custom PTY terminal primitive for the live agent pane.
3. Keep the current replay renderer only for previews, snapshots, and fallback mode.
4. Cut the live agent pane over first, then port the remaining screens to the new runtime once terminal ownership is proven out.

This avoids trying to force a real terminal surface through a `View() string` API that was not designed for it.

## Decisions

| Decision | Rationale |
|----------|-----------|
| Use `tcell` as the target screen/runtime layer and `tview` as the shell/widget layer | This keeps the stack conservative: mature screen ownership plus common layout/widgets without betting on a smaller runtime ecosystem |
| Keep `creack/pty` for PTY allocation and resize | The PTY layer is already correct and independent of the renderer |
| Keep a replay/emulation path for previews and finished-session output | Task-list preview and offline log rendering still benefit from deterministic replay |
| Treat `x/vt` as a possible replacement for `vt10x` in replay code, not as the full live-pane solution | Emulation is only one part of the problem; the bigger issue is screen ownership |
| Do not try to embed a native terminal pane inside Bubble Tea's string rendering model | Mixed ownership would be fragile around cursor placement, repaint order, focus, and alternate screen behavior |
| Migrate the agent view first under a feature flag | It is the highest-value surface and isolates terminal correctness from the rest of the app |

## Implementation Steps

### Phase 1: Isolate Terminal Concerns From Bubble Tea
**Status:** complete

- [x] Add `context/research/terminal-runtime-notes.md` — capture the library comparison, fit, and rejection reasons for Bubble Tea-only passthrough
- [x] Introduce a UI-agnostic agent-view state model under `internal/app/agentview` — scroll/focus/session display state, diff state, git refresh timing
- [x] Extract PTY input translation into `TerminalAdapter` interface and `SessionLookup` for session resolution
- [x] Define runtime boundary: `Panel` type, `DiffState`, `State` struct with focus/scroll/diff methods; `UIRuntime` type with `DetectRuntime()`
- [x] Add a runtime switch (`ARGUS_UI_RUNTIME=bubbletea|tcell`) with Bubble Tea as the default, wired in `cmd/argus/main.go`

### Phase 2: Build a Tcell/Tview App Shell
**Status:** complete

- [x] Add a new `tcell`/`tview` entrypoint — feature-gated `runTcell()` in `cmd/argus/main.go`, selected via `ARGUS_UI_RUNTIME=tcell`
- [x] Implement the top-level layout shell in `internal/tui2`: `Header` (tab bar), `StatusBar` (bottom hints + counts), `TaskListView` (grouped by project with archive), `AgentPane` (placeholder), `SidePanel` (git/files)
- [x] Map current Bubble Tea primitives onto `tview` primitives: `tview.Pages` for view switching, `tview.Flex` for layout, custom `tview.Box`-based widgets for task list / agent pane / panels
- [x] Port the global navigation model: tab switching (1/2/3), quit (q/ctrl+c), agent detach (ctrl+q/esc), daemon connectivity check on tick, task refresh on tick
- [x] Mirror the existing task-selection and agent-attach flow: Enter on task → agent view with session lookup, `startOrAttach` for session lifecycle, `tcellKeyToBytes` for PTY input forwarding
- [x] Add test coverage: 18 tests covering app creation, tab switching, task selection, agent view enter/exit, key-to-bytes conversion, PTY sizing, task list navigation, row building, archive detection

### Phase 3: Replace the Live Agent Pane With a Native Terminal Surface
**Status:** complete

- [x] Add `internal/tui2/terminalpane.go` — custom `tview.Box`-based widget with native PTY rendering via vt10x cells → tcell.Screen cells, cursor display, input row highlighting, and scrollback
- [x] Connect `TerminalPane` directly to `agentview.TerminalAdapter` (satisfied by `agent.SessionHandle`) for output and resize
- [x] Route keyboard events directly to PTY via `tcellKeyToBytes` with scrollback interception (Shift+arrows, PgUp/PgDn)
- [x] Preserve log replay for completed sessions via `ReplayVT10X` + `drawANSILine` ANSI→tcell parser
- [x] Exported `ReplayVT10X` and `EstimateVTRows` from `internal/ui/vtrender.go` for use by tui2 scrollback; kept `findInputRow` and cursor synthesis for Bubble Tea fallback

### Phase 4: Keep Replay Rendering Only Where It Still Adds Value
**Status:** complete

- [x] Kept `vt10x` for preview and offline rendering (stability — no migration to `x/vt`)
- [x] Preserved Bubble Tea `agentview.go` with scoping comments as fallback runtime
- [x] Scoped `vtrender.go` header documentation to preview/replay/fallback only
- [x] Existing tests for prompt-row tinting and cursor synthesis unchanged (they test the Bubble Tea fallback path)

### Phase 5: Port the Remaining Shell and Cut Over
**Status:** complete

- [x] Ported git status panel (`internal/tui2/gitstatus.go` — `GitPanel` with status/diff/branch sections)
- [x] Ported file explorer (`internal/tui2/fileexplorer.go` — `FilePanel` with auto-expand, cursor nav, status icons)
- [x] Ported new task form (`internal/tui2/newtaskform.go` — modal with project/backend selectors, prompt input)
- [x] Preserved keymap semantics: Ctrl+Q detach, Shift+arrows scroll, Ctrl/Alt+arrows panel switch, o/Ctrl+P PR open, j/k/Enter file nav
- [x] Added regression tests: 40+ tests across all tui2 components (terminalpane, newtaskform, fileexplorer, gitstatus, app, tasklist)
- [x] Bubble Tea remains the default runtime for stability; tcell available via `ARGUS_UI_RUNTIME=tcell`
- [x] Reviews and Settings tabs remain as stubs with error messages (complex views deferred to separate work)

### Phase 6: Replace vt10x with x/vt in TerminalPane
**Status:** not started

- [ ] Replace `vt10x.New(vt10x.WithSize(w, h))` → `vt.NewSafeEmulator(w, h)` in `internal/tui2/terminalpane.go`
- [ ] Replace `vt.Cell(x, y)` → `emulator.CellAt(x, y)` (returns `*uv.Cell`)
- [ ] Replace `cellStyle(vt10x.Glyph)` → new `uvCellToTcellStyle(*uv.Cell)` using `tcell.FromImageColor()`
- [ ] Replace cursor access: `vt.Cursor()` → `emulator.CursorPosition()`
- [ ] Remove `vt.Lock()/Unlock()` — SafeEmulator is thread-safe
- [ ] Use `emulator.Scrollback()` for scrollback instead of replaying full buffer through a tall terminal
- [ ] Use `emulator.Touched()` for incremental redraws — only repaint lines that changed
- [ ] Update tests in `internal/tui2/terminalpane_test.go`
- [ ] `go get github.com/charmbracelet/x/vt github.com/charmbracelet/ultraviolet`

### Phase 7: Extract shared utilities from internal/ui/
**Status:** not started

New package: `internal/gitutil/`

Move pure-Go files with zero charmbracelet imports:
- [ ] `internal/ui/gitcmd.go` → `internal/gitutil/gitcmd.go` (FetchGitStatus, FetchFileDiff, FetchDirFiles, message types)
- [ ] `internal/ui/scrollstate.go` → `internal/gitutil/scrollstate.go` (ScrollState)
- [ ] `internal/ui/diffparse.go` → `internal/gitutil/diffparse.go` (ParseUnifiedDiff, BuildSideBySide, types)
- [ ] `internal/ui/skills.go` → `internal/gitutil/skills.go` (SkillItem, skill loading)
- [ ] `internal/ui/worktree.go` → `internal/gitutil/worktree.go` (worktree discovery, cleanup)
- [ ] Extract `ChangedFile`, `ParseGitStatus`, `ParseGitDiffNameStatus`, `MergeChangedFiles` from `internal/ui/fileexplorer.go` to `internal/gitutil/changedfiles.go`
- [ ] Update all imports in `internal/tui2/` and `internal/ui/`

### Phase 8: Port Reviews tab to tcell
**Status:** not started

New file: `internal/tui2/reviews.go`

- [ ] Three-panel layout (PR list | diff | comments) using `tview.Flex`
- [ ] PR list with "Review Requests" first, "My Open PRs" second, cursor navigation, status badges
- [ ] Diff viewer with scroll, syntax highlighting (reuse `parseDiffLines` or `gitutil.ParseUnifiedDiff`)
- [ ] Comments list + compose box
- [ ] Data flow: `FetchPRList()` → goroutine → `QueueUpdateDraw`; PR select → fetch files + comments in parallel
- [ ] Cooldown: 10-min PR list, 2-min comments TTL
- [ ] Key bindings: up/down/j/k, Enter, tab focus, s split, c comment, a approve, r request changes, R refresh, esc back

### Phase 9: Port Settings tab to tcell
**Status:** not started

New file: `internal/tui2/settings.go`

- [ ] Two-panel layout (section list | detail)
- [ ] Sections: STATUS, SANDBOX, PROJECTS, BACKENDS, KNOWLEDGE BASE
- [ ] Cursor skips section headers, detail panel shows selected item config
- [ ] Key bindings: up/down navigate, 'n' new, 'e' edit, 'd' delete/default, Enter select
- [ ] Forms as modal overlays via `tview.Pages.AddPage`

### Phase 10: Port remaining forms to tcell
**Status:** not started

- [ ] `internal/tui2/projectform.go` — add/edit project (name, path, branch, backend, sandbox settings)
- [ ] `internal/tui2/backendform.go` — add/edit backend (name, command, prompt flag)
- [ ] `internal/tui2/renametask.go` — rename task (single text input)
- [ ] All follow modal pattern: `tview.Box` with `InputHandler`, added/removed as page

### Phase 11: Delete Bubble Tea runtime + clean deps
**Status:** not started

- [ ] Delete `internal/ui/` entirely (~60 files)
- [ ] Remove BT wiring from `cmd/argus/main.go` (delete `runTUI()`, remove runtime switch)
- [ ] Delete `internal/app/agentview/runtime.go` and `runtime_test.go`
- [ ] Remove from go.mod: `bubbletea`, `bubbles`, `lipgloss`, `hinshun/vt10x`
- [ ] Keep: `charmbracelet/x/vt`, `charmbracelet/x/ansi`
- [ ] `go mod tidy` → `go build ./... && go vet ./... && go test ./...`
- [ ] Verify: `grep -r "charmbracelet/bubbletea\|charmbracelet/bubbles\|charmbracelet/lipgloss\|hinshun/vt10x" go.mod` → zero matches

### Phase 12: Wire daemon restart + in-process session exit
**Status:** not started

- [ ] `App.RestartedClient()` returns the new client after daemon restart
- [ ] Wire `onFinish` callback in `runTcell()` for in-process mode
- [ ] Daemon health check → auto-restart → reconnect sessions (mirror BT's `DaemonRestartedMsg` flow)

## Testing Strategy

- Keep `go test ./...` green throughout the extraction and migration.
- Add focused adapter tests for PTY resize, key forwarding, scrollback, and session replay.
- Create manual smoke scripts for terminal-specific behaviors that unit tests miss: alt-screen apps, mouse reporting, cursor visibility, long wrapped prompts, and Unicode/wide glyphs.
- Test both runtimes behind the feature flag until cutover: Bubble Tea fallback and `tcell` target runtime.
- Verify that task-list previews and completed-session output remain stable while live rendering changes underneath.

## Risks & Open Questions

| Risk | Mitigation |
|------|------------|
| The `tcell` migration is materially larger than a renderer refactor | Ship in phases and cut over the agent pane first behind a runtime flag |
| Reusing current UI logic is harder than expected because `internal/ui` mixes state and presentation | Extract agent/session state into runtime-agnostic packages before porting widgets |
| Terminal behavior differs across macOS/Linux terminals in ways the current replay path hides | Add manual smoke coverage for resize, mouse, alternate screen, and Unicode cases early |
| A mixed Bubble Tea/`tcell` runtime is tempting and causes dead-end complexity | Keep the separation explicit: Bubble Tea fallback runtime versus `tcell` target runtime |
| Preview rendering drifts from live-pane rendering semantics | Deliberately treat preview as a separate product surface with its own acceptance criteria |

- Is a runtime split with `cmd/argus-tcell` acceptable during migration, or must the switch stay inside one binary?
- Do we want the task list and non-agent screens to remain Bubble Tea for a transitional period, or should the whole shell move once the agent pane works?
- Should replay rendering stay on `vt10x` for stability, or should it move to `x/vt` once the live pane no longer depends on it?
- Do we need Windows support for the terminal-surface migration, or can the first pass remain Unix-first like the existing PTY behavior?

## Dependencies

- Current PTY/session layer in [`internal/agent/session.go`](/Users/darrencheng/.argus/worktrees/argus/codex-prompt-line-manually/internal/agent/session.go)
- Current Bubble Tea app shell in [`internal/ui/root.go`](/Users/darrencheng/.argus/worktrees/argus/codex-prompt-line-manually/internal/ui/root.go)
- Current live-pane replay/render path in [`internal/ui/agentview.go`](/Users/darrencheng/.argus/worktrees/argus/codex-prompt-line-manually/internal/ui/agentview.go) and [`internal/ui/vtrender.go`](/Users/darrencheng/.argus/worktrees/argus/codex-prompt-line-manually/internal/ui/vtrender.go)
- Bubble Tea package docs: https://pkg.go.dev/github.com/charmbracelet/bubbletea
- Tview package docs: https://pkg.go.dev/github.com/rivo/tview
- Tview repository: https://github.com/rivo/tview
- Charm `x/vt` package docs: https://pkg.go.dev/github.com/charmbracelet/x/vt
- Tcell package docs: https://pkg.go.dev/github.com/gdamore/tcell/v2
- Tcell repository: https://github.com/gdamore/tcell
- PTY package docs: https://pkg.go.dev/github.com/creack/pty
- Current `vt10x` package docs: https://pkg.go.dev/github.com/hinshun/vt10x

## Errors Encountered

| Error | Attempt | Resolution |
|-------|---------|------------|
| None during planning | N/A | N/A |

## Estimated Scope

**Phases:** 12
**Tasks:** ~60
**Files touched:** ~80+ (including ~60 deleted in Phase 11)

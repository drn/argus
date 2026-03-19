# Plan: High-Fidelity Terminal Rendering + Bubble Tea Removal

**Date:** 2026-03-18
**Source:** Inline request: "research well-maintained libraries that do this cleanly. then write an implementation plan"
**Status:** Complete
**Current Phase:** All phases complete (1-12)

## Goal

Replace the current replay-and-repaint terminal path in Argus with high-fidelity emulated rendering for the live agent pane — upstream colors, cursor behavior, prompt styling, and escape sequences rendered faithfully via direct cell-to-cell painting — then delete the entire Bubble Tea runtime and ship a single tcell-based UI.

## Why Emulation, Not True Passthrough

**True passthrough** means PTY bytes go directly to the host terminal with no intermediary. This is what `attach` mode does (full-screen handoff via `tea.Exec`). It works perfectly but requires giving the agent the entire terminal — incompatible with the three-panel layout (git status | terminal | file explorer).

**The only way to have panels + terminal content is emulated rendering.** This is what every terminal multiplexer does (tmux, screen, Zellij): feed PTY bytes to an emulator, read the resulting cells, paint them into a screen region. The quality depends on (a) the emulator's escape sequence coverage and (b) how faithfully cells reach the screen.

The old Bubble Tea path failed on (b): cells were converted to ANSI strings for `View() string`, Bubble Tea parsed them back into cells, then rendered — a lossy round-trip that destroyed cursor position, prevented damage tracking, and required manual prompt-row hacks.

The tcell path eliminates the string intermediary: emulator cells paint directly to `tcell.SetContent()`. x/vt (replacing vt10x) improves (a): actively maintained, better escape sequence coverage, native scrollback buffer, damage tracking via `Touched()`, hyperlink support.

**x/vt is compatible with Bubble Tea** — it's just an emulator, it works with anything. But x/vt + BT would still have BT's string bottleneck. The value of tcell is the direct cell painting, not the emulator swap.

| Path | Emulation | Cell → Screen | Damage Tracking | Cursor Fidelity |
|------|-----------|---------------|-----------------|-----------------|
| vt10x + BT (old) | Poor (unmaintained) | cells → ANSI strings → BT parses → renders | N/A (full repaint) | Lost in string conversion |
| x/vt + BT (hypothetical) | Excellent | cells → ANSI strings → BT parses → renders | Available but useless (BT repaints every View()) | Lost in string conversion |
| **x/vt + tcell (target)** | **Excellent** | **cells → tcell.SetContent() directly** | **Works — only repaint Touched() lines** | **Preserved natively** |

## Background

Argus currently runs agent sessions over a PTY and then re-renders the screen inside Bubble Tea:

- `internal/agent/session.go` starts the PTY and stores a ring buffer of raw output.
- `internal/ui/agentview.go` reads recent bytes, replays them through `vt10x`, and converts the resulting screen back into ANSI strings for the center panel.
- `internal/ui/vtrender.go` injects Argus-owned semantics such as the active input row tint, cursor repainting, and trimming of parked/empty rows.

That architecture works for preview and normalized display, but the live pane is a reconstructed view with lossy string conversion, not direct cell painting.

Library research from primary sources points to the following:

- Bubble Tea is mature and well-maintained, but its core model remains string-rendered (`View() string`), which makes high-fidelity terminal rendering impossible — every frame round-trips through ANSI string conversion.
- `github.com/gdamore/tcell/v2` is the most mature low-level Go screen/event library. It gives the right primitives for screen ownership, cursor control, mouse handling, and event polling.
- `github.com/rivo/tview` is a higher-level widget library built on `tcell` with layouts, lists, forms, tables, pages, and modals. It is the closest conservative replacement for the Bubble Tea shell primitives Argus uses today.
- `github.com/charmbracelet/x/vt` is a modern Charm terminal emulator with `Write`, `Draw`, cursor, damage tracking, scrollback buffer, and alt-screen support. It replaces `vt10x` as the emulation layer.
- `github.com/creack/pty` remains the right PTY layer and should stay.
- `github.com/hinshun/vt10x` is unmaintained (last commit 2022), drops many modern escape sequences, and should be replaced.

Recommendation:

- Migrate the UI runtime to `tcell` + `tview` for direct cell painting.
- Replace `vt10x` with `x/vt` for better emulation fidelity.
- Delete the entire Bubble Tea runtime once all views are ported.

## Requirements

### Must Have

- High-fidelity rendering of upstream PTY output: colors, cursor, wrapped rows, alt-screen behavior, and prompt styling.
- No manual prompt-row background injection or cursor synthesis in the live-pane path.
- Preserve existing task/session behavior in `internal/agent`, `internal/daemon`, and `internal/db`.
- Preserve the current three-panel workflow: git status, agent terminal, file explorer.
- Preserve task switching, detach flow, resize handling, and key forwarding to the PTY.
- Keep session persistence and post-exit log replay.

### Should Have

- Damage tracking — only repaint lines that changed, not the full panel every frame.
- Native scrollback via x/vt's `Scrollback()` buffer — no more replaying the entire ring buffer through a tall emulator.
- Keep current keybindings and focus behavior as close as possible during migration.
- Task list preview panel in tcell runtime (small terminal snapshot per task).

### Won't Do (this iteration)

- Rebuild the daemon, agent session model, or worktree model.
- Replace `creack/pty`.
- True passthrough (raw PTY bytes to host terminal for a screen subregion) — fundamentally incompatible with panels.
- Windows support — Unix-first, same as existing PTY behavior.

## Technical Approach

The clean path is a runtime split and staged migration:

1. Extract a UI-agnostic application layer from the current Bubble Tea-heavy presentation code.
2. Introduce a second UI runtime built on `tcell`, using `tview` for high-level application chrome and a custom PTY terminal primitive for the live agent pane.
3. Replace `vt10x` with `x/vt` for modern emulation + damage tracking + native scrollback.
4. Port all remaining views (Reviews, Settings, forms) to tcell.
5. Delete the entire Bubble Tea runtime and all charmbracelet/bubbletea dependencies.

## Decisions

| Decision | Rationale |
|----------|-----------|
| Use `tcell` as the target screen/runtime layer and `tview` as the shell/widget layer | This keeps the stack conservative: mature screen ownership plus common layout/widgets without betting on a smaller runtime ecosystem |
| Use `x/vt` as the emulator, replacing `vt10x` | Actively maintained, damage tracking, scrollback buffer, hyperlinks, better escape sequence coverage |
| Emulated rendering, not true passthrough | True passthrough requires full-screen handoff — incompatible with three-panel layout. x/vt + tcell gives high-fidelity emulation with direct cell painting |
| Keep `creack/pty` for PTY allocation and resize | The PTY layer is already correct and independent of the renderer |
| Do not try to embed a native terminal pane inside Bubble Tea's string rendering model | The string intermediary is the bottleneck — x/vt + BT would still be lossy |
| Delete BT entirely, not keep it as fallback | Two UI codebases in one binary is unsustainable. Once all views are ported, BT adds only maintenance burden |

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
**Status:** complete

This is a significant architectural change. Currently scrollback replays the entire 256KB ring buffer through a tall vt10x instance every frame. x/vt's native `Scrollback()` buffer eliminates this entirely — no more `estimateVTRows()`, no more creating a throwaway emulator sized to total-output-lines.

**Emulator swap:**
- [x] `go get github.com/charmbracelet/x/vt github.com/charmbracelet/ultraviolet`
- [x] Replace `vt10x.New(vt10x.WithSize(w, h))` → `vt.NewSafeEmulator(w, h)` in `internal/tui2/terminalpane.go`
- [x] Replace `vt.Cell(x, y)` → `emulator.CellAt(x, y)` (returns `*uv.Cell` with `.Content` string, `.Style` with fg/bg as `image/color.Color`, attrs as bitflags)
- [x] Replace `cellStyle(vt10x.Glyph)` → new `uvCellToTcellStyle(*uv.Cell)` using `tcell.FromImageColor(cell.Style.Fg)` for color conversion
- [x] Replace cursor access: `vt.Cursor()` → `emulator.CursorPosition()`
- [x] Remove `vt.Lock()/Unlock()` — SafeEmulator is thread-safe
- [x] Replace `rowHasContent` to check `cell.Content != "" && cell.Content != " "` (x/vt cells use string content, not rune)

**Scrollback rewrite:**
- [x] Replace `renderReplay` (full-buffer replay through tall vt10x) with `emulator.Scrollback()` — native scrollback buffer with `.Len()` and `.CellAt(x, y)`
- [x] Delete `estimateVTRows()` from terminalpane.go — no longer needed
- [x] Scrollback rendering reads from `emulator.Scrollback()` for lines above the viewport and `emulator.CellAt()` for visible lines

**Damage tracking:**
- [x] Use `emulator.Touched()` to get set of changed lines since last draw
- [x] Only repaint changed lines in `paintVT()` instead of full panel repaint

**Log replay for finished sessions:**
- [x] Rewrite log replay in terminalpane.go to use x/vt instead of importing `ui.ReplayVT10X` + `ui.EstimateVTRows` — feed session log bytes to a local x/vt emulator, use its scrollback for navigation
- [x] Remove tui2's dependency on `internal/ui/vtrender.go` exports (`ReplayVT10X`, `EstimateVTRows`)

**Tests:**
- [x] Update `internal/tui2/terminalpane_test.go` for x/vt API
- [x] Add tests for scrollback via `Scrollback()` buffer
- [x] Add tests for damage tracking (only changed lines repainted)

### Phase 7: Extract shared utilities from internal/ui/
**Status:** complete

Two new packages: `internal/gitutil/` (git operations, diff parsing, changed files) and `internal/skills/` (skill loading — not git-related).

**Move to `internal/gitutil/` (pure Go, zero charmbracelet imports):**
- [x] `internal/ui/gitcmd.go` → `internal/gitutil/gitcmd.go` (FetchGitStatus, FetchFileDiff, FetchDirFiles, and their message types: `GitStatusRefreshMsg`, `DirFilesMsg`, `FileDiffMsg`)
- [x] `internal/ui/scrollstate.go` → `internal/gitutil/scrollstate.go` (ScrollState)
- [x] `internal/ui/diffparse.go` → `internal/gitutil/diffparse.go` (ParseUnifiedDiff, BuildSideBySide, DiffLine, DiffHunk types). Note: uses `charmbracelet/x/ansi` for `ansi.Hardwrap` and `ansi.StringWidth` — this dep stays in go.mod.
- [x] `internal/ui/worktree.go` → `internal/gitutil/worktree.go` (worktree discovery, cleanup)
- [x] Extract `ChangedFile`, `ParseGitStatus`, `ParseGitDiffNameStatus`, `MergeChangedFiles` from `internal/ui/fileexplorer.go` → `internal/gitutil/changedfiles.go` (pure Go types and parsers only; the `FileExplorer` struct with lipgloss rendering stays in `internal/ui/` until Phase 11 deletion)

**Move to `internal/skills/`:**
- [x] `internal/ui/skills.go` → `internal/skills/skills.go` (SkillItem, ScanSkills, LoadSkillsCmd — pure Go, no charmbracelet imports)

**Update imports:**
- [x] `internal/tui2/app.go`: `ui.FetchGitStatus` → `gitutil.FetchGitStatus`, `ui.ParseGitStatus` → `gitutil.ParseGitStatus`, etc.
- [x] `internal/tui2/fileexplorer.go`: `ui.ChangedFile` → `gitutil.ChangedFile`
- [x] `internal/tui2/newtaskform.go`: skill loading → `skills.ScanSkills`
- [x] Keep `internal/ui/` files importing from `gitutil`/`skills` so BT runtime still compiles (deleted in Phase 11)

**vtrender.go stays in `internal/ui/`** — after Phase 6 removes tui2's dependency on it, vtrender.go is only used by the BT runtime and will be deleted in Phase 11. No extraction needed.

### Phase 8: Port Reviews tab to tcell
**Status:** complete

New file: `internal/tui2/reviews.go`

The BT reviews tab (`internal/ui/reviews.go`) has significant complexity that must be ported:

**PR fetching (from `internal/github/github.go`):**
- [x] Cross-repo list via `gh search prs` → enrichment with `reviewDecision` via `gh pr list` grouped by repo (O(repos) not O(PRs))
- [x] `SetPRs` must sort review requests before "my PRs" — `prCursor` navigates a flat slice, order must match visual sections
- [x] 10-min cooldown (`prListCooldown`) gates refetch on tab entry; all tab-switch paths check `canFetchPRList()`
- [x] Show cached data during background refresh with dimmed "refreshing…" indicator

**Three-panel layout using `tview.Flex`:**
- [x] Left: PR list with "Review Requests" section first, "My Open PRs" second. Cursor navigation, status badges (draft, review decision, CI status).
- [x] Center: Diff viewer using `gitutil.ParseUnifiedDiff` + `gitutil.BuildSideBySide` for split mode. Syntax highlighting via Chroma with `injectBg` for diff line backgrounds (Chroma resets after every token — must re-apply background after each `\033[0m`). Full diff cached per PR, re-sliced per file via `ExtractFileDiff`.
- [x] Right: Comments list with threading + compose box. Comments fetched via GitHub REST API. 2-min TTL for auto-refresh. Diff staleness checked against `PR.UpdatedAt`.

**Key bindings:**
- [x] up/down/j/k navigate PR list or diff
- [x] Enter select PR → fetch files + comments in parallel
- [x] Tab cycle focus: list → diff → comment compose
- [x] s toggle split/unified diff view
- [x] c capture line number, open comment compose
- [x] a approve, r request changes via `github.SubmitReview()`
- [x] R manual refresh (subject to cooldown)
- [x] esc back (exit compose, exit diff, exit reviews)

### Phase 9: Port Settings tab to tcell
**Status:** complete

New file: `internal/tui2/settings.go`

The BT settings tab has more complexity than a simple list/detail view:

**Two-panel layout (section list | detail):**
- [x] Left: Scrollable list of sections. Cursor skips section headers (same pattern as task list).
- [x] Right: Detail panel for selected item.

**Sections and their detail views:**
- [x] STATUS — daemon connection state (`daemonConnected` flag drives "in-process mode" warning), sandbox availability (`IsSandboxAvailable()` cached via `sync.Once`)
- [x] SANDBOX — global sandbox enabled/disabled, deny-read paths (CSV), extra-write paths (CSV). Inline editing of path lists.
- [x] PROJECTS — project name, path, branch, backend, per-project sandbox overrides (`sandbox_enabled`: inherit/true/false, `sandbox_deny_read`, `sandbox_extra_write`). Detail shows all config fields.
- [x] BACKENDS — name, command, prompt flag. Shows `(default: <name>)` in section header. 'd' sets default.
- [x] KNOWLEDGE BASE — vault paths (`kb.argus_vault_path`, `kb.metis_vault_path`). Uses `NewKBVaultForm` pattern (accepts DB config key explicitly, not derived from label).
- [x] UX LOGS — display recent entries from `~/.argus/ux.log`

**Key bindings:**
- [x] up/down navigate (cursor skips section headers)
- [x] 'n' new project/backend
- [x] 'e' edit selected item (opens modal form)
- [x] 'd' delete item or set default backend
- [x] Enter select/expand

**Forms as modal overlays via `tview.Pages.AddPage`** — same pattern as `NewTaskForm`.

### Phase 10: Port remaining forms to tcell
**Status:** complete

- [x] `internal/tui2/projectform.go` — add/edit project (name, path, branch, backend selector, per-project sandbox settings: enabled inherit/true/false, deny-read paths, extra-write paths). Auto-detect remote default branch on path entry (prefer `upstream` over `origin`).
- [x] `internal/tui2/backendform.go` — add/edit backend (name, command, prompt flag). Edit mode: name field read-only. Mirrors `internal/ui/backendform.go` pattern with 3 text input fields.
- [x] `internal/tui2/renametask.go` — rename task (single text input). Display-only rename: updates `t.Name` in DB, worktree dir/branch/session unchanged. Works while agent is running.
- [x] `internal/tui2/kbvaultform.go` — KB vault path editor. Takes DB config key explicitly (`"kb.metis_vault_path"`, `"kb.argus_vault_path"`), not derived from label string.
- [x] All follow modal pattern: `tview.Box` with `InputHandler`, added/removed as page on submit/cancel.

### Phase 10: Port remaining forms to tcell
**Status:** complete

The BT task list shows a small terminal snapshot in the right panel when hovering over a task (`internal/ui/preview.go`). The tcell task list currently has no preview.

- [x] Add preview rendering to `TaskListView` right panel — small x/vt emulator sized to panel dimensions, fed from `session.RecentOutput()` or session log file
- [x] Preview updates on cursor movement (debounced, not every keystroke)
- [x] Finished tasks: replay from `~/.argus/sessions/<taskID>.log`
- [x] Active tasks: snapshot from `session.RecentOutputTail()`

### Phase 11: Delete Bubble Tea runtime + clean deps
**Status:** complete

- [x] Delete `internal/ui/` entirely (~60 files including vtrender.go, agentview.go, root.go, all forms, all views, all tests)
- [x] Remove BT wiring from `cmd/argus/main.go`: delete `runTUI()` function, delete `tea` and `internal/ui` imports, make `runTcell()` the only path (remove runtime switch and `ARGUS_UI_RUNTIME` env var)
- [x] Delete `internal/app/agentview/runtime.go` and `runtime_test.go` (no longer needed — single runtime)
- [x] Remove from go.mod: `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/bubbles`, `github.com/charmbracelet/lipgloss`, `github.com/hinshun/vt10x`
- [x] Keep in go.mod: `github.com/charmbracelet/x/vt` (emulator), `github.com/charmbracelet/x/ansi` (used by `internal/gitutil/diffparse.go` for `ansi.Hardwrap`/`ansi.StringWidth`), `github.com/charmbracelet/ultraviolet` (x/vt cell types)
- [x] `go mod tidy` to drop indirect deps (colorprofile, cellbuf, x/term, etc.)
- [x] `go build ./... && go vet ./... && go test ./...`
- [x] Verify: `grep -r "charmbracelet/bubbletea\|charmbracelet/bubbles\|charmbracelet/lipgloss\|hinshun/vt10x" go.mod` → zero matches
- [x] Verify: `grep -r "internal/ui" internal/tui2/ cmd/` → zero matches (only `internal/gitutil` and `internal/skills`)

### Phase 12: Wire daemon restart + in-process session exit
**Status:** complete

The BT runtime has significant daemon lifecycle code that must be ported:

**Daemon health check (mirror BT's `daemonFailures` counter):**
- [x] Tick handler pings daemon via `client.Ping()` (type-assert `runner` to `*dclient.Client`)
- [x] Three consecutive failures set `daemonRestarting = true` and trigger `restartDaemonCmd()`
- [x] Reset `daemonFailures = 0` on successful ping AND in restart-complete handler
- [x] Skip health check when `!daemonConnected` (in-process mode) or `daemonRestarting` (already restarting)

**Daemon restart flow (mirror BT's `DaemonRestartedMsg`):**
- [x] `App.RestartedClient()` returns the new `*dclient.Client` after daemon restart (currently returns nil)
- [x] Restart handler: reset all InProgress tasks to Pending, **preserve SessionID** (Claude Code's `--session-id` persists conversation state — clearing it loses history)
- [x] Re-query sessions from new daemon, update `runner` reference

**In-process session exit:**
- [x] Wire `onFinish` callback in `runTcell()` for in-process mode so session exits are detected immediately via `QueueUpdateDraw`, not on next 1s tick
- [x] `onFinish` must fire before session removal from runner (same ordering invariant as BT path)

**Auto-start daemon:**
- [x] `autoStartDaemon()` forks current binary with `Setsid`, polls socket until ready (50ms intervals, 3s timeout)
- [x] Falls back to in-process mode if auto-start fails, with warning in Settings tab

## Testing Strategy

- Keep `go test ./...` green throughout the extraction and migration.
- Add focused adapter tests for PTY resize, key forwarding, scrollback, and session replay.
- Create manual smoke scripts for terminal-specific behaviors that unit tests miss: alt-screen apps, mouse reporting, cursor visibility, long wrapped prompts, and Unicode/wide glyphs.
- Test both runtimes behind the feature flag until Phase 11 cutover.
- Verify that task-list previews and completed-session output remain stable while live rendering changes underneath.
- After Phase 11: verify no `internal/ui` imports remain in tui2 or cmd.

## Risks & Open Questions

| Risk | Mitigation |
|------|------------|
| x/vt's API surface differs from vt10x more than expected | Phase 6 is isolated to terminalpane.go — if x/vt's cell/cursor API doesn't map cleanly, the blast radius is one file |
| Reviews tab port is the largest single phase (complex data flow, Chroma highlighting, comment threading) | Port data fetching first (goroutines + QueueUpdateDraw), then layout, then interactions |
| Settings forms have subtle validation and DB interaction | Mirror existing BT form logic closely, use same DB methods |
| Damage tracking may not integrate cleanly with tview's own drawing | `tview.Box.Draw()` is called every frame by tview — may need to track dirty state ourselves and skip `SetContent` for untouched cells |
| Task list preview requires a second x/vt emulator per visible task | Debounce heavily, only emulate for the currently-selected task, reuse emulator instance |

- Should the task list preview (Phase 10.5) use x/vt or a simpler approach (just show last N lines of raw output)?
- Do we need Chroma syntax highlighting in the tcell diff viewer, or is colored +/- lines sufficient?

## Dependencies

- Current PTY/session layer in `internal/agent/session.go`
- Current Bubble Tea app shell in `internal/ui/root.go`
- Current live-pane replay/render path in `internal/ui/agentview.go` and `internal/ui/vtrender.go`
- Charm `x/vt` package docs: https://pkg.go.dev/github.com/charmbracelet/x/vt
- Charm ultraviolet (cell types): https://pkg.go.dev/github.com/charmbracelet/ultraviolet
- Tcell package docs: https://pkg.go.dev/github.com/gdamore/tcell/v2
- Tview repository: https://github.com/rivo/tview
- PTY package docs: https://pkg.go.dev/github.com/creack/pty

## Errors Encountered

| Error | Attempt | Resolution |
|-------|---------|------------|
| None during planning | N/A | N/A |

## Estimated Scope

**Phases:** 13 (including 10.5)
**Tasks:** ~75
**Files touched:** ~85+ (including ~60 deleted in Phase 11)

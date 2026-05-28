# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Argus is a terminal-native LLM code orchestrator built with Go + tcell/tview. It manages multiple Claude Code / Codex sessions with task tracking, git worktree isolation, and keyboard-driven workflow.

## Build & Run

```bash
make build                  # go build ./...
make vet                    # go vet ./...
make test                   # go test -race -count=1 ./...
make test-pkg PKG=./internal/db/  # single package, verbose
make test-cover             # coverage profile + summary
make test-cover-gate        # race suite + coverage floor (-min 88; matches CI gate)
make test-watch             # gotestsum --watch (install: go install gotest.tools/gotestsum@latest)
make fmt                    # goimports -w . (format the tree)
make fmt-check              # fail if any file is not goimports-clean (matches CI)
make vuln                   # govulncheck ./... (install: go install golang.org/x/vuln/cmd/govulncheck@latest)
make lint-pr                # golangci-lint --new-from-rev=origin/master (matches CI; run before pushing)
make pre-pr                 # full CI mirror: build+vet+fmt-check+lint-pr+vuln+test-cover-gate
go build -o argus ./cmd/argus/    # build binary
```

## Before Opening a PR

**Run `make pre-pr` and get a clean pass before opening OR updating any PR.** It mirrors `.github/workflows/ci.yml` step-for-step (build → vet → fmt-check → lint-pr → vuln → test-cover-gate), so a green `make pre-pr` means CI will be green. This is non-negotiable: do not `git push` a PR branch until it passes.

- **`make fmt-check` failing?** Run `make fmt` first — goimports rewrites the tree (the most common CI miss is an unformatted file).
- **`make test-cover-gate` failing?** Filtered coverage dropped below the 88% floor. Add tests for the code you touched (or for under-covered platform-agnostic code) until it clears — the floor ratchets up and never down. Do NOT lower `-min`. Note that filtered coverage can drift ~0.2% between darwin (local) and linux (CI) because of platform-runtime branches in files like `internal/agent/sandbox.go`; target platform-agnostic code for the most reliable margin.
- **`make lint-pr` failing?** `lint-pr` uses `--new-from-rev=origin/master`, so it only flags issues your diff introduced (including `staticcheck` deprecations like `SA1019` on new lines). Fix them; do not add blanket `//nolint`.

Why this matters: the gate steps run in sequence and the first failure short-circuits the rest. A formatting miss alone will hide test/lint/coverage failures from CI until it's fixed, so running the **full** `make pre-pr` locally is the only way to surface every problem in one pass.

## Test-Driven Development

Follow Red-Green-Refactor as the default workflow:

1. **Red** — Write a failing test first using `internal/testutil` assertions
2. **Green** — Write the minimum code to make it pass
3. **Refactor** — Clean up while keeping tests green

Use `make test-watch` for continuous feedback. Use `make test-pkg` for focused iteration on a single package.

**Assertions** — use `internal/testutil` (not raw `if got != want`):

```go
import "github.com/drn/argus/internal/testutil"

testutil.Equal(t, got, want)           // comparable types
testutil.DeepEqual(t, got, want)       // structs/slices via go-cmp
testutil.NoError(t, err)               // err == nil
testutil.ErrorIs(t, err, target)       // errors.Is
testutil.Nil(t, val)                   // handles nil-interface trap
testutil.Contains(t, s, substr)        // string contains
```

All table-driven tests must use `t.Run` subtests. Guard slow tests with `testing.Short()`.

## Architecture

**tcell/tview UI** with direct cell painting for the agent terminal pane. The `App` struct owns the `tview.Application`, DB, runner, and all sub-views.

- `cmd/argus/main.go` — Entry point. Parses subcommands (`daemon`, `daemon stop`), opens SQLite database. In TUI mode: tries daemon client first, falls back to in-process runner. Starts the tcell/tview app.
- `internal/tui/app.go` — **Top-level tview application**. Owns all sub-views and routes key events via `tapp.SetInputCapture()`. View switching via `tview.Pages`. Layout uses `tview.Flex` (vertical: header + pages + statusbar).
- `internal/tui/tasklist.go` — Task list with collapsible project folders, cursor, scrolling, filtering. Tasks are grouped by project name into a flattened row list (project headers + task rows). Only one project is expanded at a time — auto-expands when the cursor enters a project, auto-collapses others. Cursor navigation skips project header rows entirely. Includes an **Archive section** at the bottom — the archive auto-expands when the cursor enters it and auto-collapses when the cursor leaves. Archived projects are only displayed within the archive section, never in the main section.
- `internal/tui/terminalpane.go` — Custom `tview.Box` widget for the agent terminal. Feeds PTY bytes to an x/vt emulator and paints cells directly to `tcell.Screen` via `paintVT()`. Supports live mode (incremental byte feeding), scrollback (x/vt native `Scrollback()` buffer), and log replay for finished sessions. Damage tracking via `Touched()` for efficient incremental repainting.
- `internal/tui/gitstatus.go` — `GitPanel` for git status/diff/branch display in both agent view and task list.
- `internal/tui/fileexplorer.go` — `FilePanel` with auto-expand, cursor navigation, and status icons.
- `internal/tui/settings.go` — Settings tab with sections for status, sandbox, projects, backends, KB, and UX logs.
- `internal/tui/newtaskform.go` — New task form as modal overlay via `tview.Pages.AddPage`.
- `internal/tui/taskpage.go` — Task list page wrapper with three-panel layout (tasks | git+preview | details) and empty-state banner.
- `internal/app/agentview/` — Runtime-agnostic agent view state: `State`, `Panel`, `DiffState`, `TerminalAdapter` interface, `SessionLookup`.
- `internal/model/` — Core domain types. `Task` struct and `Status` enum with `pending → in_progress → in_review → complete` workflow. Status implements `encoding.TextMarshaler` for JSON serialization.
- `internal/db/` — SQLite-backed persistence at `~/.argus/data.sql`. Stores tasks, projects, backends, and config in a single database. Thread-safe with mutex. Seeds defaults on first run.
- `internal/config/config.go` — Config struct types and defaults. Struct types (`Config`, `Backend`, `Project`, `Keybindings`, `UIConfig`) are used throughout the codebase as value types. The `db.DB.Config()` method assembles a `Config` from the database.
- `internal/agent/` — Agent process management with PTY:
  - `agent.go` — Backend resolution and command building (`BuildCmd`). Supports `--session-id` for conversation pinning.
  - `worktree.go` — Git worktree creation under `~/.argus/worktrees/<project>/<task>` with `argus/<task>` branch naming.
  - `iface.go` — `SessionProvider` (manages sessions) and `SessionHandle` (single session) interfaces. UI code depends only on these interfaces, enabling both in-process and daemon-backed implementations.
  - `session.go` — PTY-backed process session via `creack/pty`. Single `readLoop` goroutine tees output to ring buffer + all attached writers. Multi-writer support via `AddWriter`/`RemoveWriter` for fan-out to multiple consumers. Supports attach/detach without stopping the process.
  - `runner.go` — Multi-session manager keyed by task ID. Implements `SessionProvider`. Start/Stop/Get/Attach/Detach. Auto-cleans up on process exit, fires `onFinish` callback.
  - `attach.go` — `AttachCmd` for full-screen terminal attach. Sets raw terminal mode, resizes PTY, uses detachReader to intercept `ctrl+q` for detach.
  - `ringbuffer.go` — Exported `RingBuffer` — fixed-size circular buffer for output replay on reattach. Used by both in-process sessions and daemon client's local buffer.
  - `errors.go` — Sentinel errors.
- `internal/daemon/` — Daemon architecture for persistent agent sessions:
  - `daemon.go` — `Daemon` struct: owns Runner, accepts Unix socket connections, dispatches RPC vs stream (first byte 'R'/'S'). PID file at `~/.argus/daemon.pid`. Signal handling (SIGTERM/SIGINT → graceful shutdown).
  - `types.go` — Shared RPC request/response types (`StartReq`, `SessionInfo`, `StreamHeader`, etc.).
  - `rpc.go` — `RPCService` implementing JSON-RPC methods: Ping, StartSession, StopSession, StopAll, SessionStatus, ListSessions, WriteInput, Resize, Shutdown.
  - `stream.go` — Output streaming handler. Client sends `StreamHeader` JSON, daemon calls `AddWriter(conn)` on the session. Raw bytes flow until session exit or client disconnect.
- `internal/uxlog/` — UX debug logging for the TUI layer. Writes to `~/.argus/ux.log`, separate from daemon logs. Logs task start/stop/finish, status transitions, stream connect/disconnect, RPC timeouts. Viewable in Settings → UX Logs.
- `internal/daemon/client/` — TUI-side daemon client:
  - `client.go` — `Client` implementing `SessionProvider` via JSON-RPC to daemon. Manages `RemoteSession` lifecycle.
  - `handle.go` — `RemoteSession` implementing `SessionHandle`. Local `RingBuffer` populated by stream reader. RPC calls for WriteInput, Resize, PTYSize, etc.
  - `stream.go` — Goroutine reads raw bytes from daemon stream connection into local ring buffer.
- `internal/apiclient/` — typed HTTP client for the REST API. Used by the TUI's `--remote` mode AND by external scripts/CLI tools. Provides `Client` (bearer auth, JSON, keep-alive), `Provider` (implements `agent.SessionProvider` over HTTP), `Session` (implements `agent.SessionHandle` — SSE-fed local ring buffer + REST writes). Mirror of daemon-client but transport-agnostic.
- `internal/apistore/` — implements `internal/tui/store.Store` over the REST API. Used by `--remote` mode. Caches the full `config.Config` snapshot (refreshed every 30 s by `cmd/argus/remote.go`) so `Config()` doesn't burn one HTTP request per UI tick.
- `internal/tui/store/` — narrow interface extracted from the methods the TUI calls on `*db.DB`. Both `*db.DB` (local) and `*apistore.Store` (remote) satisfy it implicitly. `store/assert_test.go` is the compile-time canary that catches signature drift.
- `cmd/argus/remote.go` — entry point for `argus --remote URL --token TOKEN`. Mirrors `runTUI()` except: no local SQLite, no daemon socket, no in-process runner fallback. Validates connectivity via `/api/status` before tcell takes over, then constructs an `apistore.Store` + `apiclient.Provider` and hands them to `tui.New`.
- `internal/gitutil/` — Git operations, diff parsing, changed files. Pure Go with no UI dependencies. Used by tui for git status, file diffs, and worktree management.
- `internal/spinner/` — Reusable spinner animation definitions. Each `Spinner` has a `Style`, `Label`, `Frames` (rune slice), and `TickInterval`. Built-in styles: Progress (nerdfont ee06–ee0b, 100ms), Dots (braille dots, 100ms), Braille (braille pattern, 100ms), Classic (ASCII, 150ms). Configurable via `ui.spinner` setting. `model.SetActiveSpinner()` switches at runtime; `model.SpinnerFrame(tick)` delegates to the active spinner.
- `internal/skills/` — Skill loading for autocomplete. Scans `~/.claude/skills/` and project-specific skill directories.
- `internal/api/` — HTTP REST API + mobile PWA for remote control on port 7743. Binds `127.0.0.1` (required) plus the Tailscale IP (best-effort) — never `0.0.0.0`, so untrusted LANs (hotel/cafe WiFi) cannot reach the API even if a strong token is set. Tailscale IP is discovered via `tailscale ip -4` (authoritative — talks to the LocalAPI socket, disambiguates from other CGNAT VPNs like Cloudflare WARP) with a 100.64.0.0/10 interface scan as fallback. Localhost bind failure is fatal; Tailscale bind failure is logged and ignored so a transient flap during startup cannot take the API offline. Port-probing pattern from MCP server. Surface area:
  - **Tasks**: list/create/get/stop/resume/delete/archive/unarchive/rename/fork/status, sessions stop-all, maintenance prune-completed
  - **Terminal**: `/output`, `/input`, SSE `/stream`, `/size`, `/resize` — feeds xterm.js in the SPA
  - **Config CRUD**: projects + backends (master-only)
  - **Git per worktree**: `/git/status`, `/git/diff`, `/files`
  - **Web Push (VAPID)**: `/push/vapid-public-key`, `/push/subscribe`, `/push/subscriptions`, `/push/test` (master), idle watcher fires throttled push when sessions transition idle
  - **Per-device tokens**: master-only mint/revoke; SHA-256 hashed in `api_tokens` table; auth middleware accepts master OR device, tags request via `X-Argus-Auth: master|device` header so destructive endpoints can `requireMaster()`
  - **Auth**: `Authorization: Bearer <token>` or `?token=<token>` query param (required for `EventSource` which can't set headers)
  - **PWA**: vendored xterm.js + addon-fit, `manifest.webmanifest`, service worker (cache-first shell, network-only `/api`), apple-touch-icon, icons 192/512
- `internal/push/` — `Manager` wraps `webpush-go` with VAPID key persistence (DB `config` table), per-task throttling (`lastSent` map, pruned via `ForgetTask` from idleWatcher), expired-subscription auto-pruning on HTTP 410 from push service.
- `cmd/argus-test-server/` — isolated API harness for Playwright. Sets `HOME=$tempdir`, seeds a `bash`-backed task that PTY-echoes input. Exposes `/test/reset` on `port+10` for between-spec state cleanup. Used by `web-tests/` Playwright project (43 specs).
- `internal/daemon/headless.go` — Headless task creation (worktree + DB + session start) without TUI. Shared by HTTP API and MCP via `TaskCreator` function injection.

**Key pattern:** Sub-views are custom `tview.Box` widgets with `Draw(screen tcell.Screen)` methods. Async updates via `tapp.QueueUpdateDraw()` from the tick goroutine. Key routing via `tapp.SetInputCapture()`. **Every custom widget that accepts text input must implement `PasteHandler()`** — tview's bracket paste bypasses `InputCapture` entirely, so widgets without a `PasteHandler()` silently drop pasted text. For PTY-backed widgets, wrap the pasted text in bracket paste sequences (`\x1b[200~`/`\x1b[201~`).

**UX rendering — don't reintroduce `screen.Sync()` for content updates.** This codebase went through a 12-commit cycle (Mar 22 – May 12, 2026) chasing visible "tearing" in tmux. Every fix made it worse because the diagnosis was wrong. The actual root cause was a long-deleted `lazyScreen.skipClear` typing-latency optimization (`94797775`), removed in `e516ad33`. The Sync-based scaffolding (forceSync, OnBranchChange→Sync, OnContentChange→Sync, multiplexerMode, hash-gating, etc.) was all built to patch downstream ghosts from `skipClear` — but it shipped _the day after_ `skipClear` was removed and was never unwound. What looked like "tmux drift" in the visible flashing was actually `tcell.Sync()` itself: it emits `CSI 2J` (clear-screen) which tmux faithfully propagates to the outer terminal.

**Two correct primitives, two correct uses:**

- **`screen.Show()`** (tview already calls this after every frame) emits the per-cell diff against last-emitted state. `tview.draw()` calls `screen.Clear()` first which blanks the cell buffer, then widgets paint, then `Show()` emits only changed cells. Inside tmux, tcell v2.13.0+ auto-wraps every `draw()` in DECSET 2026 (Synchronized Output / BSU+ESU) when the terminfo is `XTermLike` — tmux is `XTermLike` per its tcell terminfo entry — so the entire frame emission is atomic. **This handles 99% of UI updates with zero flash, including typing, cursor nav, PTY streaming, modal open/close, page swaps, resize.**
- **`screen.Sync()`** is for repairing screen damage that tview's Clear+Show diff cycle can't handle — exactly three callsites: (1) `afterDraw` on terminal resize (tcell's diff compares against the prior emit, not the terminal's actual state; resize is the one event where those diverge), (2) Ctrl+L (user-initiated refresh), and (3) tmux focus regain (window switch may have repainted our pane from a stale backing). All three are rare; one `CSI 2J` flash per occurrence is the correct tradeoff. Anywhere else, **don't call Sync.**

**Hard rules for future agents:**

1. **Do NOT add `screen.Sync()` to recover from any "tearing" symptom without first ruling out user-side tmux config.** The fix is almost always `set -as terminal-features ',xterm*:sync'` in `~/.tmux.conf` (passthrough of inner DECSET 2026 to the outer terminal). See README's "Running inside tmux" section.
2. **Do NOT re-introduce `OnBranchChange`/`OnContentChange` → `forceRedraw` → Sync paths.** The `forceRedraw` helper still exists but is log-only — it does NOT trigger Sync. It's there to preserve a debug trail of which transitions fired, useful when chasing future drift reports. If a specific widget produces a visual artifact that tcell.Show() can't fix, fix the widget's Draw (ensure full bounding-rect coverage via `widget.FillArea` / `widget.DrawBorderedPanel`) — never add a Sync trigger.
3. **The diagnosis "tcell's SGR/cursor cache desyncs from tmux" is FACTUALLY WRONG for tcell v2.13.x.** tcell resets `t.cx = -1`, `t.cy = -1`, `t.curstyle = styleInvalid` at the top of every `draw()` call (line ~750 of `tscreen.go`). There is no cross-frame cache to desync. Any documentation, comment, or commit message claiming otherwise is repeating a debunked theory.
4. **Read `context/knowledge/gotchas/ui-threading.md` BEFORE adding any rendering-related code.** The post-mortem covers what each previous fix attempted, why it failed, and what to do instead.
5. **Not every "tearing" is tcell-side.** If the symptom is a visible content shift ~200ms after a new task launches (not a CSI 2J flash, not a stale cell, but a content reflow), suspect a PTY-size mismatch that triggers a SIGWINCH-induced agent repaint. The agent process (Claude/Codex) clears + repaints its screen on size change; if `computePTYSize` and `SetSession`'s seed don't agree with what `DrawBorderedPanel` will compute as the inner rect, the defensive forceResync sends a different-size Resize RPC, the kernel signals SIGWINCH, and the agent repaints. Fix the size-alignment math (see `agentViewRowOverhead` / `agentViewColOverhead` constants in `internal/tui/app.go` and the inner-rect seed in `internal/tui/terminal/terminalpane.go:SetSession`) — do NOT reach for Sync. See `context/knowledge/gotchas/pty-terminal.md` for the invariants.
6. **NO code path in the TUI process may write to `os.Stderr` or `os.Stdout` after `tcell.Screen.Init` runs.** The TUI's stderr/stdout IS the user's terminal. tcell does not route through them, so any direct write lands at the cursor's current position and corrupts the displayed cell state — surviving on screen until the next `screen.Sync()` because tcell's diff tracker only re-emits cells whose buffer value changed. Symptoms: torn cells, scattered log fragments, stacked status bars, mis-positioned content. **This was misdiagnosed as tcell/tmux drift for entire investigation cycles.**

   **Guards (defense in depth):**

   - **(a) `slog.SetDefault` redirect** in `runTUI` at startup: `slog.SetDefault(slog.New(slog.NewTextHandler(uxlog.Writer(), nil)))`. Catches every `slog.{Info,Error,Warn,Debug,...}` call program-wide.
   - **(b) `log.SetOutput` redirect** in `runTUI` at startup: `log.SetOutput(uxlog.Writer())`. Catches every stdlib `log.{Print*,Fatal*,Panic*}` call.
   - **(c) OS-level `syscall.Dup2` of fd 2 to the uxlog file** installed in `runTUI` immediately before `app.Run()` (not at startup — setup-time errors above need fd 2 = terminal so the user sees them). Catches what (a)+(b) cannot: goroutine panic stack dumps (Go's runtime emits panic output directly to fd 2 via the `runtime` package, bypassing every Writer-based redirect and the main-goroutine `defer recover`), subprocess fd 2 inheritance, and third-party direct fd 2 writes. The original fd 2 is dup'd, and an explicit `restoreFd2()` runs immediately after `app.Run()` returns (sync.Once-guarded; the deferred call is the no-op on the normal path and the only call on the panic path). This explicit-before-os.Exit pattern is load-bearing — without it, the post-Run `fmt.Fprintf(os.Stderr, "error: %v", err) + os.Exit(1)` branch would silently swallow the error into uxlog because `os.Exit` skips defers.
   - **(d) Top-level `defer recover()`** in `runTUI`'s main goroutine: logs the panic stack to uxlog before re-panicking. By the time the re-panic reaches the runtime, (c)'s deferred restore has already fired (defers run LIFO during panic unwind), so the runtime's final panic output reaches the user's terminal after tview has already torn down the alt-screen.
   - **(e) Regression tests** in `internal/uxlog/uxlog_test.go`: `TestSlogWithUxlogWriter_DoesNotReachStderr` pins (a)+(b) by capturing `os.Stderr` while firing slog/log calls; `TestFd2RedirectViaDup2_CatchesRawSyscallWrites` pins (c) by writing a sentinel via the raw `syscall.Write(2, ...)` path (the same path the Go runtime uses for panic dumps) and asserting it lands in uxlog. Both fail the build if anyone breaks the wiring.

   **What NOT to do:**

   - Never call `fmt.Fprintf(os.Stderr, ...)` or `fmt.Print*(...)` from anywhere reachable from `runTUI` after `app.Run()` starts — write via `uxlog.Log` or `slog.*` instead.
   - Never redirect fd 1 (`os.Stdout`) — `computePTYSize` calls `term.GetSize(int(os.Stdout.Fd()))` which needs fd 1 to remain a TTY. tcell uses `/dev/tty` directly so its I/O path is independent of fds 0/1/2.

   **Notes:**

   - The daemon has its own redirects in `runDaemon`, so daemon-side calls are safe — but **any package that runs in both processes** must assume its slog/log/stderr calls might land in the TUI and must be safe under the redirects.
   - The fix is at the program level (one line per logger plus the Dup2), NOT per call site — there are 100+ slog call sites in `internal/` (`grep -rn 'slog\.' internal/ | wc -l` ≈ 106) and that's exactly why this bug was so insidious.
   - The slog handler uses `nil` `HandlerOptions`, so `slog.Debug` is silently dropped (default level is `Info`). Matches the daemon's behavior. If a future caller needs Debug visibility, use `uxlog.Log` directly rather than `slog.Debug`.
   - **Subprocesses do NOT leak.** Go's `exec.Cmd` defaults nil `Stderr`/`Stdout` to `/dev/null` (NOT inherit), the codebase has zero `cmd.Stderr = os.Stderr` callsites, and (c) doubly ensures any future violation lands in uxlog rather than the terminal.
   - **No remaining known leak paths.** If a future bug report describes "stack trace appeared on screen then argus exited," verify (c) is still in place and the regression tests still pass — those are the load-bearing guards.

**Agent pattern:** A single `readLoop` goroutine is the sole reader of the PTY master fd. It always writes to the ring buffer, and tees output to all attached writers (via `session.writers` slice). Writers are copied under lock before iterating; errored writers are removed automatically. `AddWriter(w)` replays the ring buffer then registers for live output. `Attach()`/`Detach()` use AddWriter/RemoveWriter internally. The detach key (`ctrl+q`) is intercepted by `detachReader` wrapping stdin.

**Terminal rendering:** PTY bytes → x/vt emulator (`charmbracelet/x/vt`) → cells painted directly to `tcell.SetContent()`. No ANSI string intermediary. Damage tracking via `Touched()` enables incremental repainting. Scrollback uses x/vt's native `Scrollback()` buffer. The cursor is rendered unconditionally with high-contrast colors regardless of `CursorVisible()`.

**Daemon pattern:** The daemon (`argus daemon`) owns the Runner and PTY sessions. The TUI connects via Unix socket (`~/.argus/daemon.sock`). First byte on each connection selects the protocol: 'R' for JSON-RPC (request/response), 'S' for output streaming (raw bytes). The TUI's `Client` implements `SessionProvider` so the UI code is identical whether running in-process or via daemon. Sessions survive TUI restarts — the daemon keeps PTY fds alive until explicit stop or shutdown. The TUI auto-starts the daemon if none is running: `autoStartDaemon()` forks the current binary with `Setsid` for process group detachment, then polls the socket until ready (50ms intervals, 3s timeout). Falls back to in-process mode if auto-start fails, with a warning shown in the Settings tab.

**Task/worktree lifecycle:** All fresh-task creation routes through `agent.CreateAndStart` (HTTP API + MCP via `daemon.HeadlessCreateTask`; TUI new-task form and fork directly). It runs in a single goroutine and is fully transactional: CreateWorktree → optional `OnWorktreeCreated` hook (fork context files) → `db.Add` → SessionID generation → `runner.Start` → flip to InProgress. Each side-effecting step registers a LIFO compensating cleanup, so any failure unwinds every prior step — no orphan worktrees, branches, or ghost DB rows. On name conflict, `CreateWorktree` auto-suffixes with `-1`, `-2`, etc. `startSession` in the TUI is reserved for _existing-task restart_ (Enter-to-restart, auto-start on agent-view entry); on failure it reverts status but preserves the row, because the task already existed. On delete/destroy: stops agent → `agent.RemoveWorktreeAndBranch(path, branch, repoDir)` removes worktree (via `git worktree remove` from repoDir) → deletes local + remote branch → removes from DB.

**Git status pattern:** Git operations (worktree discovery, diff, status) must **never** run synchronously on the UI thread. Git commands run in background goroutines and deliver results via `QueueUpdateDraw` callbacks. Resolved paths are cached to avoid repeated lookups.

## Config & Persistence

- Data dir: `~/.argus/`
- Database: SQLite (`data.sql`) via `modernc.org/sqlite` (pure Go, no CGO)
- Backends are command templates with prompt flag interpolation, not SDK integrations

## Breaking Changes Policy

- Only one user (the author) — breaking changes are fine, no backwards compatibility needed
- No legacy migration code — if a schema change requires data migration, write a one-off script
- `internal/store/` (legacy JSON persistence) and `config.toml` support have been removed

## Key Learnings

Non-obvious invariants and gotchas are in `context/knowledge/gotchas/`. **Read the relevant file when working in that area** — they are NOT loaded automatically to save context window space.

@context/knowledge/index.md

### Maintaining Key Learnings

**What belongs in gotcha files:**

- Invariants that caused bugs when violated (e.g., "must do X before Y or Z breaks")
- Non-obvious ordering requirements, race conditions, platform quirks
- Gotchas where the obvious approach silently fails

**What does NOT belong:**

- Architecture descriptions (what code does) — put in the Architecture section above
- Feature descriptions (UI layout, key bindings, panel structure) — discoverable from code
- Development rules (testing, logging, documentation) — put in dedicated sections of CLAUDE.md
- Implementation details that are clear from reading the function

**Format:** Each entry is 1-2 sentences: the rule in bold, then minimal context. Add to the appropriate topic file in `context/knowledge/gotchas/`. If no file fits, add to `gotchas/misc.md`. If a section in `misc.md` grows beyond 10 bullets, promote it to its own file.

### Documentation Requirements

- **Every new feature must have its gotchas documented** in the appropriate `context/knowledge/gotchas/*.md` file before the session ends — but only the non-obvious gotchas, not a description of what the feature does.
- **What to document:** invariants that caused bugs, ordering requirements, platform quirks, silent failure modes. NOT: what the code does, feature descriptions, or UI layout.
- **README.md is marketing copy, not a changelog.** The top half (hero, "Why Argus", the three pillars, "Also In The Box") sells the project to a first-time visitor. Treat it as positioning, not a feature dump. The "Reference" appendix below the `---` is the dense docs surface.
- **When to touch the marketing top:** only when a large swath of new functionality lands — a new pillar-class capability, a new surface (PWA, MCP, KB were each one), or a reframing where the existing prose is now wrong. A single keybinding, config flag, endpoint, or behavior tweak does NOT warrant a top-half edit.
- **When to touch the Reference appendix:** any factual change to keybindings, MCP tool surface, REST endpoints, sandbox defaults, or spinner styles. Keep it precise and update tables in place — don't add narrative.
- **Default to silence.** If the change doesn't shift the value prop or break a documented fact, leave the README alone. Repeated small edits dilute the marketing voice and make the file a noisy diff target.
- **Screenshot policy:** the `screenshots/` directory is curated for marketing impact. Add a new screenshot only when a new pillar-class capability is shipping AND the screenshot shows something visually distinct. Replace stale ones in place rather than accumulating. Empty/sparse screens (splashes, modals on empty backgrounds, settings tabs) don't belong — every screenshot must demonstrate the product doing real work.
- **Bump `SW_VERSION` in `internal/api/static/sw.js` whenever any other shell asset under `internal/api/static/` changes** (`index.html`, `manifest.webmanifest`, vendor JS/CSS). The service worker serves the shell cache-first — without a version bump, every device that already installed the PWA keeps serving the stale shell forever and never sees the change. Increment by 1 (`argus-shell-vN` → `argus-shell-vN+1`).

### Logging Requirements

- **Every new feature must include uxlog calls for debugging.** All async handlers that process results from external systems (git commands, daemon RPC, etc.) must log both success and failure paths via `uxlog.Log("[feature] ...")`. Use a consistent prefix per feature area (e.g., `[tui]`, `[git]`, `[daemon]`).
- **What to log:** fetch results (count/size), errors, state transitions, and any guards that silently skip work (e.g., cooldown timers, staleness checks).

### Testing Requirements

- **Every change must include tests.** Run `make test` to verify all tests pass before considering work complete. **No new code without coverage** — every new function, branch, and error path must be exercised by a test in the same PR. The CI coverage gate enforces a 95% floor (filtered) and PRs that drop the number below the floor are rejected. See [context/knowledge/testing.md](context/knowledge/testing.md) for the full test-author rules (idioms, synctest, mocking, exclusion list).
- **Run `make test-cover` after writing tests** to verify coverage improved. Target ≥95% on packages you touch (90% acceptable for UI smoke-only code).
- **All table-driven tests must use `t.Run` subtests.** Guard slow tests with `testing.Short()`.
- **Test file placement:** `*_test.go` in the same package (not `_test` suffix). Use existing `testDB(t)` helpers.
- **What to test:** exported functions, pure logic (parsers, state transitions), view/render output, edge cases (nil, empty, boundaries), state machines.
- **OK to skip:** real terminal functions (raw mode, ioctl), external process shelling, `cmd/argus/main.go`.
- **Testing patterns:** `db.OpenInMemory()`, `agent.NewRunner(nil)`, `exec.Command("echo")` / `exec.Command("sleep")`, `DefaultTheme()`, table-driven with `t.Run`. Keep daemon client test names short (macOS 104-byte socket path limit).
- **CRITICAL: Tests must NEVER operate on real `~/.argus/` paths.** All worktree paths, data dirs, and file operations in tests MUST use `t.TempDir()`. A runtime `testGuard` in `internal/agent/cleanup.go` blocks deletions on real `~/.argus/` during `go test` as a safety net, but tests should be designed correctly in the first place.
- **Tests that exercise `agent.CreateAndStart` or anything that calls `WorktreeDir()` / `db.DataDir()` MUST `t.Setenv("HOME", t.TempDir())` before the call.** These helpers resolve through `$HOME`, so without the override they write to the real `~/.argus/worktrees/`. `testGuard` also exempts paths under `os.TempDir()` for exactly this case, so the HOME redirect is compatible with the safety net.
- **CRITICAL: Tests must NEVER connect to or affect the live argus daemon.** Use `agent.NewRunner(nil)` (not a real daemon client). Never dial the Unix socket (`~/.argus/daemon.sock`). Never send signals to the daemon PID.
- **Any change to tview screen setup (SetScreen, EnablePaste, EnableMouse, screen wrapping) must include a SimulationScreen integration test** verifying the feature works end-to-end. See `internal/tui/smoke_test.go` for the pattern: `simApp(t)` creates a `lazyScreen`-wrapped SimulationScreen with correct Enable ordering; `wireApp(t, app)` wires a full `App` to a SimulationScreen for smoke tests; `runApp(t, app)` manages the event loop lifecycle.
- **Major UI paths (tab switching, modal open/close, paste, agent view enter/exit) must have smoke tests** in `smoke_test.go` that exercise the real tview event loop. These catch setup-ordering bugs and event routing regressions that unit tests on individual handlers miss.
- **Every page wrapper or layout container with non-interactive child panels must have a `MouseHandler` that guards `setFocus`.** tview's default `Box.MouseHandler()` steals focus on click. Non-interactive panels (no `InputHandler`) silently drop all keyboard input when focused. The fix is to wrap `setFocus` in the page's `MouseHandler` to always redirect to the interactive panel. See `TaskPage.MouseHandler()` for the pattern. **Any new page wrapper must include a `TestSmoke_Click*` test** that injects a mouse click on a non-interactive area and verifies focus stays on the intended widget.
- **Widgets with conditional Draw branches may optionally surface an `OnBranchChange` / `OnLayoutChange` callback the App wires to `forceRedraw`** — purely as a debug-trail signal in `~/.argus/ux.log`. **The callback does NOT trigger `screen.Sync()`** (forceRedraw is log-only since the May 2026 cleanup — see the "UX rendering" rules above and the post-mortem in `gotchas/ui-threading.md`). Stale-cell prevention is handled by `tview.Clear()` running every draw cycle plus widgets calling `widget.FillArea` / `widget.DrawBorderedPanel` to cover their full bounding rect — NOT by Sync. If you add a new conditional-branch widget and want the debug trail, ship a smoke test that asserts the `[tui] force redraw: ...` log entry (pattern: `TestSmoke_FilterToggleFiresRedraw`); if you don't need the debug trail, skip the callback entirely.

## Planned but Not Yet Implemented

- Task import from markdown/JSON (`internal/import/`) — Phase 4

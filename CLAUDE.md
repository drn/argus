# CLAUDE.md

Guidance for Claude Code (claude.ai/code) working in this repo.

## What This Is

Argus — a terminal-native LLM code orchestrator (Go + tcell/tview). Manages multiple Claude Code / Codex sessions with task tracking, git worktree isolation, and a keyboard-driven workflow.

## Spec-Driven Development (OpenSpec)

**Route every spec-worthy change through `openspec/` before writing code.** Any behavioral change — new feature, changed behavior, new endpoint/keybinding/MCP tool, altered invariant — gets a change folder first:

1. Create `openspec/changes/<name>/` with `proposal.md`, delta specs under `specs/<capability>/spec.md`, and `tasks.md`.
2. Get approval before implementing.
3. Implement against the tasks, keeping deltas in sync as requirements shift.
4. **Archive within the same PR, before merge** — run `openspec archive <name>` (or apply it by hand: merge the delta requirements into the base specs under `openspec/specs/<capability>/` and move the change folder to `openspec/changes/archive/<YYYY-MM-DD>-<name>/`) and commit the result on the change branch. Merge applies the work immediately, so the base-spec update must land atomically with the PR — do NOT leave archiving as a separate post-merge step (that strands the base specs behind shipped code). The `openspec` CLI may be absent; the manual merge-and-move is the fallback and produces the identical tree.

Skip the change folder only for genuinely non-behavioral work: docs, comments, formatting, test-only edits, mechanical refactors. When unsure, write the change.

**Specs are LOCAL DOCS only** (see `openspec/project.md`): nothing in CI, the Makefile, or the Go build reads them, and that stays true. Never wire `openspec validate` (or any spec tooling) into Go CI or a make target. The quality gate is and stays `make pre-pr`.

## Build & Run

```bash
make build          # go build ./...
make vet            # go vet ./...
make test           # go test -race -count=1 ./...
make test-pkg PKG=./internal/db/   # single package, verbose
make test-cover     # coverage profile + summary
make test-cover-gate # race suite + coverage floor (-min 88; matches CI gate)
make test-watch     # gotestsum --watch
make fmt            # goimports -w . (format the tree)
make fmt-check      # fail if any file is not goimports-clean (matches CI)
make vuln           # govulncheck ./...
make lint-pr        # golangci-lint --new-from-rev=origin/master (matches CI)
make pre-pr         # full CI mirror: build+vet+fmt-check+lint-pr+vuln+test-cover-gate
go build -o argus ./cmd/argus/
```

## Before Opening a PR

**`make pre-pr` must pass clean before opening OR updating any PR — non-negotiable; do not `git push` a PR branch until it does.** It mirrors `.github/workflows/ci.yml` step-for-step (build → vet → fmt-check → lint-pr → vuln → test-cover-gate), so a green `pre-pr` means green CI. Steps short-circuit on first failure, so run the **full** gate locally to surface everything at once. Per-gate failure recipes: `context/knowledge/gotchas/ci-gates.md`.

## Test-Driven Development

Red-Green-Refactor default. `make test-watch` for continuous feedback, `make test-pkg` for one package. Use `internal/testutil` assertions (`Equal` / `DeepEqual` (go-cmp) / `NoError` / `ErrorIs` / `Nil` (nil-interface-safe) / `Contains`), never raw `if got != want`. All table tests use `t.Run` subtests; guard slow tests with `testing.Short()`.

## Architecture

tcell/tview UI with direct cell painting for the agent terminal. `App` (`internal/tui/app.go`) owns the `tview.Application`, DB, runner, and all sub-views; routes keys via `SetInputCapture`, switches views via `tview.Pages`, lays out with `tview.Flex` (header + pages + statusbar).

**Package map** (read the code for detail; only non-obvious wiring is called out):

- `cmd/argus/main.go` — entry; parses subcommands (`daemon`), opens SQLite; TUI tries the daemon client first, falls back to an in-process runner. `cmd/argus/remote.go` — `--remote URL --token` entry: no local SQLite/socket/runner, REST-only via `apistore` + `apiclient`.
- `internal/tui/` — all views (tasklist, terminalpane, gitstatus, fileexplorer, settings, newtaskform, taskpage). `internal/tui/hera/` — native Hera view (rail / coordinator pane / details), split across `model`/`rail`/`panes`/`details`/`tree`/`focus`/`ops`/`refresher`. `internal/tui/store/` — narrow interface that both `*db.DB` (local) and `*apistore.Store` (remote) satisfy implicitly; `assert_test.go` catches signature drift.
- `internal/agent/` — PTY process mgmt: `agent.go` (`BuildCmd`, `--session-id`), `worktree.go`, `session.go` (single `readLoop` tees ring buffer + all attached writers), `runner.go` (`SessionProvider` keyed by task), `ringbuffer.go`, `iface.go` (`SessionProvider`/`SessionHandle` — the only interfaces UI code depends on).
- `internal/daemon/` — `daemon.go` (owns Runner + Unix socket; first byte 'R'/'S' selects RPC vs stream), `sessioncore.go` (the R/S server both daemon and supervisor mount; hosts the `#707` `awaitExitInfoCached` guard), `supervisor.go` (the dark long-lived PTY owner on its own sock/pid/lock trio), `headless.go` (TUI-less task create), `client/` (TUI-side daemon client).
- `internal/api/` — REST + PWA on :7743, binds `127.0.0.1` + the Tailscale IP only, never `0.0.0.0`. `internal/push/` — Web Push (VAPID). `internal/mcp/` — MCP server :7742, native `hera_*` tools. `internal/db/` — SQLite at `~/.argus/data.sql`; `hera.go` + `hera_messages.go` are the Hera store. `internal/hera/service.go` — role-addressed delivery over the existing `notify.Notifier` bus.
- Others: `internal/config`, `internal/gitutil` (pure Go, off-UI-thread), `internal/spinner`, `internal/skills`, `internal/uxlog`, `internal/apiclient`, `internal/apistore`, `internal/model` (`Task` + `Status`: `pending → in_progress → in_review → complete`), `cmd/argus-test-server` (Playwright harness).

**Key patterns** (non-obvious; the gotcha files hold the invariants):

- **Widgets** are custom `tview.Box` with `Draw(screen)`. Async updates via `tapp.QueueUpdateDraw()`. **Every text-input widget must implement `PasteHandler()`** — bracket paste bypasses `InputCapture`, so widgets without it silently drop pasted text; PTY widgets wrap pasted text in `\x1b[200~`/`\x1b[201~`.
- **Rendering:** PTY bytes → x/vt emulator → cells painted to `tcell.SetContent()` (no ANSI intermediary), damage-tracked via `Touched()`. **Do NOT add `screen.Sync()` to fix "tearing"** — only 3 repair callsites are legit (resize, Ctrl+L, tmux focus-regain); everything else flows through tcell's per-cell diff. **Do NOT write to `os.Stderr`/`os.Stdout` after `tcell.Screen.Init`.** Both rules + the full 12-commit post-mortem + the fd-2 guards: `gotchas/ui-threading.md` (read before any render code). PTY-size / SIGWINCH invariants: `gotchas/pty-terminal.md`.
- **Agent:** one `readLoop` is the sole PTY reader → ring buffer + all attached writers; `AddWriter` replays the ring then registers for live output. `ctrl+q` detach via `detachReader`.
- **Daemon/supervisor split:** the daemon owns coordination (hera, REST API, MCP, scheduler, DB, TUI socket) and is bounce-able; the session-supervisor owns the agent PTYs and is long-lived. The daemon proxies sessions to the supervisor over `~/.argus/supervisor.sock`, so bouncing the daemon re-attaches to running agents — only restarting the *supervisor* SIGHUPs them. Gated `cfg.Supervisor.Enabled` (default ON; `false` ⇒ legacy in-process path, byte-identical, kept one release). The daemon auto-starts a `Setsid`-detached supervisor if the socket is silent.
- **Hera (native multi-agent):** roles (coordinator / worker / freelance) bound to argus tasks, addressed by name; reuses existing primitives — the bus is `notify.Notifier` (idle-gated, exactly-once), worker spawn is `agent.CreateAndStart` + a transactional role-binding insert. The details pane renders the orchestration tree (role hierarchy), also the source for the subtree TLDR roll-up. One live binding per (task, orchestrator); a task may join several orchestrators. Gated `cfg.Hera.Enabled` (default ON) — now only governs daemon-side MCP native-vs-plugin tool registration; the TUI's 2nd tab is always native Hera. Native and the external plugin are mutually exclusive and share no state.
- **Retired: the `depends_on` DAG.** `depswatcher`, `task_link`/`unlink`/`deps`/`halt_downstream`/`set_plan_slug`, `/api/dag`, the DAG tab + SPA view — all removed for Hera. `internal/orch` + `internal/depswatcher` deleted; `Task.DependsOn`/`PlanSlug` + columns gone. **`base_branch` was kept** — the git-stacking mechanic (branch a worktree off another task's branch), independent of the retired gating; a coordinator stacks PRs by spawning sequential workers, each branched off the previous.
- **Task/worktree lifecycle:** all fresh-task creation routes through `agent.CreateAndStart` — single goroutine, fully transactional (CreateWorktree → `OnWorktreeCreated` hook → `db.Add` → SessionID → `runner.Start` → InProgress), each step LIFO-compensated on failure (no orphan worktrees/branches/rows); auto-suffixes name conflicts. `startSession` is existing-task restart only (reverts status but keeps the row on failure). Delete: stop agent → `RemoveWorktreeAndBranch` → delete local + remote branch → DB delete.
- **Git ops never run synchronously on the UI thread** — background goroutines deliver via `QueueUpdateDraw`; resolved paths are cached.

## Config & Persistence

- Data dir `~/.argus/`; SQLite `data.sql` via `modernc.org/sqlite` (pure Go, no CGO).
- Backends are command templates with prompt-flag interpolation, not SDK integrations.

## Breaking Changes Policy

- One user (the author): breaking changes are fine, no backwards compatibility, no legacy migration code (write a one-off script for schema data moves). `internal/store/` (legacy JSON) and old `config.toml` support have been removed.

## Key Learnings

Non-obvious invariants and gotchas live in `context/knowledge/gotchas/`. **Read the relevant file when working in that area** — they are NOT auto-loaded, to save context.

@context/knowledge/index.md

### Maintaining Key Learnings

Gotcha files hold: invariants that caused bugs when violated, ordering requirements, race conditions, platform quirks, silent-failure modes. They do NOT hold: architecture / what-code-does (→ Architecture above), feature/UI descriptions (discoverable from code), or dev rules (→ the dedicated sections here). Format: 1–2 sentences — rule in bold + minimal context — in the right topic file (else `misc.md`; promote a `misc.md` section past 10 bullets to its own file).

### Documentation Requirements

- **Every new feature documents its non-obvious gotchas** in `context/knowledge/gotchas/*.md` before the session ends — invariants / ordering / quirks / silent-failures, NOT what the code does.
- **Adding, removing, or rebinding ANY TUI key REQUIRES updating the keymap + help in the same PR.** Rebindable keys route through `internal/tui/keymap` (`defaultSpecs` + `actionLabels` + `contextOrder` are the single source of truth) and the `?` overlay is GENERATED from it via `modal.SectionsFromKeymap`. A new rebindable action = add it in `keymap` (the help row appears automatically) + a `keymap.Resolve` case at the dispatch site + assertion in `help_test.go` (`TestHelpModal_Draw`), then mirror into the README Reference keybinding table. Structural/non-rebindable keys stay literal in the handler and as static rows in `help.go` (`helpLayout` extras / `staticHelpSections`). See `gotchas/keybindings.md`.
- **README.md is marketing, not a changelog.** The top half (hero / Why Argus / pillars / Also In The Box) is positioning — touch it only when a pillar-class capability or a new surface lands, or existing prose is now wrong. The Reference appendix (below `---`) is the dense docs surface — update its tables in place for any factual change (keybindings, MCP tools, REST endpoints, sandbox defaults, spinner styles). Default to silence; a single key/flag/endpoint tweak does not warrant a top-half edit.
- **Screenshots** (`screenshots/`) are curated for marketing: add one only for a pillar-class, visually-distinct capability; replace stale ones in place; no empty/sparse screens.
- **Bump `SW_VERSION` in `internal/api/static/sw.js`** whenever any other shell asset under `internal/api/static/` changes — the service worker serves the shell cache-first, so without a bump installed PWAs never see the change.

### Logging Requirements

- **Every new feature includes uxlog calls.** All async handlers that process external results (git, daemon RPC) log both success and failure via `uxlog.Log("[feature] ...")` (consistent prefix per area). Log fetch results (count/size), errors, state transitions, and silently-skipped work (cooldowns, staleness guards).

### Testing Requirements

- **Every change includes tests; no new code without coverage.** `make test` must pass. CI gate enforces a 95% filtered floor — PRs dropping below it are rejected. Full author rules: `context/knowledge/testing.md`. Run `make test-cover` after; target ≥95% on touched packages (90% for UI smoke-only code).
- Table tests use `t.Run`; guard slow with `testing.Short()`. Test files in-package (not `_test` suffix); use `testDB(t)`. Test exported fns, pure logic, render output, edge cases, state machines. Skip: raw terminal fns, external process shelling, `cmd/argus/main.go`. Patterns: `db.OpenInMemory()`, `agent.NewRunner(nil)`, `exec.Command("echo"/"sleep")`, `DefaultTheme()`. Keep daemon-client test names short (macOS 104-byte socket-path limit).
- **CRITICAL — tests NEVER touch real `~/.argus/`.** All paths via `t.TempDir()`. Tests hitting `agent.CreateAndStart` / `WorktreeDir()` / `db.DataDir()` MUST `t.Setenv("HOME", t.TempDir())` first (these resolve through `$HOME`). The `testGuard` in `internal/agent/cleanup.go` blocks real-`~/.argus/` deletions during `go test` (exempts `os.TempDir()`) as a net — design correctly anyway.
- **CRITICAL — tests NEVER touch the live daemon.** Use `agent.NewRunner(nil)`; never dial `~/.argus/daemon.sock`; never signal the daemon PID.
- **Any tview screen-setup change (SetScreen / EnablePaste / EnableMouse / wrapping) needs a SimulationScreen integration test** (`smoke_test.go`: `simApp` / `wireApp` / `runApp`). Major UI paths (tab switch, modal open/close, paste, agent enter/exit) need smoke tests exercising the real event loop.
- **Every page wrapper / layout container with non-interactive child panels needs a `MouseHandler` guarding `setFocus`** (tview's default steals focus on click → non-interactive panels drop all keys). See `TaskPage.MouseHandler()`; ship a `TestSmoke_Click*` test asserting focus stays on the intended widget.
- **`OnBranchChange` / `OnLayoutChange` callbacks are a log-only debug trail (NOT Sync)** — see the rendering rules above. If you add one, ship a smoke test asserting the `[tui] force redraw: ...` log line (`TestSmoke_FilterToggleFiresRedraw`); otherwise skip the callback.

## Planned but Not Yet Implemented

- Task import from markdown/JSON (`internal/import/`) — Phase 4

## 1. `internal/claudeagents` package (TDD)

- [x] 1.1 New package `internal/claudeagents`: `Session` struct (`pid`, `cwd`, `kind`, `startedAt`, `id`, `state`, `status`, `waitingFor`, `sessionId`, `name`), `Session.Alive()` (pid present), `Session.Backgrounded()` (kind == "background").
- [x] 1.2 `ErrUnavailable` sentinel + `DefaultTimeout` (10s) constant.
- [x] 1.3 `cmdFactory` exec-seam var (mirrors `internal/llm`'s `nameGenCmd`), swappable in tests.
- [x] 1.4 `List(ctx, cwd) ([]Session, error)`: `claude agents --json` (+ `--cwd <dir>` when non-empty), JSON-unmarshal the array.
- [x] 1.5 `Stop(ctx, id) error`: `claude stop <id>`, wraps non-zero exit with captured output.

## 2. `internal/agent` wiring

- [x] 2.1 New file: `listBackgroundSessionsFn`/`stopBackgroundSessionFn` package vars defaulting to `claudeagents.List`/`claudeagents.Stop` (mirrors `autoRenameFn`).
- [x] 2.2 `reapOrphanedClaudeSessions(taskID, worktreeDir string) []string`: list scoped to `worktreeDir`, filter `Backgrounded() && Alive()`, stop each, `uxlog` every outcome (stopped, list failure, stop failure), swallow all errors.
- [x] 2.3 Wire `go reapOrphanedClaudeSessions(taskID, sess.WorkDir())` into `Runner.Stop`, after the existing `sess.Stop()` call — return value/error of `Runner.Stop` unchanged.

## 3. Tests

- [x] 3.1 `internal/claudeagents`: JSON parsing table test (interactive-only, mixed, malformed); `ErrUnavailable` via `t.Setenv("PATH", t.TempDir())`; fake-`claude`-script driven `List`/`Stop` tests asserting args (`--cwd` present/absent, `stop <id>`) and non-zero-exit error wrapping.
- [x] 3.2 `internal/agent`: `reapOrphanedClaudeSessions` unit tests via the swapped fn vars — no live sessions, background+alive stopped, interactive skipped, exited-background (no pid) skipped, list error swallowed, stop error swallowed and logged.
- [x] 3.3 `Runner.Stop` test: fake `stopBackgroundSessionFn` signals via a channel; assert `Stop` returns promptly (goroutine, not blocking) and the fake was invoked with the expected id.

## 4. Docs & gates

- [x] 4.1 Extend `context/knowledge/gotchas/daemon-rpc.md`'s "Claude Code's own background-session supervisor" section (added during investigation) with the shipped fix's shape (package name, filter rule, fire-and-forget goroutine) so the gotcha reflects what actually landed, not just the diagnosis.
- [x] 4.2 `make pre-pr` green.
- [x] 4.3 Archive this change within the branch (base `agent-execution` spec updated atomically) before opening the PR.

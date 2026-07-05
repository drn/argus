## Context

Investigation (this same orchestrator, `orphaned-workers`) confirmed the root cause of a hera worker task that argus believed was stopped but whose agent kept running: the session had been backgrounded to Claude Code's own per-user supervisor process, a process-management plane entirely outside anything argus spawns or signals. The trigger was a literal Ctrl+Z reaching the PTY; two sibling workers are separately closing that hole in the web/PWA and macOS clients. This change is the complementary, general-purpose cure: regardless of what caused a session to detach, argus's stop path should also check whether Claude Code itself still has a live background session under that task's worktree, and stop it.

## Decision 1 — New `internal/claudeagents` package, not folded into `internal/llm`

`internal/llm` already shells out to the `claude` CLI (`GenerateName`, for auto-naming), and its `ErrUnavailable` sentinel + exec-factory test-seam pattern (`nameGenCmd`) is exactly the shape this needs. But `internal/llm`'s docstring and design are specifically about LLM inference calls (model selection, budget caps, retry-on-transient-failure, prompt framing) — `claude agents`/`claude stop` do not invoke the model at all; they query and mutate Claude Code's own session-registry/job-control surface. Folding an unrelated concern into `internal/llm` would make the package harder to reason about for both consumers. A small, single-purpose `internal/claudeagents` package is the same amount of code either way, but names the actual concern and lets `internal/agent` (this change) and a future `argus doctor` check (see Out of Scope) both depend on it without pulling in `internal/llm`'s heavier LLM-specific machinery.

## Decision 2 — Reap in `Runner.Stop`, not `Session.Stop`

`Session.Stop` is a low-level primitive (`Cmd.Process.Signal`) with no knowledge of the task ID or worktree path beyond `Cmd.Dir`. `Runner.Stop` already resolves the `*Session` for a `taskID` and is the single choke point every real stop path (`task_stop` MCP tool, daemon RPC, REST endpoint, TUI stop/delete actions) already funnels through — adding the reap call there means every one of those callers gets the fix with no per-caller wiring. `KickRerender`'s internal `sess.Stop()` call bypasses `Runner.Stop` deliberately (see proposal's Out of Scope) since it is a restart, not a user-intended stop.

## Decision 3 — Fire-and-forget goroutine, not inline in the Stop call

`claude agents --json` is a real CLI invocation (Node.js process startup), not a syscall — even a fast path costs on the order of hundreds of milliseconds. `Runner.Stop` today returns as soon as the SIGTERM is sent, without waiting for the process to actually exit; adding a synchronous CLI round-trip would make every stop measurably slower for the overwhelmingly common case where there is nothing to reap. The reap runs in `go reapOrphanedClaudeSessions(...)`, bounded by `claudeagents.DefaultTimeout` (10s) so a hung `claude` binary can only leak one goroutine, never block a caller. This mirrors the existing `runAutoRename` fire-and-forget pattern in the same package.

## Decision 4 — Only stop entries reported as `kind: "background"` with a live `pid`

`claude agents --json --cwd <dir>` returns every session under that directory, including argus's own tracked interactive session (verified empirically: dispatching an in-session sub-agent and querying `claude agents --json --cwd $PWD` showed only the top-level `"kind": "interactive"` row, never the sub-agent). Filtering to `kind == "background"` means this path can never target the same process the SIGTERM just signaled — no double-stop race, no risk of `claude stop` doing something unexpected to a session argus is actively managing. Requiring a live `pid` (only present in the CLI's JSON while the OS process is alive, per Claude Code's docs) skips background entries that have already exited or never started — nothing to stop, and calling `claude stop` on them would be a wasted round-trip at best.

## Decision 5 — `claude stop` uses the short `id`, never the session UUID

Confirmed directly from the original incident and Claude Code's own CLI: `claude stop <full-session-uuid>` fails with `No job matching '<uuid>'` — the short `id` field (distinct from `sessionId`) in the `claude agents --json` row is the only accepted identifier. `internal/claudeagents.Stop` takes exactly that `id` field; callers must not pass `SessionID`.

## Decision 6 — Fail open on every error, never surface a new stop failure

A missing `claude` binary, a `claude agents --json` parse failure, or a `claude stop` non-zero exit are all logged via `uxlog` (mirroring the `[autoname]` log-line convention) and otherwise swallowed. `Runner.Stop`'s return value and error semantics are completely unchanged — this is additive best-effort cleanup layered on top of, never a replacement for or a new failure mode of, the existing stop path.

## Testing

- `internal/claudeagents`: pure JSON-parsing test table (interactive-only list, mixed interactive+background, malformed JSON); `ErrUnavailable` when `claude` is absent from `PATH` (`t.Setenv`); a fake `claude` shell script (mirroring `internal/llm`'s `setupFakeClaude`/`writeExec` pattern) driving `List`/`Stop` end-to-end including argument capture (`--cwd` passed correctly, `stop <id>` invoked with the right id) and non-zero-exit error wrapping.
- `internal/agent`: a swappable `listBackgroundSessionsFn`/`stopBackgroundSessionFn` pair (mirroring `autoRenameFn`) so `reapOrphanedClaudeSessions`'s filtering logic (kind/pid gating, multiple entries, list/stop errors) is tested without any real subprocess. A `Runner.Stop` test asserts the goroutine fires (synchronized via a channel in the faked `stopBackgroundSessionFn`, not a sleep) and that `Stop`'s own return value/timing is unaffected by a slow or failing fake.

## Why

**Argus's stop path can never kill a Claude Code session that has already backgrounded itself — this is not a signal-scoping bug, it is a completely different, argus-invisible process-management plane.**

Every stop path in argus (MCP `task_stop`, the daemon `StopSession` RPC, the REST `/api/tasks/{id}/stop` endpoint, and every TUI stop/delete/kick-rerender action) bottoms out in `Runner.Stop` → `Session.Stop()` → a single `SIGTERM` to the one PID argus spawned. Per Claude Code's own documentation (code.claude.com/docs/en/agent-view), a session can be "backgrounded" — via `/bg`, `/background`, arrow-key detach, or a literal Ctrl+Z reaching its PTY — at which point it becomes "a detached process" hosted by "a per-user supervisor process, separate from your terminal." Once that happens the session is reparented out of argus's process tree permanently; no signal argus sends to the original PID (even a hypothetical process-group-wide one) can ever reach it again.

Live incident: a hera worker's session backgrounded itself (root-caused to a literal Ctrl+Z reaching the PTY — a footgun the TUI already independently discovered and fixed twice, but only in the TUI; see `context/knowledge/gotchas/hera-view.md`, `web-remote.md`, `macos-app.md`). Argus's own bookkeeping believed the task was stopped (flipped to `in_review`), but the real Claude Code process kept running under Claude Code's own supervisor, invisible to argus. Reviving the task then collided with Claude Code's own "session already running as a background agent" guard. The only way found to actually stop it was Claude Code's own CLI: `claude agents --json --cwd <dir>` to find the short id, then `claude stop <id>`.

This proposal is the general fix: teach argus's stop path to also check Claude Code's OWN background-session registry, so a stop catches an orphan regardless of what caused the detachment (Ctrl+Z, or any future/other trigger this investigation didn't find).

## What Changes

- **A new `internal/claudeagents` package** wraps the `claude agents --json --cwd <dir>` / `claude stop <id>` CLI surface: list sessions Claude Code itself is tracking under a given working directory, and stop one by its short id (not a session UUID — `claude stop <uuid>` fails with "No job matching"). Fails open (`ErrUnavailable`) when the `claude` CLI is missing from PATH, mirroring the existing `internal/llm` GenerateName pattern.
- **`Runner.Stop` additionally reaps any live Claude Code background session under the stopped task's worktree**, after signaling the tracked PID as before. This runs in a background goroutine (fire-and-forget, bounded by a short timeout) so a `claude` CLI round-trip never adds latency to a stop request — the overwhelming common case is "nothing to reap," and the existing SIGTERM behavior is completely unchanged for that case.
- Only sessions Claude Code itself reports as `kind: "background"` and currently alive (`pid` present) are stopped — argus's own tracked interactive session for that task is never targeted by this path, avoiding any double-stop race with the SIGTERM already in flight.
- All failures (CLI missing, list error, stop error) are logged via `uxlog` and swallowed — this is best-effort cleanup layered on top of the existing stop, never a new way for `task_stop` to fail or block.

## Capabilities

- `agent-execution` — `Runner.Stop` gains a best-effort side effect that also stops any Claude-Code-hosted background session still alive under the task's worktree, closing the gap the existing SIGTERM-only stop semantics cannot reach.

## Out of scope

- **`argus doctor` detection of an already-orphaned session** (surfacing the "argus thinks this task is stopped but a live `claude agents` entry still exists" mismatch as a read-only diagnostic) — a real, separate feature: `doctor`'s existing model (`doctor.Actor` / `doctor.Diagnose`) is built entirely around comparing six binary identities, has no DB access today, and has no per-task reporting shape. Bolting a per-task cross-reference check onto it is a distinct, comparably-sized change and is left as a named follow-up rather than a half-integration here.
- **Preventing the detachment in the first place** (adding the TUI's existing Ctrl+Z-before-PTY guard to the web/PWA client and the macOS app) — dispatched separately to two other workers in this same orchestrator; this change is the cure, not the prevention.
- No change to `KickRerender`'s internal `sess.Stop()` call (the rerender-restart path) — it stops and immediately restarts the SAME task, so an orphan check there is lower-value and out of scope; only the named `Runner.Stop` (the actual `task_stop` path) is touched.
- No change to `sendBounceSignals`/`ARGUS_BOUNCED` or any other daemon-lifecycle notification — this is a pure best-effort process cleanup, not a new agent-facing signal.

## Why

Resuming a Claude task whose conversation is still alive in Claude Code's own per-user background supervisor (detached via `/bg`, `←`, or a stray Ctrl+Z) fails with "That session is running in the background (<id>). Run `claude attach <id>` ... or `claude stop <id>` first to resume it here." The existing reap only runs from `Runner.Stop`, which never fires when the tracked PTY child simply exited after detaching, so the task is stuck until the user runs `claude stop <id>` by hand.

## What Changes

- `Runner.Start` with `resume=true` on a Claude backend synchronously stops the Claude Code background session hosting the task's own session id (matched on `sessionId`, under the task's worktree) before building the launch command.
- Fails open: CLI missing, list error, or stop error are logged (`[bgreap]`) and the resume proceeds unchanged.
- Shared list/filter/stop body with the stop-time reap; the stop-time behavior is unchanged.

## Capabilities

- `agent-execution` — resume gains a pre-launch reap of the conversation's own background session.

## Out of scope

- Detecting the error text in the pane and retrying — the pre-launch reap removes the cause, so a retry path is redundant.
- Frontend changes: `Runner.Start` is the shared choke point for TUI, REST, and hera revive, so all three surfaces are covered with no client work.

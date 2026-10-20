## Why

`hera_send`'s auto-revive (add-hera-send-auto-revive) runs the full `hera_revive` gate before every coordinator message. That gate treats "alive, idle (no PTY output for 3s), not parked at a prompt" as *stuck* and kicks the session: SIGTERM the agent, then resume it via `--session-id`. That signature is simply the normal resting state of any worker between turns, or of a worker waiting on a background job. So every coordinator message to an idle worker restarted it, and the agent's own exit tore down its in-session background shells (Claude Code's `run_in_background` jobs).

Ground truth from `~/.argus/daemon.log`, 2026-10-18/19: 120 `outcome=kicked_stuck` revives across 7 orchestrators, each followed within about a second by `session exited stopped=true exit status 143 pending=true`. In `sketch-guides-docs`, the video workers' 20-minute frame renders and encodes died at exactly those moments, and the workers' transcripts report "another session restart" seconds after each kick. The coordinator's message telling workers to launch jobs under `nohup` itself caused one of the restarts.

The kick adds nothing on the send path. Delivering the message is what wakes an idle agent: the reliable-notify path types into its composer and submits.

## What Changes

- `hera_send`'s auto-revive becomes **dead-only**. A recipient with no live process is still restarted in place before delivery, exactly as before. A live recipient is never kicked, stopped, or restarted by a send, whatever its idle/blocked state, and the response reports a new `skipped_alive` outcome.
- `internal/hera` gains `ReviveDeadRole` (a dead-only sibling of `ReviveRole`) and the `ReviveSkippedAlive` outcome. `mcp.HeraReviveInput` gains `DeadOnly`, and the daemon's reviver dispatches on it.
- Explicit `hera_revive` keeps its kick. Its tool description and the hera skill now say plainly that a kick restarts the agent and kills the agent's in-session background jobs, so it is a last resort for a session that is truly wedged, never a progress check.
- The hera skill gains worker guidance on verification scope and on surviving restarts with long-running jobs, and a coordinator rule to check progress rather than ping. It also corrects §4's false claim that `hera_revive` is "a safe … no-op" for a healthy role.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-messaging`: "hera_send auto-revives a dead or stuck recipient" becomes "hera_send auto-revives a dead recipient". A live recipient is left untouched with a `skipped_alive` outcome.

## Impact

- `internal/hera/revive.go` (+ tests), `internal/mcp/server.go`, `internal/mcp/hera.go` (+ tests), `internal/daemon/daemon.go`, `internal/daemon/revive.go` (+ tests).
- `internal/skills/builtin/hera/SKILL.md`, `context/knowledge/gotchas/messaging.md`, `context/knowledge/index.md`.
- No REST, TUI, web or macOS surface change: `hera_send` is MCP-only, and the TUI's Enter-key revive is operator-initiated and unchanged.
- No schema change, no supervisor protocol change.

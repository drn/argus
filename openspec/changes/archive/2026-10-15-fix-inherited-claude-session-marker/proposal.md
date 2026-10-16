## Why

Claude Code exports per-session runtime markers (`CLAUDECODE`, `CLAUDE_CODE_CHILD_SESSION`, `CLAUDE_CODE_SESSION_ID`, its messaging socket/token, …) into every process it launches. When the Argus supervisor or daemon is (re)started from inside a Claude Code session, those markers flow through `os.Environ()` into every agent Argus spawns. Claude Code then treats each agent as a child session and turns transcript saving off ("Transcript saving is off — inherited CLAUDE_CODE_CHILD_SESSION marker"). Argus depends on those transcripts for resume, session discovery and coordinator context tracking. Confirmed live on 2026-10-15: the supervisor restarted from a Claude session carried the marker, and all seven running agents inherited it.

## What Changes

- Every agent spawn strips the parent Claude Code session's runtime markers from the inherited environment, for every backend.
- Every Claude-family spawn exports `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1`, so transcript saving stays on even if a marker reaches the agent some other way.
- The daemon's headless Claude `/usage` probe strips the same markers and sets the same flag.
- Stripped names are logged (names only, never values), mirroring the existing `[account]` inherited-env log.
- Claude session discovery ignores SDK-launched transcripts (`entrypoint` `sdk-*`). With the real transcript missing, session-ID capture had been adopting a security-review hook's `claude -p` transcript as the task's session (about 15 tasks on 2026-10-15), so resume targeted the hook's run.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `agent-execution`: spawned agents never inherit a parent Claude Code session's runtime markers, Claude agents always persist transcripts, and session discovery ignores SDK-launched transcripts.
- `usage-budget-routing`: the Claude usage probe runs with the same scrubbed environment.

## Impact

- Code: `internal/claudesession/claudesession.go` (`ListIn`), `internal/agent/claudeaccount.go` (`spawnBaseEnv`), `internal/agent/agent.go` (`BuildCmd` Claude env), `internal/usagebudget/usagebudget.go` (`startPTYSession`).
- Changes the supervisor spawn surface: the spawn-surface digest must be re-recorded, and the live supervisor needs a restart (stops running agents) to pick it up.
- No REST, wire-protocol or UI change; no frontend parity impact.

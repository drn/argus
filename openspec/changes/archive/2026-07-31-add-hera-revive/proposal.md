## Why

When the session-supervisor restarts, it SIGHUPs every PTY it owns. A hera worker's session survives as a live-but-stuck (SIGTSTP'd) process, or dies outright — either way it stops making progress. Today the only way to notice and fix this is a human watching the TUI: pressing `Enter` on the role's rail row drives `reviveHeraWorker` (kick a stuck-but-alive session back to life) or `startSession` (restart a dead one). A hera coordinator — itself just another agent session, not a human at a keyboard — has no way to notice a bound role has gone quiet and do anything about it. It can only wait, or spawn a wasteful duplicate worker (a mistake already documented in `hera-dont-declare-worker-dead-without-live-confirmation` memory).

This change gives a coordinator a new MCP tool, `hera_revive`, that inspects one role it coordinates and, if it's dead or genuinely stuck, revives it — reusing the exact same safety gate (idle, not blocked on a prompt) the TUI's `Enter` key already enforces, so it can never thrash a session that's actually working or waiting on an answer. If the role is fine, the tool says so and does nothing. This is deliberately pull/on-demand — a coordinator calls it when `hera_status`/`hera_tree_updates` shows no progress — not an automatic daemon-side trigger on supervisor restart.

## What Changes

- New MCP tool `hera_revive(cwd, role_name, [orchestrator])`, coordinator-only: resolves `role_name` within the caller's orchestrator, and, based on its live session state, either restarts a dead session in place, kicks a stuck-but-alive session in place, or reports that no action was needed (busy, blocked on a prompt, a live coordinator, or already mid-restart) — mirroring the TUI Enter-key path's outcomes exactly.
- New shared primitive `internal/hera.ReviveRole` (mirrors the existing `RecycleCoord` architecture: a pure decision function over narrow `ReviveStore`/`ReviveRunner` interfaces) encodes the gating sequence once, unit-tested without a real PTY/SQLite.
- New daemon-side adapter `daemon.HeraReviveRunner` (mirrors `daemon.HeraRecycleRunner`) wires the primitive to the real `*db.DB` + `agent.SessionRunner`, reusing already-shared building blocks: `agent.BlockedOnPrompt` (ring-based, daemon-correct), `db.ReviveHeraWorkerToInProgress` (the existing single shared in_review→in_progress restore helper), and `agent.SessionRunner.KickRerender` / `StartOrReattach` / `agent.RefreshResumeSessionID`.
- The TUI's existing `Enter`-key revive path (`internal/tui/heraactions.go`) is left untouched — see `design.md` for why this is a deliberate, documented choice rather than silent duplication.

## Capabilities

### New Capabilities

(none — this adds one tool + one internal primitive to the existing `hera-coordination` capability)

### Modified Capabilities

- `hera-coordination`: registers the fifteenth native `hera_*` MCP tool (`hera_revive`); the "Worker revive restores in_progress" requirement gains a third caller of the shared `ReviveHeraWorkerToInProgress` helper.

## Impact

- `internal/hera/revive.go` (new), `internal/hera/revive_test.go` (new)
- `internal/daemon/revive.go` (new), `internal/daemon/revive_test.go` (new)
- `internal/mcp/server.go` (`HeraReviveInput`/`HeraReviver` types, `heraRevive` field)
- `internal/mcp/hera.go` (`hera_revive` tool schema, `toolHeraRevive` handler, `SetHeraReviver`, dispatch case)
- `internal/daemon/daemon.go` (`heraReviveRole` adapter method, `SetHeraReviver` wiring)
- `.claude/skills/hera/SKILL.md`, `README.md` (MCP tools table), `context/knowledge/gotchas/daemon-rpc.md`, `context/knowledge/index.md`
- No schema/data migration, no new dependencies, no REST/API surface change, no TUI behavior change.

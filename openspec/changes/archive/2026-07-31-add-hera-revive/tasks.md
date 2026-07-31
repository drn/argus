**Design doc:** `openspec/changes/add-hera-revive/design.md`

## 1. Tests (write failing first)

- [x] 1.1 `internal/hera/revive_test.go`: fake `ReviveStore`/`ReviveRunner`, cases for every `ReviveOutcome` (`restarted_dead`, `kicked_stuck` incl. the in_progress restore call, `skipped_coordinator_live`, `skipped_busy`, `skipped_blocked_on_prompt`, `skipped_restart_pending`, `skipped_no_session_id`), plus a task-not-found error case.
- [x] 1.2 `internal/daemon/revive_test.go`: `daemon.HeraReviveRunner` against a real `db.OpenInMemory()` + `agent.NewRunner(nil)` (per `context/knowledge/testing.md`), proving `KickRerender` preserves the live session's existing PTY size and `RestartDead` mirrors `handleRestartTask`'s resume-via-session-id behavior.
- [x] 1.3 `internal/mcp/hera_revive_test.go` (new, mirrors `hera_rebind_test.go`'s shape): coordinator-only rejection, unknown role name, own-role rejection, no-live-binding rejection, and a success case asserting the wired `HeraReviver` func is called with the resolved task id + role kind and the outcome renders in the tool response.
- [x] 1.4 Confirm every `it should X` acceptance criterion in `design.md` has a corresponding failing test before moving to implementation.

## 2. `internal/hera.ReviveRole` (the shared primitive)

**Depends on:** Stage 1

- [x] 2.1 `internal/hera/revive.go`: `ReviveOutcome` type + constants, `ReviveStore`/`ReviveRunner` interfaces, `ReviveRole(store, runner, taskID string, isCoordinator bool) (ReviveOutcome, error)` implementing the gate from design.md D3 (alive? → coordinator-live? → has session id? → restart pending? → idle? → blocked on prompt? → kick, best-effort restore-to-in_progress).
- [x] 2.2 Run `internal/hera` tests; confirm Stage 1.1 passes.

## 3. `daemon.HeraReviveRunner` (the daemon-side adapter)

**Depends on:** Stage 2

- [x] 3.1 `internal/daemon/revive.go`: `HeraReviveRunner` struct (`*db.DB` + `agent.SessionRunner` + `cfgFn`), `NewHeraReviveRunner`, and the six `hera.ReviveRunner` methods — `IsAlive`/`IsIdle` via `runner.Get(taskID)`, `BlockedOnPrompt` via `agent.BlockedOnPrompt` (idle-gated), `HasPendingRestart` passthrough, `KickRerender` at the session's current `PTYSize()`, `RestartDead` mirroring `internal/api/handlers.go`'s `handleRestartTask` (`agent.RefreshResumeSessionID` + `StartOrReattach` + status flip).
- [x] 3.2 Run `internal/daemon` tests; confirm Stage 1.2 passes.

## 4. Wire the `hera_revive` MCP tool

**Depends on:** Stage 3

- [x] 4.1 `internal/mcp/server.go`: `HeraReviveInput{TaskID string; IsCoordinator bool}` and `HeraReviver func(HeraReviveInput) (string, error)` types (mirrors `HeraSpawnInput`/`HeraSpawner`); `heraRevive HeraReviver` field on `Server`.
- [x] 4.2 `internal/mcp/hera.go`: `hera_revive` entry in `heraToolDefs` (`cwd`, `role_name` required; `orchestrator` optional); `SetHeraReviver(reviver HeraReviver)` setter (a new independent setter, not folded into `SetHeraService`, matching the existing multi-setter pattern); `toolHeraRevive` handler — coordinator-only guard, `resolveOrchRole` for `role_name`, reject targeting the caller's own role, `HeraLiveBindingByRole` → `ErrHeraNotFound` translated to a clear error, call `s.heraRevive`, render the outcome with a human-readable message per outcome.
- [x] 4.3 `internal/mcp/server.go`'s tool dispatch switch: `case "hera_revive": return s.toolHeraRevive(req.ID, params.Arguments)`.
- [x] 4.4 Run `internal/mcp` tests; confirm Stage 1.3 passes.

## 5. Wire the daemon

**Depends on:** Stage 4

- [x] 5.1 `internal/daemon/daemon.go`: `heraReviveRole(in mcp.HeraReviveInput) (string, error)` method constructing `NewHeraReviveRunner(d.db, d.runner, d.cfgFn)` and calling `hera.ReviveRole(d.db, rr, in.TaskID, in.IsCoordinator)`.
- [x] 5.2 Wire `mcpSrv.SetHeraReviver(d.heraReviveRole)` alongside the existing `SetHeraService` call, inside the same `cfg.Hera.Enabled` gate.
- [x] 5.3 Run `internal/daemon` tests.

## 6. Docs

**Depends on:** Stage 5

- [x] 6.1 `.claude/skills/hera/SKILL.md`: add `hera_revive` to the §3 coordination-tools list (bootstrap/messaging/status section) with its pull-only, idle+not-blocked-gated semantics and when to reach for it (a role looks stuck after a supervisor restart, or `hera_status`/`hera_tree_updates` show no progress); add a one-line decision-rule pointer in §4 if it fits naturally alongside the existing "got a doorbell?" style bullets.
- [x] 6.2 `README.md`: add a `hera_revive` row to the Hera MCP tools table (Reference appendix, § MCP Tools).
- [x] 6.3 Add a gotcha bullet to `context/knowledge/gotchas/daemon-rpc.md` (same coverage cell as the existing `ReviveHeraWorkerToInProgress`/BUG-B entry) noting `hera_revive` is the third caller of the shared restore helper, and that the TUI's Enter-key gate is intentionally NOT unified with it (see design.md D3) — every individual check stays single-sourced, only the ordering is expressed twice.
- [x] 6.4 Update `context/knowledge/index.md`'s coverage-bullet cell for `gotchas/daemon-rpc.md` to reflect the new bullet.

## 7. Archive

**Depends on:** Stage 6

- [x] 7.1 Run `make pre-pr`; fix any failures.
- [x] 7.2 `openspec archive add-hera-revive` (or the manual merge-and-move fallback): merge the `hera-coordination` delta spec into `openspec/specs/hera-coordination/spec.md`, move the change folder to `openspec/changes/archive/2026-07-30-add-hera-revive/`, commit on the same branch before merge.

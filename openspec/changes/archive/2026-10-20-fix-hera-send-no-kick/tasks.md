**Design doc:** `openspec/changes/fix-hera-send-no-kick/design.md`

## 1. Tests (write failing first)

- [x] 1.1 `internal/hera/revive_test.go`: `ReviveDeadRole` restarts a dead session (`restarted_dead`); returns `skipped_alive` for a live idle/unblocked session WITHOUT calling `KickRerender`; propagates a restart error; errors on an unknown task.
- [x] 1.2 `internal/mcp/hera_send_revive_test.go`: the send-path reviver is called with `DeadOnly: true`; a `skipped_alive` outcome renders a `- **revive**:` line and the send still succeeds.
- [x] 1.3 `internal/mcp/hera_revive_test.go`: explicit `hera_revive` calls the reviver with `DeadOnly: false`.
- [x] 1.4 `internal/daemon/revive_test.go`: `reviveForInput` with `DeadOnly` leaves a live fixture session running and reports `skipped_alive`.

## 2. Implement

**Depends on:** Stage 1

- [x] 2.1 `internal/hera/revive.go`: add `ReviveSkippedAlive` + `ReviveDeadRole`, sharing the task-load/dead-restart helper with `ReviveRole`.
- [x] 2.2 `internal/mcp/server.go`: add `HeraReviveInput.DeadOnly`; `internal/mcp/hera.go`: send path sets it, add the `skipped_alive` message, and update the `hera_revive` tool description.
- [x] 2.3 `internal/daemon/revive.go` + `daemon.go`: `reviveForInput` dispatches on `DeadOnly`.

## 3. Docs, skill, and land

**Depends on:** Stage 2

- [x] 3.1 `internal/skills/builtin/hera/SKILL.md`: send/revive text, §4 "safe no-op" correction, worker verification scope, long-job survival, coordinator check-progress-don't-ping.
- [x] 3.2 `context/knowledge/gotchas/messaging.md` + `index.md`: dead-only send-revive gotcha + background-job-guard follow-up note.
- [x] 3.3 Archive the change (merge delta into `openspec/specs/hera-messaging/spec.md`, move folder) on this branch.
- [x] 3.4 `make pre-pr` green; push; open PR; CI green.

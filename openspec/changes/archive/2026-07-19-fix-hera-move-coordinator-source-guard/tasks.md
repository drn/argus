## 1. Guard hera_move against moving a live coordinator's own binding

- [x] 1.1 `toolHeraMove` (`internal/mcp/hera.go`) rejects the call, ending and
  creating nothing, when `resolveCallerRole`'s resolved role is
  `db.HeraKindCoordinator` — before any mutation (`MoveHeraBinding` is never
  reached). Error names the caller's role + orchestrator and directs the
  caller to the Hera TUI's `J` adopt/reparent key.

## 2. Tests

- [x] 2.1 `TestHera_Move_SourceCoordinatorRejected` (`internal/mcp/hera_test.go`):
  a live coordinator calling `hera_move(kind="freelance")` toward a different
  orchestrator is rejected; its original coordinator binding stays live and
  unchanged; no role is created under the target orchestrator.
- [x] 2.2 Existing `TestHera_Move_*` suite still passes unmodified (worker/
  freelance source moves, ambiguous multi-binding self-promotion, destination
  kind=coordinator rejection) — confirms the new guard is scoped to the
  SOURCE role kind only.

## 3. Docs

- [x] 3.1 `.claude/skills/hera/SKILL.md` and
  `internal/skills/builtin/hera/SKILL.md` gain a decision-rule bullet: never
  `hera_move` your own coordinator binding to join another team; ask a human
  to use the Hera TUI's `J` key instead.

## 1. Reorder the status-icon precedence

- [x] 1.1 `RoleStatusIcon` (internal/tui/widget/rolestatusicon.go): move `case in.Active` above `case in.ReadyToClose` / `case in.Failed` / `case in.Done`, so the new order is `needs-input → active → ready_to_close → failed → done → idle → live → default`. Update the function doc + `RoleStatusInputs` field comments with the BUG-F rationale (active is the honest content-derived signal; resting case preserved via IsActive's running/!idle gate).

## 2. Tests (TDD)

- [x] 2.1 Widget precedence (`TestRoleStatusIcon_Precedence`): add the KEY BUG-F case — `ReadyToClose=true` + `Active=true` → SPINNER (was review glyph); add `Active` over `failed`/`done`; split the old `ready_to_close/failed/done over active` cases into resting (`Active=false`) forms that keep the review/✕/✓ glyphs. Add `active beats failed` to `TestRoleStatusIcon_Failed`.
- [x] 2.2 Rail-level render (`TestStatusIcon_ActiveOutranksReadyToClose`): a live+running+not-idle `in_review` worker with `ReadyToClose=true` animates the spinner; the same worker session-idle returns the review glyph (resting case preserved).

## 3. Docs & gates

- [x] 3.1 Document the invariant in context/knowledge/gotchas/hera-view.md (active outranks ready_to_close/done/failed in RoleStatusIcon; keeps the resting case via IsActive's running/!idle gate).
- [x] 3.2 `make pre-pr` green; `openspec validate --all --strict` passes.
- [x] 3.3 Archive this change within the branch (base specs updated atomically).

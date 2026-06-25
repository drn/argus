## 1. Op layer (`internal/tui/hera/adopt.go`)

- [x] 1.1 Add `EndReasonDetached = "detached"` alongside `EndReasonReparented`.
- [x] 1.2 Extract the BUG-026 teardown block out of `ReparentCoordinator` into a
  shared `teardownParentLinks(taskID string, coordRoleID int64, reason string)
  (int, error)` helper: end every live parent-link binding (RoleID != coord
  role) with `reason`, then delete every distinct parent link role (any role
  other than the coord role, reached through any binding of that task) so its
  bindings cascade. Returns the count of distinct parent link roles deleted.
- [x] 1.3 Make `ReparentCoordinator` call `teardownParentLinks(taskID,
  coordRole.ID, EndReasonReparented)` so the teardown stays single-source.
- [x] 1.4 Add `DetachResult` (`ChildOrchestratorName`, `LinksRemoved`) and
  exported `DetachCoordinator(childOrchestratorID int64) (*DetachResult, error)`:
  resolve `C`'s coord role + latest binding's task id (same as re-parent), run
  ONLY `teardownParentLinks(..., EndReasonDetached)`, recreate NO link, log via
  `uxlog.Log("[hera-view] detach: ...")` (success AND the already-top-level
  no-op skip). `LinksRemoved == 0` ⇒ already top-level (idempotent clean no-op).
- [x] 1.5 Genericize `coordRoleOf`'s error wording (drop "to re-parent") since it
  is now shared by detach.

## 2. UI entry point (`internal/tui/heraactions.go`)

- [x] 2.1 In `heraAdoptCoordinator`, prepend a package-level `detachSentinel`
  `*db.HeraOrchestrator` (distinguished by pointer identity, name
  `— Detach (make top-level) —`) to the picker targets; remove the
  `len(targets)==0` bail (the detach row is always present). Title hints detach.
- [x] 2.2 In the picker `pick` callback, when the chosen orchestrator is the
  `detachSentinel`, call `DetachCoordinator(childOrchID)` (statusbar error on
  failure, `heraRefresh()` on success); otherwise re-parent as before.

## 3. Tests (`internal/tui/hera/adopt_test.go`)

- [x] 3.1 `DetachCoordinator` un-nests a nested coordinator: the parent link role
  + its live binding are gone, `C`'s own coord role + coord binding survive, and
  `C` is top-level again (no live parent-link binding remains for `C`'s task).
- [x] 3.2 `DetachCoordinator` on an already-top-level coordinator is an idempotent
  clean no-op (`LinksRemoved == 0`, no error, coord role + binding untouched).
- [x] 3.3 `DetachCoordinator` rejects an unknown orchestrator and a coordinator
  with no coordinator role (shared-resolution guards still fire).
- [x] 3.4 `ReparentCoordinator` still works end-to-end and its BUG-026 teardown
  still holds (existing tests pass against the shared helper — no regression).

## 4. Docs

- [x] 4.1 Add the bridge-binding nesting / detach-as-teardown-without-recreate
  invariant to `context/knowledge/gotchas/hera-view.md` (1–2 sentences).

## 5. Archive

- [x] 5.1 Merge the delta into `openspec/specs/hera-view/spec.md` and move this
  change folder to `openspec/changes/archive/<YYYY-MM-DD>-add-hera-detach-coordinator/`
  (manual merge-and-move; `openspec` CLI may be absent).

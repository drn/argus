**Design doc:** `openspec/changes/add-worker-bounce/design.md`

## 1. Tests (write failing first)

- [ ] 1.1 `internal/mcp/hera_status_recycle_test.go` (or a new adjacent test file): add cases for a worker-kind and a freelance-kind caller successfully setting `handoff_note`/`request_recycle` via `hera_status`, asserting `task_meta` is written and no rejection error occurs. Update/replace the existing "rejected for worker/freelance" test case to assert acceptance instead.
- [ ] 1.2 `internal/hera/recycle_watcher_test.go`: add cases proving `RecycleWatcher.Tick`/`tickTask` drives a worker-kind and a freelance-kind role through `RecycleCoord`/`RecycleSelfService` when `pending_recycle=true` and the session is idle — mirroring the existing coordinator-kind test shape. Keep the existing "coordinator picked among 2+ live bindings" case passing unmodified.
- [ ] 1.3 `internal/hera/recycle_test.go`: add/adjust a case proving `BuildRecycleSeedPrompt`'s opening text does not assert coordinator-specific wording when seeding a worker or freelance role (e.g. assert it no longer contains the literal string "coordinator" in a role-specific way, or assert the new generalized wording directly).
- [ ] 1.4 `internal/tui/heraactions_test.go` and/or `internal/tui/hera/panes_test.go`/`page.go`'s existing key-dispatch tests: add cases proving `B` on a worker/freelance rail selection opens a confirm modal and, on confirm, calls `WriteInputSystem` with the expected instruction text — and that it does NOT call the coordinator's immediate-kill path. Add a case proving coordinator `B` behavior is unchanged (regression).
- [ ] 1.5 Confirm every `it should X` acceptance criterion in `design.md` has a corresponding failing test before moving to implementation.

## 2. Widen hera_status (internal/mcp/hera.go)

**Depends on:** Stage 1

- [ ] 2.1 In `toolHeraStatus`, remove the coordinator-only gate on `handoff_note`/`request_recycle` — accept both from any hera-bound role kind (coordinator, worker, freelance), writing the same `task_meta` keys regardless of kind.
- [ ] 2.2 Update the tool's `Description` string (and the two parameters' descriptions) to drop "Coordinator-only"/"Rejected for worker/freelance callers" wording.
- [ ] 2.3 Run `internal/mcp` tests; confirm Stage 1.1 passes and no existing coordinator-path test regresses.

## 3. Widen RecycleWatcher (internal/hera/recycle_watcher.go)

**Depends on:** Stage 1

- [ ] 3.1 In `RecycleWatcher.tickTask`, remove the `role.Kind == db.HeraKindCoordinator` filter on the resolved binding — pick the first live binding for the task, keeping the existing "prefer the coordinator-kind binding when 2+ live bindings exist" disambiguation for tasks that do have one.
- [ ] 3.2 Update the function's doc comment (currently states "pending_recycle is coordinator-only (hera_status rejects it otherwise)") to reflect the new scope.
- [ ] 3.3 Run `internal/hera` tests; confirm Stage 1.2 passes and existing coordinator-path tests (including the 2+-binding disambiguation case) still pass.

## 4. Generalize the recycle seed prompt (internal/hera/recycle.go)

**Depends on:** Stage 1

- [ ] 4.1 Update `BuildRecycleSeedPrompt`'s opening prose (currently "You are a fresh session recycled from a prior coordinator session on this same task...") to not assume a coordinator role.
- [ ] 4.2 Update the function's doc comment and any other coordinator-specific wording in this file (e.g. `RecycleTrigger`'s doc comments referencing "a coordinator") to describe a hera role generically.
- [ ] 4.3 Run `internal/hera` tests; confirm Stage 1.3 passes.

## 5. Wire the rail `B` key for worker/freelance selections

**Depends on:** Stage 2, Stage 3

- [ ] 5.1 In `internal/tui/hera/page.go`, widen the `'B'` key handler's guard from `!sel.IsCoordinator()` to also accept worker/freelance selections, routing to a new callback (e.g. `OnBounceWorker`) distinct from `OnForceRecycle` when the selection is not a coordinator.
- [ ] 5.2 In `internal/tui/heraactions.go`, add a new bounce action (e.g. `heraOpenBounceWorker`/`heraDoBounceWorker`) mirroring `heraOpenForceRecycle`/`heraDoForceRecycle`'s shape: confirm modal with worker/freelance-appropriate copy ("Bounce `<name>`? Asks it to hand off its current state, then restarts fresh once it does."), then on confirm, resolve the role's live session and call `WriteInputSystem` with an instruction telling it to call `hera_status(handoff_note=..., request_recycle=true)` summarizing what's done/current state/what's next. No daemon-side kill/restart call from this path — the existing self-service pipeline (Stages 2-3) completes it asynchronously.
- [ ] 5.3 Confirm `heraOpenForceRecycle`/`heraDoForceRecycle` (the coordinator path) are untouched.
- [ ] 5.4 Run `internal/tui` tests; confirm Stage 1.4 passes.

## 6. Docs

**Depends on:** Stage 5

- [ ] 6.1 Update `README.md`'s Reference keybinding table entry for `B` (currently states "No-op on a non-coordinator selection") to describe both behaviors.
- [ ] 6.2 Add a gotcha bullet to `context/knowledge/gotchas/hera-view.md` (or `orchestration.md`, whichever fits the existing entry shape better) documenting: `B` now bounces worker/freelance roles via a self-service instruct-and-wait path (no direct kill), reusing `RecycleSelfService` with no new trigger type; no automated nudge/timeout exists for this path (explicit scope decision).
- [ ] 6.3 Update `context/knowledge/index.md`'s coverage-bullet cell for the relevant gotcha file(s) touched in 6.2.

## 7. Archive

**Depends on:** Stage 6

- [ ] 7.1 Run `make pre-pr`; fix any failures.
- [ ] 7.2 `openspec archive add-worker-bounce` (or the manual merge-and-move fallback): merge the three delta specs into their base specs under `openspec/specs/`, move the change folder to `openspec/changes/archive/<date>-add-worker-bounce/`, commit on the same branch before merge.

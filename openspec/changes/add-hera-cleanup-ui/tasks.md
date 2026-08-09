**Design doc:** `openspec/changes/add-hera-cleanup-ui/design.md`
**Depends on:** `add-merge-safety-classifier` landing first. Independent of `add-nuke-merge-warning`.
**Blocked on sign-off:** the "mark complete = status flip only" recommendation in design.md's Open Questions must be confirmed (or redirected) before Stage 2 begins.

## 1. Tests

- [ ] 1.1 `internal/db` (or wherever `task_meta` helpers live) test: a stuck-task-predicate query returns exactly the tasks matching `archived=1, status=in_review, no live hera binding`, across all projects.
- [ ] 1.2 `internal/daemon` test: `POST /api/maintenance/cleanup-candidates/compute` starts a background pass and returns immediately.
- [ ] 1.3 `internal/daemon` test: a second compute call while one is in flight is a no-op (no duplicate pass).
- [ ] 1.4 `internal/daemon` test: `GET /api/maintenance/cleanup-candidates` returns cached verdicts + `computing` flag correctly in both the computing and idle states.
- [ ] 1.5 `internal/daemon` test: a confirmed-safe cached verdict is not re-classified on a subsequent compute pass; a needs-review verdict is.
- [ ] 1.6 `internal/daemon` test: repo resolution for a candidate uses the `projects` table's configured path, keyed by the task's project; a task whose project row no longer exists classifies as needs-review with an "unresolvable repo/project" reason.
- [ ] 1.7 `internal/api` test: the apply endpoint rejects a device/scope token with 403; accepts the master token.
- [ ] 1.8 `internal/api` test: `scope: "safe"` only advances confirmed-safe tasks; `scope: "all"` advances every cached candidate.
- [ ] 1.9 `internal/api` test: apply re-verifies the stuck-task predicate per task at apply time and skips (does not error on) a task that no longer qualifies.
- [ ] 1.10 `internal/api` test: apply acts on the last-computed cached snapshot, not a fresh live classification (assert via a test seam that no new classification call happens during apply).
- [ ] 1.11 `internal/tui` test (SimulationScreen smoke): the Cleanup popup opens via the command-palette action, renders sectioned Safe/Needs-Review rows, and both bulk actions are reachable and dispatch the expected apply call.
- [ ] 1.12 Confirm every scenario in `specs/hera-view/spec.md` and `specs/rest-api/spec.md` for this change has a corresponding failing test before implementation (Prove-It Pattern).

## 2. Implementation

**Depends on:** Stage 1, and the sign-off gate above

- [ ] 2.1 Add the stuck-task-predicate query (reusable by both the daemon computation and any future caller) — a straightforward SQL query mirroring the one already used to derive the 737-task audit.
- [ ] 2.2 Add a `task_meta` namespace (e.g. `cleanup`) caching each candidate's last-computed tier/verdict/reason/timestamp, plus daemon-side state tracking whether a compute pass is currently in flight.
- [ ] 2.3 Implement the background compute pass: resolve each eligible task's repo directory via its project's configured `path` (skip/mark-unresolvable if the project row is gone), group by repo, and call `internal/mergesafety`'s batch entry point — confirmed-safe results cached as terminal, needs-review results cached but re-checked on the next pass.
- [ ] 2.4 Add `POST /api/maintenance/cleanup-candidates/compute`, `GET /api/maintenance/cleanup-candidates`, and `POST /api/maintenance/cleanup-candidates/apply` to `internal/api` (routes.go + handlers), with `apply` gated by `requireMaster`.
- [ ] 2.5 Build the Cleanup popup TUI widget, modeled on `TaskSwitcherModal`'s grouped/sectioned rendering (header rows + scrollable item rows), wired into `internal/tui/app.go` (new mode constant, open/handle/close trio via `a.pages`) and reachable from the Ctrl+K command-palette registry.
- [ ] 2.6 Wire the popup's two bulk actions to the apply endpoint with the appropriate `scope`.
- [ ] 2.7 Run `make test-pkg` for each touched package and confirm all Stage 1 tests pass.

## 3. Verification

**Depends on:** Stage 2

- [ ] 3.1 Run `make pre-pr` and confirm it passes clean.
- [ ] 3.2 `openspec validate add-hera-cleanup-ui --strict` passes.
- [ ] 3.3 Archive this change into `openspec/specs/hera-view/spec.md` and `openspec/specs/rest-api/spec.md` in the same PR, before merge.
- [ ] 3.4 Add a gotcha note to `context/knowledge/gotchas/hera-view.md` (or `misc.md`) documenting the cleanup popup's classify-then-cache-then-apply flow, the master-only apply gate, and the explicit TUI-only Frontend Parity gap for this stage.
- [ ] 3.5 Confirm the README's Reference appendix (REST endpoints table) is updated for the new `/api/maintenance/cleanup-candidates*` endpoints.

**Design doc:** `openspec/changes/add-merge-safety-review/design.md`
**Depends on:** `add-merge-safety-classifier` landing first.

## 1. Tests — worktree-management (PruneTasks generalization)

- [x] 1.1 `internal/db` test: `PruneTasks(ids)` deletes exactly the given IDs (minus any failing the live-binding guard), regardless of their `status`.
- [x] 1.2 `internal/db` test: `PruneTasks` re-verifies the live-Hera-binding guard at call time — an ID that gained a live binding after the caller's own check is skipped, not deleted.
- [x] 1.3 `internal/db` test: `PruneCompleted()` (the existing all-`status=complete` sweep) is behaviorally unchanged — same guard, same skipped-count semantics — now expressed as a thin wrapper over `PruneTasks`.
- [x] 1.4 `internal/agent` test: `PrunePrepare` with `opts.TaskIDs` set sources its task list from the explicit set, not the all-complete query; `PrunePlan.Run`'s slow phase is unchanged either way.
- [x] 1.5 `internal/agent` test: existing `Ctrl+R` (`opts.TaskIDs` unset) test coverage still passes unmodified, proving zero regression.

## 2. Implementation — worktree-management

**Depends on:** Stage 1

- [x] 2.1 Add `DB.PruneTasks(ids []string) (pruned []*model.Task, skipped int, err error)` to `internal/db/tasks.go`, re-expressing `PruneCompleted()` in terms of it.
- [x] 2.2 Add `TaskIDs []string` to `agent.PruneOptions`; branch `PrunePrepare` to call `database.PruneTasks(opts.TaskIDs)` when set, else the existing `database.PruneCompleted()`.
- [x] 2.3 Run `make test-pkg PKG=./internal/db/` and `make test-pkg PKG=./internal/agent/`, confirm Stage 1 passes and no existing test regresses.

## 3. Tests — merge-safety review popup + entry points

- [x] 3.1 `internal/tui` test: single-role nuke opens the review popup (not a plain `ConfirmModal`) with the one task as its candidate.
- [x] 3.2 `internal/tui` test: popup renders NOT-SAFE before SAFE; `Clean safe` is the default-selected action.
- [x] 3.3 `internal/tui` test: `Clean safe` at n=1 with a SAFE candidate proceeds with the nuke; with a NOT-SAFE candidate, it's a no-op (nuke does not proceed).
- [x] 3.4 `internal/tui` test: `Clean all` at n=1 proceeds with the nuke regardless of the candidate's verdict.
- [x] 3.5 `internal/tui` test: `Cancel` performs no nuke.
- [x] 3.6 `internal/tui` test: cascade nuke and clear-archived (`C`) do NOT open the popup — they retain their existing confirm, now augmented with a confirmed/not-confirmed count (Tier A, off the UI thread, computed before the confirm opens).
- [x] 3.7 `internal/tui` test: no `gh`/network call is made from any nuke entry point (single-role or cascade) — assert via the classifier's test seam recording zero Tier B invocations.
- [x] 3.8 `internal/api` test: `POST /api/maintenance/cleanup-candidates/compute` starts a background pass, is idempotent while running. (Lives in `internal/api`, not `internal/daemon` — the computation is a `*Server`-owned method, consistent with how `handlePruneCompleted` already works; there is no ticker/poller, so `internal/daemon` needed no changes.)
- [x] 3.9 `internal/api` test: `GET /api/maintenance/cleanup-candidates` returns cached verdicts + `computing` flag.
- [x] 3.10 `internal/api` test: repo resolution for a global-Cleanup candidate uses the `projects` table's configured path, keyed by the task's project; a task whose project row no longer exists classifies as not-safe with an "unresolvable repo/project" reason.
- [x] 3.11 `internal/api` test: the clean endpoint rejects device/scope tokens with 403; accepts master.
- [x] 3.12 `internal/api` test: `scope: "safe"` deletes only confirmed-safe tasks via `PruneTasks`; `scope: "all"` deletes every cached candidate.
- [x] 3.13 `internal/api` test: clean re-verifies the stuck-task predicate and live-binding guard per task and skips (does not error on) one that no longer qualifies.
- [x] 3.14 `internal/api` test: clean acts on the last-computed cached snapshot, not a fresh live classification.
- [x] 3.15 `internal/tui` test (SimulationScreen smoke): the global Cleanup action opens via the Ctrl+K palette, renders sectioned rows, and both Clean actions dispatch the expected `clean` call with the right scope.
- [x] 3.16 Confirm every scenario in `specs/hera-view/spec.md`, `specs/rest-api/spec.md`, and `specs/worktree-management/spec.md` for this change has a corresponding failing test before implementation (Prove-It Pattern).

## 4. Implementation — popup widget + entry points

**Depends on:** Stage 3

- [x] 4.1 Build the merge-safety review popup widget (`internal/tui/mergesafetypopup.go`), modeled on `TaskSwitcherModal`'s grouped/sectioned rendering, with the NOT-SAFE/SAFE section order and the three-action bar (`Clean safe` default-selected, `Clean all`, `Cancel`).
- [x] 4.2 Wire single-role nuke (`internal/tui/heraactions.go` + `internal/tui/mergesafety.go`): async Tier A classify → `QueueUpdateDraw` → open the popup with the one-candidate list; Clean actions call the existing `heraNukeRole` mechanics (unchanged); staleness guard compares the role's ID against the live rail selection when the async check completes.
- [x] 4.3 Extend `heraCascadeNukeFrom` and `heraClearArchive` to run Tier A checks concurrently (bounded pool, `maxClassifyWorkers=8`) across their subtree's reclaimed tasks and fold a confirmed/not-confirmed count into their existing confirm message — no popup, no mechanics change; skipped entirely when nothing needs reclaiming.
- [x] 4.4 Implement the daemon-side cleanup-candidate computation (`internal/api/cleanup_candidates.go`): `DB.StuckTaskCandidates()` predicate query, `task_meta` namespace `"cleanup"` caching (`safe`/`tier`/`reason`, safe=true terminal), repo resolution via `cfg.Projects[task.Project]` (empty when the project no longer exists — `mergesafety.ClassifyBatch`'s own fail-closed handling covers it, no special-casing needed), grouped Tier A+B classification via `mergesafety.ClassifyBatch`, and single-flight in-progress tracking (`cleanupComputeState`, mutex+bool).
- [x] 4.5 Add the 3 REST endpoints (`internal/api/routes.go` + `internal/api/cleanup_candidates.go`), `clean` gated by `requireMaster`, implemented via `agent.PrunePrepare(s.db, agent.PruneOptions{TaskIDs: ...})` + `.Run(nil)` — **caught during implementation**: `PrunePrepare` treats an *empty* `TaskIDs` slice as "unset" and silently falls back to its default all-`status=complete` sweep; `handleCleanupCandidatesClean` explicitly short-circuits with `cleaned:0` before ever calling `PrunePrepare` when the requested scope matches zero tasks, with a regression test proving an unrelated complete task survives.
- [x] 4.6 Wire the global Cleanup action: a new keybinding `c` in `CtxHeraRail` (`internal/tui/keymap/actions.go` — verified free in that context before adding; the palette (`rowsForContext` in `commandpalette_actions.go`) only lists actions with a resolvable `keymap.BindingFor`, so a palette-only entry with no underlying key isn't supported by the existing infra — a real keybinding was added alongside the palette registration, superseding the original "palette-only" framing in design.md as an implementation-detail correction, not a behavior change). `HeraPage.OnCleanup` callback opens the popup in a scanning state, `pollCleanupCandidates` drives compute→poll-list (700ms interval) until ready, `heraDoGlobalClean` posts the chosen scope. A small `localMaintenanceClient` (plain `net/http`, no new package dependency) handles the REST calls, confined to `internal/tui` per this repo's boundary between TUI and daemon-side `gh`/network code. keymap + help modal + README keybinding table all updated in the same commit per this repo's mandatory rule.
- [x] 4.7 Run `make test-pkg` for each touched package and confirm all Stage 3 tests pass.

**Integration note**: Stages 3/4 for the two halves (daemon/REST vs. TUI) were implemented in parallel by two independent efforts against the already-written spec deltas as their shared contract. One mismatch surfaced at integration: the TUI side had assumed the candidate JSON field was `"id"`; the daemon side actually serializes `"task_id"`. Fixed by aligning the TUI struct tag to the daemon's actual wire format (the already-tested, already-committed side), re-verified full `internal/tui`+`internal/db`+`internal/api` green after the fix.

## 5. Verification

**Depends on:** Stage 4

- [x] 5.1 Run `make pre-pr` and confirm it passes clean. build/vet/fmt-check/lint-pr (0 issues) all clean. `vuln` fails only on the same documented pre-existing Go-toolchain-only stdlib CVEs as the classifier change (GO-2026-5856/5039/5037). `test-cover-gate`'s full-suite run hit the documented `internal/tui/terminal` `-race` resource-contention flake (zero diff to that package across this entire change; passes cleanly in isolation, ~78-90s) — confirmed via BOTH isolation and a fully-serialized `go test -race -count=1 -p 1 ./...` run, which completed with zero failures and produced a complete coverage profile: `go run ./scripts/coverfilter -min 88` reports **88.7%** (29194/32916), clearing the floor.
- [x] 5.2 `openspec validate add-merge-safety-review --strict` passes.
- [x] 5.3 Archive this change into `openspec/specs/hera-view/spec.md`, `openspec/specs/rest-api/spec.md`, and `openspec/specs/worktree-management/spec.md` in the same PR, before merge.
- [x] 5.4 Add a gotcha note to `context/knowledge/gotchas/hera-view.md` documenting: the popup's two entry points and their differing Tier A/A+B scope, the cascade/clear-archived boundary (no popup, count-only), the `maintenanceClientFactory` test seam and a real `-race` bug it caught in the poll-interval test harness, and the popup's own "pure display, no scope-filtering logic" contract.
- [x] 5.5 Confirm the README's Reference appendix (REST endpoints table, keybindings table) is updated for the new `/api/maintenance/cleanup-candidates*` endpoints and the `c` Ctrl+K/CtxHeraRail keybinding.

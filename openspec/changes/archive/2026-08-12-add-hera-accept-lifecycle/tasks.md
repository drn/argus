**Design doc:** `openspec/changes/add-hera-accept-lifecycle/design.md`

## 1. Shared accept primitive

**Depends on:** none

- [x] 1.1 `internal/hera/accept_test.go`: `AcceptRole` flips `in_progress`→`complete` and sends the acceptance message from `fromRoleID` to `roleID`.
- [x] 1.2 `internal/hera/accept_test.go`: `AcceptRole` flips `in_review`→`complete` identically.
- [x] 1.3 `internal/hera/accept_test.go`: `AcceptRole` on an already-`complete` task is a full no-op – no status write, no message sent, no error.
- [x] 1.4 `internal/hera/accept_test.go`: a custom `message` is appended to the acceptance body; an empty `message` sends the default body only.
- [x] 1.5 `internal/hera/accept.go`: implement `AcceptStore`, `AcceptSender`, and `AcceptRole(store, sender, fromRoleID, roleID int64, message string) (bool, error)` per design.md's shared-primitive decision.
- [x] 1.6 Run `make test-pkg PKG=./internal/hera/` and confirm Stage 1 tests pass.

## 2. `hera_accept` MCP tool

**Depends on:** Stage 1

- [x] 2.1 `internal/mcp/hera_accept_test.go`: coordinator accepts a worker role – task flips to `complete`, response reports success. Mirrors `hera_revive_test.go`'s real-in-memory-DB style (`testHeraServer`).
- [x] 2.2 `internal/mcp/hera_accept_test.go`: non-coordinator caller is rejected (mirrors `TestHeraRevive_NonCoordinatorRejected`).
- [x] 2.3 `internal/mcp/hera_accept_test.go`: unknown `role_name` is rejected (mirrors `TestHeraRevive_UnknownRoleRejected`).
- [x] 2.4 `internal/mcp/hera_accept_test.go`: targeting the caller's own role is rejected (mirrors `TestHeraRevive_OwnRoleRejected`).
- [x] 2.5 `internal/mcp/hera_accept_test.go`: accepting an already-complete task's role returns success with a no-op note (never an error).
- [x] 2.6 `internal/mcp/hera_accept_test.go`: accepting from `in_review` (the ordinary worker-done state) also flips to `complete`.
- [x] 2.7 `internal/mcp/hera_accept_test.go`: an optional `message` is included in the sent acceptance message (assert via `d.HeraInbox`/`HeraMessagesByIDs` on the target role).
- [x] 2.8 `internal/mcp/hera_test.go` tool-list assertion: `hera_accept` is present when hera is enabled and absent when hera is off (extend the existing 18/19-tool-count assertion).
- [x] 2.9 `internal/mcp/hera.go`: add `hera_accept` to `heraToolDefs`; extend `HeraStore` with `Get`/`SetStatus`; implement `toolHeraAccept` (resolve caller, require coordinator kind, resolve target via `resolveOrchRole`, reject self-target, call `hera.AcceptRole`, render the no-op-vs-flipped response).
- [x] 2.10 `internal/mcp/server.go`: dispatch `case "hera_accept"`.
- [x] 2.11 Run `make test-pkg PKG=./internal/mcp/` and confirm Stage 2 tests pass.

## 3. Gater auto-accept on materialize

**Depends on:** Stage 1

- [x] 3.1 `internal/heragater/heragater_test.go`: materializing a node with a single blocker fires the accept-equivalent for that blocker exactly once.
- [x] 3.2 `internal/heragater/heragater_test.go`: materializing a fan-in node (2+ blockers) fires the accept-equivalent for EVERY blocker, exactly once each.
- [x] 3.3 `internal/heragater/heragater_test.go`: a blocker whose task is already `complete` before this materialize (e.g. a prior sibling dependent already accepted it) is a no-op through the accept seam – no error, no double-fire artifact.
- [x] 3.4 `internal/heragater/heragater_test.go`: an accept-equivalent failure for one blocker is logged and does NOT fail materialization or affect the other blockers' accept calls.
- [x] 3.5 `internal/heragater/heragater_test.go`: a root node (no blockers) materializing fires no accept calls (regression guard, mirrors the existing fan-in no-op guards).
- [x] 3.6 `internal/heragater/heragater_test.go`: no-coordinator-to-accept-as case does not panic (mirrors `TestGater_FanInNoCoordinatorNoPanic`/`TestGater_HoldNoCoordinatorNoPanic`).
- [x] 3.7 `internal/heragater/heragater.go`: add `Accepter func(coordRoleID, blockerRoleID int64) error`, `SetAccepter`, and `acceptBlockers(node, blockerIDs)` called after a successful worker-kind `materialize` (scoped like `pingFanIn` – not the subcoord path, per design.md).
- [x] 3.8 `internal/daemon/daemon.go`: wire `d.heraGater.SetAccepter(...)` to `hera.AcceptRole` against `d.db` and the existing `gaterSvc`.
- [x] 3.9 Run `make test-pkg PKG=./internal/heragater/` and confirm Stage 3 tests pass.

## 4. Revive from `complete`

**Depends on:** none (independent of Stages 1-3)

- [x] 4.1 `internal/db/hera_test.go`: `ReviveHeraWorkerToInProgress` restores a worker task from `complete` back to `in_progress`, identically to the existing `in_review` case.
- [x] 4.2 `internal/db/hera_test.go`: still refuses from `pending`/`in_progress` (regression guard on the existing behavior).
- [x] 4.3 `internal/db/hera_test.go`: a `complete` task carrying `ready_to_close` or a terminal role-status still refuses (the `heraWorkerAwaitingCloseout` guard re-evaluated for the `complete` source, per design.md).
- [x] 4.4 `internal/db/hera.go`: widen `ReviveHeraWorkerToInProgress`'s source-status check to accept `model.StatusInReview || model.StatusComplete`; confirm `heraWorkerAwaitingCloseout` needs no change (it already reads meta/role-status, not the task's status column).
- [x] 4.5 Run `make test-pkg PKG=./internal/db/` and confirm Stage 4 tests pass.

## 5. `heraHide` stops the session on hide only

**Depends on:** none (independent of Stages 1-4)

- [x] 5.1 `internal/tui/hera/ops_test.go`: `ArchiveToggle` returns `(true, nil)` on the archive (hide) direction and `(false, nil)` on the unarchive (un-hide) direction, for both a role and an orchestrator selection. Update the 3 existing `ArchiveToggle` call sites (`TestOps_ArchiveToggle_Role`, `TestOps_ArchiveToggle_Orchestrator`, the `errNoTarget` case) for the new two-value signature.
- [x] 5.2 `internal/tui/heraactions_test.go` (or the existing hera-actions test file): `heraHide` on a live worker stops its session (backgrounded) when hiding, leaves the worktree/branch/argus-task-archived-flag untouched.
- [x] 5.3 `internal/tui/heraactions_test.go`: `heraHide` on the un-hide direction (already-archived role) does NOT stop any session.
- [x] 5.4 `internal/tui/heraactions_test.go`: `heraHide` when no live session exists for the role is a clean no-op on the stop path (archive still succeeds).
- [x] 5.5 `internal/tui/hera/ops.go`: change `ArchiveToggle`'s signature to `(archived bool, err error)`.
- [x] 5.6 `internal/tui/heraactions.go`: `heraHide` reads the returned direction; on `archived == true` with a live session (`a.runner.HasSession`), stops it via `heraGoSafe` (mirrors `heraReclaimAndArchiveTask`'s existing stop pattern) – never on un-hide, never touching the worktree/branch.
- [x] 5.7 Update every other `ArchiveToggle` call site for the new signature (`internal/tui/hera/ops_test.go` non-Stage-1 hits, any command-palette/App call sites – verify via `go build ./...`).
- [x] 5.8 Run `make test-pkg PKG=./internal/tui/...` and confirm Stage 5 tests pass.

## 6. PR-merge nudges the task's Hera coordinator

**Depends on:** none (independent of Stages 1-5)

- [x] 6.1 `internal/daemon/pr_poll_test.go`: a task holding a live/ended Hera worker role whose PR transitions to `merged` sends its resolved coordinator role a nudge message (assert via `d.HeraInbox`/`HeraMessagesByIDs`).
- [x] 6.2 `internal/daemon/pr_poll_test.go`: a task with NO Hera role ever fires no nudge and no error.
- [x] 6.3 `internal/daemon/pr_poll_test.go`: a task whose resolved role IS the coordinator itself (its own PR merged) fires no self-send nudge.
- [x] 6.4 `internal/daemon/pr_poll_test.go`: polling the SAME already-`merged` task again (a later cycle) never re-fires – relies on the existing terminal-state skip, confirm it holds for this new path too.
- [x] 6.5 `internal/daemon/pr_poll_test.go`: the nudge never flips the task's status or the role's hera role-status – status-change assertions on top of the existing `TestPollPR_*` coverage.
- [x] 6.6 `internal/daemon/daemon.go`: in `pollPRStatesOnce`'s write loop, on `res.Merged` (a new `gitutil.PRResult` field – `State` alone collapses MERGED/CLOSED into one `merged-closed` value, so the raw distinction is threaded through separately, see `internal/gitutil/pr_batch.go`), resolve the task's most recent Hera role (`ListHeraBindingsByTask` → `HeraRole`) and its orchestrator's coordinator (`ListHeraRolesByKind`), skip silently if either is absent or the coordinator IS the resolved role, else send a nudge via a `hera.New(d.db, notifier)`-constructed service (nil-notifier-safe; soft-fail, logged, never blocks the poll).
- [x] 6.7 Run `make test-pkg PKG=./internal/daemon/` and confirm Stage 6 tests pass.

## 7. Verification

**Depends on:** Stages 1-6

- [x] 7.1 Run `make pre-pr` and confirm it passes clean (build, vet, fmt-check, lint-pr, vuln, test-cover-gate). The documented ARGUS_* env-leak on 2 unrelated `internal/agent` tests inside a hera-worker sandbox is pre-existing – confirm clean with those excluded, don't chase it.
- [x] 7.2 `openspec validate add-hera-accept-lifecycle --strict` passes (if the CLI is available; otherwise eyeball the delta files against this repo's format conventions).
- [x] 7.3 Archive this change into its base specs (`hera-coordination`, `task-orchestration`, `hera-view`, `pr-status`) in the same branch, before the final commit, per this repo's CLAUDE.md.
- [x] 7.4 Add gotchas to `context/knowledge/gotchas/orchestration.md` (gater auto-accept) and `context/knowledge/gotchas/hera-view.md` (`ArchiveToggle` direction + session-stop-on-hide) documenting the new invariants; bump `context/knowledge/index.md`'s bullet counts.

## 8. Post-review refinement: the acceptance message requires a reply

**Depends on:** Stages 1-7 (a coordinator ground-truth review after archive found a mid-flight refinement message had been missed)

- [x] 8.1 `internal/hera/accept.go`: rewrite `acceptDefaultBody`/`AcceptTldr` and their doc comment so the message is a closed-loop check-in that explicitly instructs the recipient to reply with exactly one of confirming it's winding down, telling the coordinator it has more work, or asking a question – and states plainly that the reply is informational only and never auto-reopens the task.
- [x] 8.2 `internal/hera/accept_test.go`: new `TestAcceptRole_DefaultBodyRequiresAReply` pins the reply-required wording (substring checks on "reply", "winding down", "more work to do", "question", "does not automatically reopen") so it can't silently regress back into a one-way notice.
- [x] 8.3 Sync every place quoting or paraphrasing the old wording so nothing in the code/specs/docs contradicts the actual string: `internal/mcp/hera.go`'s `hera_accept` tool `Description`, the `hera-coordination` base spec's `hera_accept` requirement + scenarios (both `openspec/specs/` and this archived snapshot), and this change's own `design.md`/`proposal.md` prose.
- [x] 8.4 New commit on top (not amended into the original), per the coordinator's explicit instruction – keeps this stack's history clean for the eventual combined PR.
- [x] 8.5 Re-run `make pre-pr` and confirm it still passes clean.

## 9. Post-review refinement: Enter on a closed-out dead-session worker must refuse, not restart

**Depends on:** Stages 1-8 (found via a coordinator's live-testing session: accept a self-reported-done worker with a dead session, then press `Enter` on the now-`complete` row)

- [x] 9.1 `internal/db/hera.go`: export `heraWorkerAwaitingCloseout` as `HeraWorkerAwaitingCloseout` so the TUI can reuse the exact same close-out predicate `ReviveHeraWorkerToInProgress` already applies; update its one internal call site and doc comment. No behavior change to `ReviveHeraWorkerToInProgress` itself.
- [x] 9.2 `internal/tui/heraactions.go`: `heraReattach`'s dead-session branch calls a new `heraTaskClosedOut(taskID)` helper for worker/freelance selections (`sel.IsWorkerOrFreelance()`) BEFORE calling `startSession`; on a closeout-check error, refuse and surface the error; when awaiting close-out, refuse (no status write, no session start) and set a clear status-bar message ("Task is closed out – use hera_revive to reopen"). Coordinators are unaffected (unconditional restart, unchanged).
- [x] 9.3 `internal/tui/heraactions_test.go`: `TestSmoke_HeraReattachRefusesClosedOutDeadWorker` (table-driven: accepted-complete + ready_to_close, self-reported-done in_review + ready_to_close, in_review + terminal role-status done) asserts Enter is refused – task status unchanged, no `SessionID` set, status bar shows the closed-out message.
- [x] 9.4 `internal/tui/heraactions_test.go`: `TestSmoke_HeraReattachStillRestartsNonClosedOutWorker` regression-guards the unchanged path – a dead-session worker with NO close-out marker still restarts normally through `startSession`.
- [x] 9.5 Sync the `hera-coordination` spec (both `openspec/specs/` and this archived snapshot) with a new "Enter refuses to restart a dead-session worker awaiting close-out" requirement, and this change's own `design.md`/`proposal.md` prose.
- [x] 9.6 Run `make pre-pr` and confirm it passes clean (the documented ARGUS_* env-leak on 2 unrelated `internal/agent` tests inside this hera-worker sandbox is pre-existing – confirmed clean with those excluded; the 3 stdlib-only `govulncheck` findings are confirmed pre-existing on a clean tree too, toolchain-only, CI runs `continue-on-error`).

## 10. Additive requirement: roster/rail must show accepted distinctly from ready

**Depends on:** Stages 1-9 (Aaron approved folding this into 8a rather than a separate follow-up)

- [x] 10.1 `internal/tui/widget/rolestatusicon.go`: add `Accepted bool` to `RoleStatusInputs`; give it precedence in `RoleStatusIcon` below `NeedsInput`/`Active`, above `ReadyToClose`/`Failed`/`Done`/`Idle`/`Live` — a bold `✓` on `theme.StyleComplete`, distinct from plain Done's `✓` and ReadyToClose's bold clipboard-check icon. Update the precedence doc comment.
- [x] 10.2 `internal/tui/hera/rail.go` (`roleStatusInputs`): wire `Accepted: role.TaskStatus == model.StatusComplete.String()`.
- [x] 10.3 `internal/tui/hera/details.go` (`rosterStatusText`): add the matching `"accepted"` case in the same relative precedence position. `hasPR` suffix behavior unchanged. `coordStatusLabel`/`coordTaskStatusLabel` (the coordinator-only path) left untouched per scope.
- [x] 10.4 `internal/tui/hera/rail_test.go`: `TestStatusIcon_AcceptedDistinctAndPrecedence` — Accepted's glyph/style distinct from both Done and ReadyToClose; dominated by NeedsInput/Active; dominates ReadyToClose/Failed/Done/Idle/Live.
- [x] 10.5 `internal/tui/hera/details_test.go`: extend `TestRosterStatusText_Precedence` with `{ReadyToClose:true, TaskStatus: model.StatusComplete.String()}` → `"accepted"` (not `"ready"`), plus a precedence case proving NeedsInput/Active still outrank Accepted.
- [x] 10.6 Sync the `hera-view` spec (both `openspec/specs/` and this archived snapshot): insert `accepted` into the roster-table requirement's precedence enumeration and add an "accepted worker reads distinctly" scenario.
- [x] 10.7 Run `make pre-pr` and confirm it passes clean (same pre-existing ARGUS_* env-leak and stdlib-only `govulncheck` exclusions as Stage 9). Commit locally only — no push, no PR, per the coordinator's instruction; report back via `hera_send` when both this and Stage 9 are committed.

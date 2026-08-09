**Design doc:** `openspec/changes/add-nuke-merge-warning/design.md`
**Depends on:** `add-merge-safety-classifier` landing first.

## 1. Tests

- [ ] 1.1 `internal/tui` test: single-role nuke confirm message is unchanged when the classifier confirms the branch merged.
- [ ] 1.2 `internal/tui` test: single-role nuke confirm message includes the not-confirmed warning when the classifier cannot confirm the branch merged.
- [ ] 1.3 `internal/tui` test: confirming the nuke proceeds identically regardless of the classifier's verdict (never blocked).
- [ ] 1.4 `internal/tui` test: cascade nuke confirm message includes a confirmed/not-confirmed count across the subtree's reclaimed tasks.
- [ ] 1.5 `internal/tui` test: clear-archived (`C`) confirm message includes the same confirmed/not-confirmed count across the tasks it would reclaim.
- [ ] 1.6 `internal/tui` test: the classifier call happens in a goroutine and the confirm modal only opens after it completes (assert ordering via a test seam / synchronization point, not a timing-based sleep).
- [ ] 1.7 `internal/tui` test: a stale selection (role/orchestrator changed or vanished between key-press and the classifier finishing) results in no confirm modal opening, mirroring the existing `fetchGitStatus` staleness-guard pattern.
- [ ] 1.8 `internal/tui` test: no `gh`/network call is made from any of the three nuke entry points (assert via the classifier's test seam recording zero Tier B invocations).
- [ ] 1.9 Confirm every new/modified scenario in `specs/hera-view/spec.md` has a corresponding failing test before implementation (Prove-It Pattern).

## 2. Implementation

**Depends on:** Stage 1

- [ ] 2.1 In `heraOpenDelete`'s single-role branch (`internal/tui/heraactions.go`), replace the synchronous `openHeraConfirm` call with: dispatch a goroutine running the classifier's Tier A check for the role's task, then `a.tapp.QueueUpdateDraw` to build the (possibly warning-augmented) message and open the confirm, with a staleness guard against the selection changing in the interim.
- [ ] 2.2 In `heraCascadeNukeFrom`, extend the existing subtree-walk count computation to also run Tier A checks concurrently (bounded worker pool) for every task that would be reclaimed, and fold the confirmed/not-confirmed counts into the existing count-bearing message — same goroutine + `QueueUpdateDraw` dispatch shape as 2.1.
- [ ] 2.3 In `heraClearArchive`, apply the same treatment as 2.2 for the hidden-archive subtree it confirms over.
- [ ] 2.4 Resolve each task's repo directory and default branch for the classifier call (live task → its worktree/`agent.ResolveDir` + the project's configured branch, matching how `heraReclaimAndArchiveTask` itself already resolves `repoDir`).
- [ ] 2.5 Run `make test-pkg PKG=./internal/tui/` and confirm all Stage 1 tests pass.

## 3. Verification

**Depends on:** Stage 2

- [ ] 3.1 Run `make pre-pr` and confirm it passes clean.
- [ ] 3.2 `openspec validate add-nuke-merge-warning --strict` passes.
- [ ] 3.3 Archive this change into `openspec/specs/hera-view/spec.md` in the same PR, before merge.
- [ ] 3.4 Add a gotcha note to `context/knowledge/gotchas/hera-view.md` documenting the pre-confirm merge-safety check, its Tier-A-only/no-network scope, and the compute-first-then-open-confirm ordering (cross-referencing the `fetchGitStatus` async idiom it mirrors).

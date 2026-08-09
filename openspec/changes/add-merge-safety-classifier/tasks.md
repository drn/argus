**Design doc:** `openspec/changes/add-merge-safety-classifier/design.md`

## 1. Tests

- [ ] 1.1 `internal/gitutil` test: `IsAncestor` reports true for a real ancestor commit, false for a non-ancestor, and an error for an unresolvable ref, using real temp-dir git fixtures.
- [ ] 1.2 `internal/gitutil` test: `ResolveDefaultBranch` uses the configured branch when non-empty; falls back to `origin/HEAD` when empty; falls back further to the existing `priorityBranches` list when neither is available.
- [ ] 1.3 `internal/gitutil` test: the new batched merge-candidate query builder emits one aliased `pullRequests(headRefName:, first: 5, orderBy: {field: CREATED_AT, direction: DESC})` block per branch, grouped under one `repository(...)` per repo, mirroring `buildBatchQuery`'s existing test shape.
- [ ] 1.4 `internal/gitutil` test: the extracted shared query-execution helper is exercised by both `FetchPRStatesBatch`'s existing tests (unchanged behavior) and the new merge-candidate fetch's tests (swapped `gh` seam, canned JSON, no network).
- [ ] 1.5 `internal/mergesafety` test: Tier A confirms safe when the branch exists locally and is an ancestor of the default branch.
- [ ] 1.6 `internal/mergesafety` test: Tier A defers to Tier B when the branch exists locally but is NOT an ancestor (does not short-circuit to not-confirmed without trying Tier B).
- [ ] 1.7 `internal/mergesafety` test: Tier B confirms safe when the branch is gone and exactly one candidate is merged, into the correct base ref, created on/after the task's creation time.
- [ ] 1.8 `internal/mergesafety` test: Tier B rejects a candidate that predates the task's creation time (branch-name-reuse guard).
- [ ] 1.9 `internal/mergesafety` test: Tier B rejects a candidate merged into the wrong base ref.
- [ ] 1.10 `internal/mergesafety` test: Tier B reports not-confirmed (ambiguous) when 2+ candidates are independently plausible.
- [ ] 1.11 `internal/mergesafety` test: reports not-confirmed with a clear reason for an unresolvable repo directory, performing no git or network call.
- [ ] 1.12 `internal/mergesafety` test: Tier A never invokes `git fetch` or any network call (assert via the test seam / a fixture repo with no remote configured).
- [ ] 1.13 Confirm every scenario in `specs/merge-safety/spec.md` has a corresponding failing test before implementation (Prove-It Pattern).

## 2. Implementation

**Depends on:** Stage 1

- [ ] 2.1 Add `IsAncestor(repoDir, commit, target string) (bool, error)` to `internal/gitutil` (new file or `gitcmd.go`), wrapping `git merge-base --is-ancestor` via the existing `runGit` primitive, with a `--` separator before branch-name arguments.
- [ ] 2.2 Add `ResolveDefaultBranch(repoDir, configured string) (short, remoteTrackingRef string, err error)` to `internal/gitutil`, implementing the configured → `origin/HEAD` → `priorityBranches` fallback chain.
- [ ] 2.3 Extract `pr_batch.go`'s temp-file `gh api graphql` execution + rate-limit-cost parsing into a shared unexported helper; re-express `FetchPRStatesBatch` in terms of it with no behavior change.
- [ ] 2.4 Add a new batched merge-candidate fetch (e.g. `FetchMergeCandidatesBatch`) using the shared helper from 2.3, with its own query shape (`state baseRefName mergedAt createdAt url`, `first: 5`) and its own result type distinct from `PRResult`.
- [ ] 2.5 Implement `internal/mergesafety.Classify(ctx, repoDir, branch, defaultRef, taskCreatedAt) (Verdict, error)`: Tier A via `IsAncestor`, falling through to Tier B via `FetchMergeCandidatesBatch` (single-branch call) with the plausibility filter (state/baseRef/timing) from the design doc.
- [ ] 2.6 Implement a batch entry point (e.g. `ClassifyBatch`) that groups multiple (repoDir, branch, defaultRef, taskCreatedAt) candidates by resolved repo and issues one Tier B query per repo for everything that didn't already resolve via Tier A — for future callers that need to classify many tasks at once without one-network-call-per-task.
- [ ] 2.7 Add `--` argument separators to `IsAncestor` and any new subprocess call taking a branch name, per the design doc's argument-injection hardening.
- [ ] 2.8 Run `make test-pkg PKG=./internal/mergesafety/` and `make test-pkg PKG=./internal/gitutil/`, confirm all Stage 1 tests pass.

## 3. Verification

**Depends on:** Stage 2

- [ ] 3.1 Run `make pre-pr` and confirm it passes clean (build, vet, fmt-check, lint-pr, vuln, test-cover-gate), per this repo's per-gate failure recipes in `context/knowledge/gotchas/ci-gates.md` if anything trips.
- [ ] 3.2 `openspec validate add-merge-safety-classifier --strict` passes.
- [ ] 3.3 Archive this change into its base spec (new `openspec/specs/merge-safety/spec.md`) in the same PR, before merge, per this repo's CLAUDE.md.
- [ ] 3.4 Add a gotcha note to `context/knowledge/gotchas/misc.md` (or a new file if it grows) documenting the classifier's fail-closed contract and the branch-name-reuse guard, cross-referencing the historical audit that motivated it.

**Design doc:** `openspec/changes/add-merge-safety-classifier/design.md`

## 1. Tests

- [x] 1.1 `internal/gitutil` test: `IsAncestor` reports true for a real ancestor commit, false for a non-ancestor, and an error for an unresolvable ref, using real temp-dir git fixtures.
- [x] 1.2 `internal/gitutil` test: `ResolveDefaultBranch` uses the configured branch when non-empty; falls back to `origin/HEAD` when empty; falls back further to the existing `priorityBranches` list when neither is available.
- [x] 1.3 `internal/gitutil` test: the new batched merge-candidate query builder emits one aliased `pullRequests(headRefName:, first: 5, orderBy: {field: CREATED_AT, direction: DESC})` block per branch, grouped under one `repository(...)` per repo, mirroring `buildBatchQuery`'s existing test shape.
- [x] 1.4 `internal/gitutil` test: the extracted shared query-execution helper is exercised by both `FetchPRStatesBatch`'s existing tests (unchanged behavior) and the new merge-candidate fetch's tests (swapped `gh` seam, canned JSON, no network).
- [x] 1.5 `internal/mergesafety` test: Tier A confirms safe when the branch exists locally and is an ancestor of the default branch.
- [x] 1.6 `internal/mergesafety` test: Tier A defers to Tier B when the branch exists locally but is NOT an ancestor (does not short-circuit to not-confirmed without trying Tier B).
- [x] 1.7 `internal/mergesafety` test: Tier B confirms safe when the branch is gone and exactly one candidate is merged, into the correct base ref, created on/after the task's creation time.
- [x] 1.8 `internal/mergesafety` test: Tier B rejects a candidate that predates the task's creation time (branch-name-reuse guard).
- [x] 1.9 `internal/mergesafety` test: Tier B rejects a candidate merged into the wrong base ref.
- [x] 1.10 `internal/mergesafety` test: Tier B reports not-confirmed (ambiguous) when 2+ candidates are independently plausible.
- [x] 1.11 `internal/mergesafety` test: reports not-confirmed with a clear reason for an unresolvable repo directory, performing no git or network call.
- [x] 1.12 `internal/mergesafety` test: Tier A never invokes `git fetch` or any network call (assert via the test seam / a fixture repo with no remote configured).
- [x] 1.13 Confirm every scenario in `specs/merge-safety/spec.md` has a corresponding failing test before implementation (Prove-It Pattern).

## 2. Implementation

**Depends on:** Stage 1

- [x] 2.1 Add `IsAncestor(repoDir, commit, target string) (bool, error)` to `internal/gitutil` (new file or `gitcmd.go`), wrapping `git merge-base --is-ancestor` via the existing `runGit` primitive, with a `--` separator before branch-name arguments.
- [x] 2.2 Add `ResolveDefaultBranch(repoDir, configured string) (short, remoteTrackingRef string, err error)` to `internal/gitutil`, implementing the configured → `origin/HEAD` → `priorityBranches` fallback chain. (Uses `--end-of-options` rather than `--` for the `rev-parse --verify` calls specifically — `git rev-parse` treats a bare `-- <rev>` as a pathspec transition, not an end-of-options marker, silently misresolving a valid branch name as "not found"; `--end-of-options` is git's actual supported separator for that command. `merge-base --is-ancestor` (used by `IsAncestor`) does not have this quirk and uses plain `--`.)
- [x] 2.3 Extract `pr_batch.go`'s temp-file `gh api graphql` execution + rate-limit-cost parsing into a shared unexported helper (`runAliasedRepoQuery`); re-express `FetchPRStatesBatch` in terms of it with no behavior change.
- [x] 2.4 Add a new batched merge-candidate fetch (`FetchMergeCandidatesBatch`) using the shared helper from 2.3, with its own query shape (`state baseRefName mergedAt createdAt url`, `first: 5`) and its own result type (`MergeCandidate`) distinct from `PRResult`.
- [x] 2.5 Implement `internal/mergesafety.Classify(ctx, Params) (Verdict, error)`: Tier A via `IsAncestor`, falling through to Tier B via `FetchMergeCandidatesBatch` (single-branch call) with the plausibility filter (state/baseRef/timing) from the design doc.
- [x] 2.6 Implement a batch entry point `ClassifyBatch(ctx, []Candidate) map[string]Verdict` that groups multiple candidates by resolved repo and issues one Tier B query per repo for everything that didn't already resolve via Tier A.
- [x] 2.7 Argument-injection hardening: `IsAncestor` guards with `--`; `ResolveDefaultBranch`'s `rev-parse` calls guard with `--end-of-options` (see 2.2). The merge-candidate query's branch names are GraphQL string literals (`%q`-escaped into query text, never passed as bare subprocess argv), so the argv-injection concern from the design doc doesn't apply there — only `gh api graphql -F query=@<tmpfile>` is invoked, with no branch-derived argv at all.
- [x] 2.8 Run `make test-pkg PKG=./internal/mergesafety/` and `make test-pkg PKG=./internal/gitutil/`, confirm all Stage 1 tests pass (also verified under `-race -count=1`).

## 3. Verification

**Depends on:** Stage 2

- [x] 3.1 Run `make pre-pr` and confirm it passes clean (build, vet, fmt-check, lint-pr, vuln, test-cover-gate). build/vet/fmt-check/lint-pr (0 issues) all clean. `vuln` fails only on documented pre-existing Go-toolchain-only stdlib CVEs (GO-2026-5856/5039/5037, `crypto/tls`/`net/textproto`/`crypto/x509`), a known CI continue-on-error condition per `gotchas/ci-gates.md`. `test-cover-gate`'s single `internal/tui/terminal` failure is the documented full-suite `-race` PTY/goroutine-contention flake from the same gotcha file (untouched package, unrelated to this change) — confirmed via isolation (`go test -race -count=1 ./internal/tui/terminal/...` passes cleanly in ~64s). Every package this change actually touches (`internal/gitutil`, `internal/mergesafety`) passed cleanly under `-race -count=1`, individually and together, with 92–94% coverage.
- [x] 3.2 `openspec validate add-merge-safety-classifier --strict` passes.
- [x] 3.3 Archive this change into its base spec (new `openspec/specs/merge-safety/spec.md`) in the same PR, before merge, per this repo's CLAUDE.md.
- [x] 3.4 Add a gotcha note to `context/knowledge/gotchas/misc.md` documenting the classifier's fail-closed contract and the branch-name-reuse guard, cross-referencing the historical audit that motivated it.

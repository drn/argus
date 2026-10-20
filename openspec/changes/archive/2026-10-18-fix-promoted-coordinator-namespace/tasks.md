## 1. Design and regression tests

- [x] 1.1 Read prior changes, write deltas, and send chosen design to the coordinator.
- [x] 1.2 Reproduce plain-task self-promotion, worker promotion, and planned worker/subcoordinator collisions with isolated HOME and actual Git repositories.
- [x] 1.3 Cover packed refs, independent sanitization, long names, fallback collisions, stable repeated spawns, and fail-open probes.

## 2. Implementation

- [x] 2.1 Resolve local namespace leaves in the target source repository, deterministically choosing a length-safe fallback.
- [x] 2.2 Apply shared resolution to all three worker/materialize primitives and log resolved and skipped checks.

## 3. Verification and delivery

- [x] 3.1 Run regression tests and make pre-pr; review implementation and test effectiveness.
- [x] 3.2 Update worktree gotchas and the knowledge index.
- [x] 3.3 Archive deltas into base specs in this PR.

## Validation results

- Regression tests and strict OpenSpec validation pass; code review and test-adversary review found no actionable issues.
- With Go 1.26.9, build, vet, formatting, lint, and vulnerability checks pass. Go 1.26.8 has standard-library vulnerability findings; no module changes were needed.
- The full `make pre-pr` race run passes every package except the documented `internal/tui` 120-second timeout on a loaded host. An isolated run with Go's default timeout passes in 159.734 seconds; no Makefile or test timeout was changed.
- Replacing the incomplete TUI profile with the successful isolated profile gives 88.7% filtered coverage (88% gate); the namespace resolver has 100% statement coverage.

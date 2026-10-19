## 1. Design and regression tests

- [x] 1.1 Read prior changes, write deltas, and send chosen design to the coordinator.
- [x] 1.2 Reproduce plain-task self-promotion, worker promotion, and planned worker/subcoordinator collisions with isolated HOME and actual Git repositories.
- [x] 1.3 Cover packed refs, independent sanitization, long names, fallback collisions, stable repeated spawns, and fail-open probes.

## 2. Implementation

- [x] 2.1 Resolve local namespace leaves in the target source repository, deterministically choosing a length-safe fallback.
- [x] 2.2 Apply shared resolution to all three worker/materialize primitives and log resolved and skipped checks.

## 3. Verification and delivery

- [ ] 3.1 Run regression tests and make pre-pr; review implementation and test effectiveness.
- [x] 3.2 Update worktree gotchas and the knowledge index.
- [ ] 3.3 Archive deltas into base specs in this PR.

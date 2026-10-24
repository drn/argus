## 1. Specify and verify

- [x] 1.1 Create proposal, design, and sandbox-execution delta for the fixed contract.
- [x] 1.2 Obtain coordinator approval of the change scope (hera message 16889).
- [x] 1.3 Add failing regression tests for wrapped/unwrapped launches, reserved-value removal, backend parity, resume, and recycle.

## 2. Implement

- [x] 2.1 Finalize and log the reserved sandbox environment signal in the shared launch path.
- [x] 2.2 Bump supervisor spawn surface and protocol metadata and refresh the digest.
- [x] 2.3 Document the launch-signal invariant and update README reference material.

## 3. Ship for review

- [x] 3.1 Run targeted tests, full make pre-pr, OpenSpec validation, and guard.
- [x] 3.2 Archive the change into base specs before opening the PR.
- [x] 3.3 Prepare the PR handoff with companion-hook merge ordering and the rebase digest command.

Validation: `make pre-pr` passed with Go 1.26.10, 88.8% filtered coverage, and inherited `CODEX_HOME`, `CODEX_SQLITE_HOME`, and `CLAUDE_CONFIG_DIR` unset so existing session-capture tests use their temporary fixtures. The original Go 1.26.8 toolchain failed the vulnerability gate. Targeted race tests and the supervisor digest/skew tests passed. Guard found no secrets, security antipatterns, or unwanted artifacts.

After archival, commit and push via iris, open the PR to `drn/argus` `master`, and send its URL to the coordinator. Do not merge or deploy. The ai-ron hook PR merges afterward. When rebasing onto the parallel bridge work, reconcile spawn/protocol versions and refresh `SpawnSurfaceDigest` using the value printed by `go test ./internal/daemon -run '^TestSupervisorSurfaceDigest/spawn$' -count=1`, then rerun the test and `make pre-pr`.

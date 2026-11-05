## 1. Stop helper and matching (worktree-management)

- [x] 1.1 Tests then code: executable-basename matching (argv[0] / token after `devbox run --`); argument-mention and prefix-sibling negatives; Oct 30 shapes (`devbox run -- mysqld --datadir=...`, launchd-parented mysqld) parse
- [x] 1.2 Tests then code: `stopDevStackProcs` supervisor-first ordering with injected scan/signal/sleep seams; `RemoveWorktree` uses it

## 2. Sweeper (worktree-management)

- [x] 2.1 Config key `devstack.stop_finished` (default true, live-read), config tests
- [x] 2.2 Tests then code: `devstackreap.go` eligibility (six-tick (~60s) grace, no session, no pending restart), resume exemption, first-tick backlog, unfinished/unknown-task skips, scan-failure skip, setting off; injected scan/signal/runner/tasks seams, no real processes
- [x] 2.2b Tests: managed-dir guard (main checkout, legacy `.claude/worktrees`, empty path skipped); shared-path multi-claimant (unfinished / live / exempt claimant blocks)
- [x] 2.3 Start the loop in `daemon.go` next to `runFinishedSessionReaper`; slog + uxlog lines

## 3. Doctor (binary-coherence)

- [x] 3.1 Tests then code: finished-task classification in `cmd/argus/doctor.go` + `internal/doctor/devstack.go` render; unknown state; exit code unchanged

## 4. Docs and close-out

- [ ] 4.0 Post-deploy (human, NOT done pre-merge): live-confirm the `devbox run -- mysqld` and launchd-parented shapes via `argus doctor`; parser tests are the only pre-merge verification
- [x] 4.1 Gotchas in `context/knowledge/gotchas/worktree.md` (incl. daemon has no uxlog.Init) and `context/knowledge/index.md`; README Reference only if a table is affected
- [x] 4.2 Surface test needs no digest re-record (confirmed); `make pre-pr` run before push
- [x] 4.3 Archive the change in the same PR

## 1. Dev-stack scan + teardown (internal/agent)

- [x] 1.1 Add `internal/agent/devstack.go`: `DevStackProc` type, `devStackProcessNames`, injectable `pgrepOutput` exec seam (mirrors `cmdFactory` in `internal/claudeagents`).
- [x] 1.2 Implement `ScanDevStackProcesses()` parsing `pgrep -fl` output; pgrep exit code 1 (no matches) returns `(nil, nil)`, not an error.
- [x] 1.3 Implement `extractWorktreePath` (regex matching `.../.argus/worktrees/<project>/<task>` or `.../.claude/worktrees/<project>/<task>`, stopping at the task-level directory). Also excludes `=` and `:` from the path prefix, not just whitespace — caught via a live `argus doctor` smoke test against real redis-server processes (`unixsocket:<path>` glues the flag directly onto the path with no space).
- [x] 1.4 Implement `stopDevStackFor(worktreePath string)`: exact-path-segment filter, SIGTERM all matches, sleep grace period, re-scan, SIGKILL survivors. Best-effort, uxlog throughout.
- [x] 1.5 Unit tests for `extractWorktreePath` and pgrep-output parsing using the injectable seam (no real processes).
- [x] 1.6 Integration-style test: spawn a real long-lived fake process (a shell script literally named after a known dev-stack binary, living inside a fake worktree dir — `sleep`'s BSD implementation rejects extra positional args, ruling out the originally-planned marker-token approach) and verify `stopDevStackFor` finds and kills it by real PID (SIGTERM path; a background reaper goroutine avoids a zombie-PID false negative in the liveness check).
- [x] 1.7 Test the path-prefix-collision guard: two "worktrees" with one name a prefix of the other; confirm only the exact match is signaled.

## 2. Wire into RemoveWorktree

- [x] 2.1 Call `stopDevStackFor(worktreePath)` at the top of `RemoveWorktree` in `internal/agent/cleanup.go`, before `git worktree remove` / `os.RemoveAll`.
- [x] 2.2 Add/extend `cleanup_test.go` coverage for the new call (behind the same test seams as 1.5/1.6).
- [x] 2.3 Confirmed `testGuard` ordering: the teardown call sits AFTER the existing `testGuard`/`dirExists`/`IsWorktreeSubdir` checks, so a `testGuard`-blocked or invalid path never reaches the dev-stack scan at all — no change to existing test-safety invariants.

## 3. Doctor advisory check (internal/doctor + cmd/argus)

- [x] 3.1 Add `internal/doctor/devstack.go`: `DevStackOrphan` type, tri-state status (Found/None/Unknown), `DiagnoseDevStackOrphans`, `RenderDevStackOrphans` — pure, no I/O, mirrors `profilelib.go`/`stophook.go`.
- [x] 3.2 Unit tests for the pure classification + rendering functions (no process spawning, no filesystem).
- [x] 3.3 Add `gatherDevStackOrphans()` + `diagnoseDevStackOrphansFrom()` in `cmd/argus/doctor.go`: calls `agent.ScanDevStackProcesses()`, checks each candidate's worktree path via an injectable `exists` predicate (`agent.DirExists` in production), feeds the result to `doctor.DiagnoseDevStackOrphans`.
- [x] 3.4 Wire the new gather+render call into `runDoctor()`, alongside the existing Stop-hook and diligence-profile-library sections. Exit code unaffected — verified via a live `argus doctor` run, which also caught 3 genuinely pre-existing real orphans (confirmed their worktree dirs are in fact deleted).

## 4. Docs + quality gate

- [x] 4.1 Added an entry to `context/knowledge/gotchas/worktree.md` documenting the teardown hook, the devbox-CLI-hang finding, the no-cascade-assumed signaling approach, and the `=`/`:` path-extraction pitfall.
- [x] 4.2 Ran `make pre-pr`. `build`/`vet`/`fmt-check`/`lint-pr` green. `vuln` fails only on pre-existing stdlib CVEs (documented as CI continue-on-error). The full-suite `-race` `test-cover-gate` step hit two known classes of pre-existing flake unrelated to this change's files (`internal/tui/terminal`'s PTY/`io.Pipe` resource-exhaustion timeout, already documented in `gotchas/ci-gates.md`; and two `internal/tui` tests — one timing-floor assertion sensitive to `-race` overhead under load, one that passed cleanly on an isolated retry) — confirmed via the project's documented isolate/serialize recipe, none touch a file this change modified. A serialized (`-p 1`) full-tree run measured filtered coverage at 90.9%, above the 88% floor.
- [x] 4.3 `openspec archive fix-devstack-orphaning` — deltas merged into `openspec/specs/binary-coherence/spec.md` and `openspec/specs/worktree-management/spec.md`; change folder moved here.
- [ ] 4.4 Open PR via `iris_gh_pr_create`.

**Design doc:** `openspec/changes/fix-inherited-claude-session-marker/design.md`

## 1. Failing tests

- [x] 1.1 Write failing tests for every scenario in `specs/agent-execution/spec.md` against `spawnBaseEnv` / `BuildCmd` (markers stripped for Claude and Codex, settings variables kept, backend `env_vars` wins, name-only log, persistence flag on Claude only). Use `t.Setenv("HOME", t.TempDir())` and `agent.NewRunner(nil)`.
- [x] 1.2 Write a failing test for `specs/usage-budget-routing/spec.md` against `startPTYSession`'s environment, using a stand-in command
- [x] 1.3 Confirm each new test fails for the intended reason (Prove-It)

## 2. Scrub and persist at every spawn

**Depends on:** Stage 1

- [x] 2.1 Add one exported denylist slice of parent-session marker names; strip it in `spawnBaseEnv` and include stripped names in the existing inherited-env log line
- [x] 2.2 Export `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1` for Claude-family spawns in `BuildCmd`, before backend `env_vars` are appended
- [x] 2.3 Apply the same denylist and flag in the usage probe's `startPTYSession`
- [x] 2.4 Re-record the supervisor spawn-surface digest (`gotchas/ci-gates.md` recipe)

## 2b. Ignore SDK-launched transcripts in session discovery

- [x] 2b.1 Failing tests: `claudesession` listing drops `sdk-*` entrypoints and keeps entrypoint-less transcripts; `CaptureClaudeSessionID` never returns a newer SDK transcript and reports no ID when only SDK transcripts exist
- [x] 2b.2 Record `entrypoint` on `claudesession.Session` and skip `sdk-*` in `ListIn`

## 3. Docs, verification, archive

**Depends on:** Stage 2

- [x] 3.1 Add a gotcha to `context/knowledge/gotchas/daemon-rpc.md`: restarting the daemon/supervisor from inside Claude Code leaks the parent session's markers; the spawn boundary scrubs them; persistence is forced for Claude agents. Update the index row.
- [ ] 3.2 `make pre-pr` green
- [ ] 3.3 Live verification: deploy, restart supervisor + daemon from a clean environment at a quiet point, spawn a Claude agent, and confirm via `ps eww` that it has no `CLAUDE_CODE_CHILD_SESSION` and has `CLAUDE_CODE_FORCE_SESSION_PERSISTENCE=1`, and that the transcript-off warning no longer appears
- [ ] 3.4 Archive the change (`openspec archive fix-inherited-claude-session-marker`) on the branch before merge

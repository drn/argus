**Design doc:** `openspec/changes/fix-agent-cache-tcc-prompts/design.md`

## 1. Tests

- [ ] 1.1 Add `TestBuildCmd_RedirectsBuildCaches` in `internal/agent/agent_test.go`, modeled on the existing `TestBuildCmd_ForcesTerminalEnv` (same file, ~line 394): assert `cmd.Env`'s last `GOCACHE=` value is under `db.DataDir()+"/cache/go-build"` and the last `PLAYWRIGHT_BROWSERS_PATH=` value is under `db.DataDir()+"/cache/ms-playwright"`, using the same "scan for last value per key" helper pattern (later entries win per `exec.Cmd.Env` dedup semantics).
- [ ] 1.2 Add a case to the same test (or a sibling subtest) asserting the forced values win even when the parent env already sets `GOCACHE`/`PLAYWRIGHT_BROWSERS_PATH` to something else (mirrors scenario 3 in the delta spec) — set both via `t.Setenv` to a bogus value before calling `BuildCmd`, then assert the forced `~/.argus/cache/...` value is what's actually in `cmd.Env`, not the bogus one.
- [ ] 1.3 Run `go test ./internal/agent/... -run TestBuildCmd` and confirm both new cases fail (Prove-It Pattern) before touching `BuildCmd` itself.

## 2. Implementation

**Depends on:** Stage 1

- [ ] 2.1 In `internal/agent/agent.go`'s `BuildCmd`, extend the existing forced-env block (~line 826-829, right after `TERM`/`COLORTERM`) with two more `cmd.Env` entries: `"GOCACHE=" + filepath.Join(db.DataDir(), "cache", "go-build")` and `"PLAYWRIGHT_BROWSERS_PATH=" + filepath.Join(db.DataDir(), "cache", "ms-playwright")`. Reuse `db.DataDir()` (already imported in this file) rather than re-resolving `$HOME`.
- [ ] 2.2 Extend the doc comment above the existing `cmd.Env` block to cover the new entries' rationale (TCC prompt avoidance), matching the existing comment's style for the TERM/COLORTERM rationale.
- [ ] 2.3 Run `go test ./internal/agent/... -run TestBuildCmd` and confirm all cases (existing + new) pass.
- [ ] 2.4 Run `make test-cover` and confirm `internal/agent` stays at or above its existing coverage floor.

## 3. Docs

**Depends on:** Stage 2

- [ ] 3.1 Add a bullet to `context/knowledge/gotchas/sandbox.md`, next to the existing "macOS TCC re-prompts" bullet, noting that `GOCACHE`/`PLAYWRIGHT_BROWSERS_PATH` are now forced to `~/.argus/cache/...` on every spawned agent specifically to stop those two tools from contributing to the TCC prompt, and that Chrome's crashpad path remains a known, unfixable exception (cross-reference the existing bullet rather than duplicating its content).
- [ ] 3.2 Run `make pre-pr` and confirm it passes clean.

## 4. Archive

**Depends on:** Stage 3

- [ ] 4.1 Run `openspec archive fix-agent-cache-tcc-prompts` (or apply the merge-and-move by hand if the CLI is unavailable) before merge, per this repo's CLAUDE.md archiving requirement.

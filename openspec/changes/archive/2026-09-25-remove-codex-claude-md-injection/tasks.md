## 1. Code

- [x] 1.1 Simplify `nonClaudeContextPrefix` in `internal/agent/nonclaude_context.go` to prepend
      routing orientation only, for `isCodex || isOpencode`; drop the Codex-only CLAUDE.md branch.
- [x] 1.2 Delete the now-unused `readGlobalClaudeMD`, `readGlobalClaudeMDReal`, `readRepoClaudeMD`,
      `readClaudeMDFile`, `maxClaudeMDBytes`, `readGlobalClaudeMDFn`, `SetReadGlobalClaudeMDForTest`.
- [x] 1.3 Update the stale comment above the `nonClaudeContextPrefix` callsite in
      `internal/agent/agent.go` (references CLAUDE.md; Codex no longer receives it).

## 2. Tests

- [x] 2.1 Remove tests pinned to the deleted CLAUDE.md-reading functions.
- [x] 2.2 Update `TestNonClaudeContextPrefix_*` / `TestBuildCmd_NonClaudeContextPrefix_*` Codex
      cases so Codex is asserted symmetric with opencode (routing only, no CLAUDE.md content).
- [x] 2.3 `go test ./internal/agent/...` green.

## 3. Docs

- [x] 3.1 Archive this change into `openspec/specs/agent-execution/spec.md` in the same PR.
- [x] 3.2 Add a gotcha bullet to `context/knowledge/gotchas/misc.md` documenting the empirical
      reversal (Codex's global `~/.codex/AGENTS.md` is real and read unconditionally, confirmed on
      codex-cli 0.157.0) so a future reader doesn't re-add this injection based on the old,
      now-outdated uncertainty.

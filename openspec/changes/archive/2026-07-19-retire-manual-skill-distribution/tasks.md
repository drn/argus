## 1. Self-guard

- [x] 1.1 Confirm PR #866 (skill embedding) and PR #871 (routing-content injection) are merged to master, and the `--add-dir` / `--append-system-prompt-file` wiring exists in `internal/agent/agent.go`.

## 2. Remove the manual distribution mechanism

- [x] 2.1 Diff `claude/snippets/hera.md` and `claude/snippets/argus-tasks.md` against `internal/routing/builtin/{hera,argus-tasks}.md` — confirm byte-identical.
- [x] 2.2 Delete `install-claude-skills.sh`, `uninstall-claude-skills.sh`.
- [x] 2.3 Delete `claude/snippets/hera.md`, `claude/snippets/argus-tasks.md` (and the now-empty `claude/` directory).
- [x] 2.4 Remove `internal/routing/routing_test.go`'s `TestBuiltinContent_MatchesRepoSnippets` drift-guard test (its source files no longer exist).

## 3. Update docs and comments

- [x] 3.1 Rewrite `README.md`'s "Agent-facing skills" section to describe the automatic mechanism, no install step.
- [x] 3.2 Fix the other README reference to `install-claude-skills.sh` (tools table, above the Agent-facing skills section).
- [x] 3.3 Update `internal/routing/routing.go`'s package comment (no longer cites `claude/snippets/*.md` as the source of truth).
- [x] 3.4 Update `context/knowledge/gotchas/misc.md` and `context/knowledge/index.md` to stop citing the retired files/test.

## 4. Spec

- [x] 4.1 Write a delta for `routing-provisioning`'s "Builtin routing content bundle" requirement, dropping the byte-identity-against-`claude/snippets` clause.
- [x] 4.2 Archive this change into the base spec in the same PR.

## 5. Verify

- [x] 5.1 `go build ./... && go test ./internal/routing/...` green after the drift-guard test removal.
- [ ] 5.2 `make pre-pr` clean.

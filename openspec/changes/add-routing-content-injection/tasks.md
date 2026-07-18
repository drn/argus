## 1. Self-guard (foundation check)

- [x] 1.1 Merge `origin/master` (via `upstream` https remote — `origin`'s SSH signing was unavailable in this sandbox) and confirm the current branch already contains `argus/3a-land-845`'s tip.
- [x] 1.2 Confirm `internal/skills/builtin` exists and `BuildCmd` (`internal/agent/agent.go`) appends `--add-dir` for it — foundation from PR #866 is present.

## 2. Builtin routing content bundle (`internal/routing`)

- [x] 2.1 Copy `claude/snippets/hera.md` and `claude/snippets/argus-tasks.md` verbatim into `internal/routing/builtin/hera.md` and `internal/routing/builtin/argus-tasks.md`.
- [x] 2.2 Add `internal/routing/routing.go`: `//go:embed builtin`, `BuiltinContent()` (concatenates the embedded files sorted by name), `materialize(workspaceRoot string)` (idempotent write via temp-file-then-rename), `EnsureBuiltinRouting()` (resolves `~/.argus/routing`, `isTestBinary()`-gated like `skills.EnsureBuiltinSkills`).
- [x] 2.3 Add `internal/routing/routing_test.go`: embedded content contains both sections; embedded copies are byte-identical to `claude/snippets/hera.md` / `claude/snippets/argus-tasks.md` read directly off disk (drift guard); `materialize` writes the concatenated content to `<dir>/system-prompt.md`; `materialize` is idempotent (no rewrite when content unchanged); `EnsureBuiltinRouting` returns `("", nil)` under `go test`.

## 3. `BuildCmd` wiring (`internal/agent`)

- [x] 3.1 Add `internal/agent/routing_prompt.go`: package var `ensureBuiltinRoutingFn = routing.EnsureBuiltinRouting` plus `SetEnsureBuiltinRoutingForTest` (mirrors `ensurePrelaunchFn`/`autoRenameFn`).
- [x] 3.2 In `BuildCmd`, immediately after the existing skills `--add-dir` block: for Claude backends, call `ensureBuiltinRoutingFn()`; on error, log and continue; on non-empty path, append `--append-system-prompt-file <path>`.
- [x] 3.3 Tests in `internal/agent/agent_test.go`: flag appended for Claude backends when the seam returns a fake path; flag withheld for codex/pi/opencode/bare backends even when the seam returns a path; materialization error is logged and non-fatal (command still built, no flag).

## 4. Docs

- [x] 4.1 Document the unconditional-injection invariant in `context/knowledge/gotchas/` alongside the existing `--add-dir` skills note (`misc.md`, where `hera plugin contract` / `config.toml override layer` etc. live, or wherever the skills `--add-dir` invariant itself is documented).

## 5. Quality gate and ship

- [x] 5.1 Run `make pre-pr` (build → vet → fmt-check → lint-pr → vuln → test-cover-gate); all green (or only the pre-existing documented `vuln` stdlib advisory).
- [ ] 5.2 Archive this change (`openspec archive`) within the PR before it is ready.
- [ ] 5.3 Open a PR via `iris_gh_pr_create`, based on `argus/3a-land-845`, noting it is stacked pending #866's merge.

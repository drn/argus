## 1. Self-guard (foundation check)

- [x] 1.1 `git fetch && git merge --no-edit origin/master`; confirm the current branch already contains master's tip (#866/#871/#872).
- [x] 1.2 Read all 3 SKILL.md bodies from `f6ac45b5` (`.claude/skills/{hera-review,hera-spawn-review,hera-review-test-adversary}/SKILL.md`); confirm `hera-review`/`hera-review-test-adversary` are self-contained prose and `hera-spawn-review` hard-depends on unshipped Go infra (`mcp__argus__profile_resolve`, `internal/review`, `openspec/changes/add-cross-vendor-review`) — confirmed absent via grep across the repo.
- [x] 1.3 Escalate the scope question to the coordinator (3 skills "shipped" but never merged; 1 of 3 has hidden deps) and get explicit sign-off before narrowing scope (hera messages #3211/#3215, approved #3212/#3216).

## 2. Ship the 2 clean skills (`.claude/skills/`)

- [x] 2.1 `git show f6ac45b5:.claude/skills/hera-review/SKILL.md` → `.claude/skills/hera-review/SKILL.md`.
- [x] 2.2 `git show f6ac45b5:.claude/skills/hera-review-test-adversary/SKILL.md` → `.claude/skills/hera-review-test-adversary/SKILL.md`.
- [x] 2.3 Do NOT copy `hera-spawn-review/SKILL.md` — explicitly deferred.

## 3. Routing/orientation content (`internal/routing/builtin`)

- [x] 3.1 Add `internal/routing/builtin/hera-review.md`: mirror `hera.md`/`argus-tasks.md`'s shape (frontmatter + `ARGUS_TASK_ID`/sandbox gate + short directive) — prefer `hera-review` (and `hera-review-test-adversary` when tests are touched) over ad hoc review; note `hera-spawn-review` panel orchestration is not yet shipped.
- [x] 3.2 Confirm `BuiltinContent()` (`internal/routing/routing.go`) requires no code change — it already concatenates every file under the embedded root, sorted by name.

## 4. Skill-body whitelist extension (`internal/skills/builtin`)

- [x] 4.1 Copy `.claude/skills/hera-review/SKILL.md` → `internal/skills/builtin/hera-review/SKILL.md`.
- [x] 4.2 Copy `.claude/skills/hera-review-test-adversary/SKILL.md` → `internal/skills/builtin/hera-review-test-adversary/SKILL.md`.
- [x] 4.3 Confirm `BuiltinItems()`/`EnsureBuiltinSkills()` (`internal/skills/builtin.go`) require no code change — both already iterate the embedded directory tree generically.

## 5. Tests

- [x] 5.1 `internal/routing/routing_test.go`: add a test asserting `BuiltinContent()` includes the new code-review orientation section header.
- [x] 5.2 New `internal/skills/builtin_test.go` (previously absent): assert `BuiltinItems()` includes `hera-review` and `hera-review-test-adversary` with their frontmatter descriptions, alongside the pre-existing 5.

## 6. Docs

- [x] 6.1 Document the parity fix + `hera-spawn-review` deferral in `context/knowledge/gotchas/misc.md` (or the routing/skills-relevant gotcha file).

## 7. Quality gate and ship

- [ ] 7.1 Run `make pre-pr` (build → vet → fmt-check → lint-pr → vuln → test-cover-gate); fix any failures.
- [ ] 7.2 Archive this change (`openspec archive ship-hera-review-skills`) within the PR before it is ready.
- [ ] 7.3 Open a PR via `iris_gh_pr_create`, base `master`.
- [ ] 7.4 Report the PR link back to the coordinator via `hera_send(status="done", ...)`.

## 1. Self-guard (foundation check)

- [x] 1.1 Confirm the deferred blocker (`mcp__argus__profile_resolve`, `internal/review`) actually exists on master by grepping source, not trusting the live MCP tool list: `internal/mcp/profiles.go` has `toolProfileResolve` wired to `case "profile_resolve":`; `internal/review/` exists with `panel.go`/`knownInSessionModels`.
- [x] 1.2 Read both `.claude/skills/{hera-spawn-review,resolve-archetype-model}/SKILL.md` bodies in full; confirm neither has any additional hidden Go/MCP dependency beyond the now-shipped `profile_resolve`/`internal/review`.

## 2. Skill bodies (`internal/skills/builtin`)

- [x] 2.1 Copy `.claude/skills/hera-spawn-review/SKILL.md` → `internal/skills/builtin/hera-spawn-review/SKILL.md` (byte-identical).
- [x] 2.2 Copy `.claude/skills/resolve-archetype-model/SKILL.md` → `internal/skills/builtin/resolve-archetype-model/SKILL.md` (byte-identical).
- [x] 2.3 Confirm `BuiltinItems()`/`EnsureBuiltinSkills()` (`internal/skills/builtin.go`) require no code change — both already iterate the embedded directory tree generically.

## 3. Routing/orientation content (`internal/routing/builtin`)

- [x] 3.1 Add `internal/routing/builtin/hera-spawn-review.md`: self-gated on `ARGUS_TASK_ID`/sandbox residency, directive for when to prefer a panel review over a single-reviewer pass.
- [x] 3.2 Add `internal/routing/builtin/resolve-archetype-model.md`: self-gated, directive for when native sub-agent dispatch should resolve its model from the diligence profile.
- [x] 3.3 Fix the stale "`hera-spawn-review` … has not shipped yet" claim in `internal/routing/builtin/hera-review.md`.
- [x] 3.4 Confirm `BuiltinContent()` (`internal/routing/routing.go`) requires no code change — it already concatenates every file under the embedded root, sorted by name.

## 4. Tests

- [x] 4.1 `internal/skills/builtin_test.go`: extend the expected-names list (in `BuiltinItems()`'s actual sorted order) and add description-coverage assertions for both new skills.
- [x] 4.2 `internal/routing/routing_test.go`: add tests asserting `BuiltinContent()` includes both new section headers and their skill names.

## 5. Docs

- [x] 5.1 Document the parity fix in `context/knowledge/gotchas/misc.md` (fix the now-stale #874 deferral note + add a new bullet for this shipment).
- [x] 5.2 Update `context/knowledge/index.md`'s `misc.md` row summary + bullet count.

## 6. Quality gate and ship

- [x] 6.1 Run `make pre-pr` (build → vet → fmt-check → lint-pr → vuln → test-cover-gate); fix any failures. (Green modulo two pre-existing, non-introduced issues: advisory-only stdlib CVEs in `make vuln`, continue-on-error in CI; and the known hera-worker-sandbox `ARGUS_TASK_ID`/`ARGUS_ARCHETYPE`/`ARGUS_MODEL`/`ARGUS_PROFILE` env-leak false-fail in `internal/agent`'s profile-env tests, confirmed to pass clean with those vars unset.)
- [x] 6.2 Archive this change (`openspec archive embed-remaining-model-tiering-skills`) within the PR before it is ready.
- [ ] 6.3 Open a PR via `iris_gh_pr_create`, base `master`. Do not merge.
- [ ] 6.4 Report the PR link back to the coordinator via `hera_send`, orchestrator `hera-merge-model-tiers`.

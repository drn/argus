## 1. Fix the JSON casing bug

- [x] 1.1 Add `json` tags to `profiles.Archetype` (`model`, `effort`, `window`, all `omitempty`) in `internal/profiles/profiles.go`
- [x] 1.2 Add `json` tags to `profiles.Rigor` (`review_passes`, `gating`, `security_spot_check`, all `omitempty`) in `internal/profiles/profiles.go`
- [x] 1.3 Strengthen `TestProfileResolve_ArchetypePassthroughVerbatim` (`internal/mcp/profiles_test.go`) to assert on the raw JSON string containing lowercase keys (`"model"`, `"effort"`, `"window"`), not just a case-insensitive struct unmarshal, so a future tag regression is actually caught
- [x] 1.4 Add an equivalent raw-JSON-casing assertion for the `[rigor]` block (`review_passes`/`gating`/`security_spot_check`)

## 2. Ship the reusable skill

- [x] 2.1 Write `.claude/skills/resolve-archetype-model/SKILL.md`: resolve-once-per-pipeline convention, fail-open rules (unresolved profile, missing/empty archetype entry), the in-session model gate (`opus`/`sonnet`/`haiku`/`fable`, mirroring `internal/review.knownInSessionModels`), and the effort-threading scope (only where the dispatch mechanism accepts an effort parameter — document the built-in `Agent` tool's current lack of one, and `Workflow`'s `agent()` `opts.effort` as the mechanism that does)
- [x] 2.2 Include a short worked example in the skill: build the archetype→`{model,effort}` map from one `profile_resolve` call, then two example dispatches — one via the `Agent` tool (`model=` only) and one via `Workflow`'s `agent()` (`model=` and `effort=`)
- [x] 2.3 Cross-reference `hera-spawn-review` in the new skill as prior art for the same pattern (informational only — do not edit `hera-spawn-review`, `hera-review`, `hera`, or `hera-plan`)

## 3. Documentation

- [x] 3.1 Add a `profile_resolve` row to the README's MCP Tools reference table (currently undocumented there)
- [x] 3.2 Cross-reference the new `resolve-archetype-model` skill from the README's "Diligence profiles (model tiering)" section
- [x] 3.3 Add a bullet documenting the JSON casing gotcha + the archetype-native-dispatch convention pointer — landed in `context/knowledge/gotchas/misc.md`'s existing "Diligence profiles" section (not `orchestration.md`), matching where the rest of the diligence-profiles gotchas already live (index.md's own topic map, confirmed by checking the file rather than assuming the task's original guess)

## 4. Verify

- [x] 4.1 `make test-pkg PKG=./internal/mcp/` and `PKG=./internal/profiles/` green
- [x] 4.2 `make pre-pr` green (build/vet/fmt-check/lint-pr/test-cover-gate all clean; `vuln` fails on stdlib-only CVEs, confirmed pre-existing on a clean base-branch tree too — CI runs it `continue-on-error`, per `context/knowledge/gotchas/ci-gates.md`)
- [x] 4.3 Manual smoke — substituted a live daemon call with the raw-JSON-casing assertion added in 1.3/1.4, which exercises the exact same `marshalProfileResolveResult` code path; redeploying the shared dogfood daemon just to re-confirm was judged disproportionate risk for marginal extra confidence (see the "Shared dogfood contention" memory: one live daemon serves many concurrent bug-bashes)

## 5. Ship

- [x] 5.1 `openspec archive add-archetype-native-dispatch` — base specs updated atomically with the PR
- [ ] 5.2 Open PR via `mcp__argus__iris_gh_pr_create` targeting `argus/model-tiering` (this workstream's integration branch, not `master`)

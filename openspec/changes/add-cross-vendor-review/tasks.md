# Tasks — add-cross-vendor-review

**Design doc:** `openspec/changes/add-cross-vendor-review/design.md`

Groups below map 1:1 to the execution sub-DAG nodes. `Depends on` lines are the sub-DAG edges. The spine is **linear** (each node branches off the prior on `argus/model-tiering`) to avoid the branch-stacking fan-in footgun; Groups 2 and 3 are independent and MAY be parallelized if the coordinator accepts the fan-in. Merge target is `argus/model-tiering`; no per-chunk GitHub PR.

## 1. Failing tests (Go prove-it)

- [ ] 1.1 Failing tests for `profile_resolve`: cwd→project→profile resolution, per-spawn override precedence, explicit-name arg, fail-open on missing/invalid, opaque archetype pass-through
- [ ] 1.2 Failing tests for the injected panel-grammar validator: well-formed accept; empty/unknown `finders`; malformed lens; `review_skill`+`review_instruction` conflict; `profiles.Validate` applies the injected validator; no `profiles → review` import
- [ ] 1.3 Failing tests for foreign-reviewer-capture: reviewer-mode sentinel wrap; extraction from a session-log fixture with surrounding noise; missing-closing-sentinel → structured "no review captured"; result addressable by task id
- [ ] 1.4 Confirm every delta scenario has a corresponding failing Go test where Go-testable (skill/harness scenarios are proven in Groups 4/7, not Go CI)

## 2. Panel grammar + profile_resolve (argus-Go)

**Depends on:** Group 1

- [ ] 2.1 Define the `[panel]` grammar type + validator in a new review-owned package; export an injectable `func(panel) []error`
- [ ] 2.2 Wire the injected validator into `profiles.Validate` (mirror the `knownModels` injection; keep `profiles` free of the review import)
- [ ] 2.3 Build `mcp__argus__profile_resolve(cwd, [profile])` in `internal/mcp` as a thin wrapper over `profiles.Loader.ValidateName` + `Project.ResolveProfileName`; resolve daemon-side; honor `task.Profile` > project binding > `default`; return the full body (archetype/rigor/panel) as JSON with opaque archetype pass-through; fail open
- [ ] 2.4 Fill the `[panel]` blocks of `docs/profiles/{default,lean,customer_grade}.toml` (customer_grade = full multi-vendor + lenses + fix_verification; lean = light, no fix_verification; default = middle)
- [ ] 2.5 Make Group-1 tests for 1.1 + 1.2 pass; `make test` green for touched packages

## 3. Foreign-reviewer-capture primitive (argus-Go)

**Depends on:** Group 2

- [ ] 3.1 Add a reviewer-mode flag to the spawn path; wrap the prompt to bracket output between `<<<ARGUS_REVIEW>>>`/`<<<END_ARGUS_REVIEW>>>`
- [ ] 3.2 Implement sentinel extraction over `agent.SessionLogPath(taskID)` (tolerate surrounding terminal/UI noise; handle missing closing sentinel → "no review captured")
- [ ] 3.3 Expose the captured review as a structured result addressable by the reviewer's task (settle the result home — dedicated result field vs `task_meta` key vs artifact — per the design's open question; lean a dedicated field)
- [ ] 3.4 Make Group-1 tests for 1.3 pass; `make test` green for touched packages

## 4. Skills: hera-spawn-review glue + default review instruction (argus `.claude/skills/`)

**Depends on:** Group 3

- [ ] 4.1 `hera-spawn-review` glue skill: call `profile_resolve`; compose the panel; spawn broad finders (Opus/Fable in-session via `Agent`, codex via reviewer-mode spawn + capture) each injected with the configured `review_skill`/`review_instruction` (default `hera-review`); spawn lens finders with their instructions
- [ ] 4.2 Synthesizer step in the glue: normalize→dedup-with-provenance→cross-vendor confidence vote→classify; single-finder→adversarial-verify-or-downgrade; foreign never makes the `[AUTO-FIX]` call
- [ ] 4.3 Fix-verification phase (reasoning-default; optional `iris_run_checks` when `script/iris-check` present) + fix-and-re-review loop (fix-workers follow project conventions, report confidence)
- [ ] 4.4 Default `hera-review` review instruction (encodes ralph's review contract) + shipped lens instruction(s) (e.g. `hera-review-test-adversary`); fallback default panel when no profile resolves
- [ ] 4.5 Manual smoke on a tiny diff to confirm the glue composes + injects correctly

## 5. ralph-review node (implementation quality review)

**Depends on:** Group 4

- [ ] 5.1 Run `/ralph-review` against the change's deltas over the Group 2-4 implementation; auto-fix confident issues, park questions
- [ ] 5.2 Resolve or park findings; keep deltas in sync if behavior shifted

## 6. spec-audit node (delta compliance)

**Depends on:** Group 5

- [ ] 6.1 Run `/spec-audit` (or the coordinator's spec-audit) against the deltas; confirm code matches every requirement/scenario; fix drift
- [ ] 6.2 `make pre-pr` green on the branch

## 7. Validation harness + archive (prove the capability)

**Depends on:** Group 6

- [ ] 7.1 Rebuild the consolidated 54-issue answer key via an in-session judge agent from `review/raw/r1-*-{fable,opus}.md` + sweeps, adjudicating REAL/WAI/uncertain using `fable-crit-review.md` as ground truth
- [ ] 7.2 Run the real `hera-spawn-review` panel on Sherlock PR-45 @ `cdc3a65`; if the live codex leg can't authenticate (daemon lacks `HERA_OPENAI`), degrade to the captured `codex-*.md` reports and flag coord
- [ ] 7.3 Score per-finder + union catch-rate (primary), precision guardrail, unique-catch, cost; produce a per-model side-by-side report (what Opus/Fable/codex each found + the union) for Aaron
- [ ] 7.4 Archive the change in-PR (merge deltas into base specs under `openspec/specs/`, move the change folder to `openspec/changes/archive/<date>-add-cross-vendor-review/`); commit on the branch
- [ ] 7.5 `iris_push` the final branch; report the tip + validation result to coord (who advances `argus/model-tiering`)

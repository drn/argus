# Tasks — add-cross-vendor-review

**Design doc:** `openspec/changes/add-cross-vendor-review/design.md`

**Scope (per design D-SCOPE, owner call 2026-07-05):** ship the panel machinery at the **Fable + Opus default**; reserve the `codex` grammar slot. The `foreign-reviewer-capture` primitive, the codex panel/fix-verification legs, and the live cross-vendor validation are **deferred to a follow-up chunk** (see "Deferred" at the bottom).

Groups below map 1:1 to the execution sub-DAG nodes. `Depends on` lines are the sub-DAG edges.

**Branch routing:** on self-promote, `hera_new_orchestrator` must NOT pass `base_branch=argus/model-tiering` — let it default to **this chunk's own current branch (`argus/2a-xvendor-review`), which carries the committed change folder**. `argus/model-tiering` does not yet include it, so an explicit override would strand Group 1 without its openspec files. Each sub-DAG node branches off the prior on `argus/2a-xvendor-review`. At the end, `iris_push` this branch and report the tip to coord, who advances `argus/model-tiering` from it. **No per-chunk GitHub PR.**

**Stay LINEAR** — the chain is 1→2→4→5→6→7 (Group 3 is deferred). Do NOT parallelize; the fan-in branch-stacking cost (missing-sibling-work footgun) outweighs the modest time savings.

## 1. Failing tests (Go prove-it)

- [x] 1.1 Failing tests for `profile_resolve`: cwd→project→profile resolution, per-spawn override precedence, explicit-name arg, fail-open on missing/invalid, opaque archetype pass-through
- [x] 1.2 Failing tests for the injected panel-grammar validator: well-formed accept (incl. a reserved `codex` finder id); empty/unknown `finders`; malformed lens; `review_skill`+`review_instruction` conflict; `profiles.Validate` applies the injected validator; no `profiles → review` import
- [x] 1.3 Confirm every non-deferred delta scenario has a corresponding failing Go test where Go-testable (skill scenarios are proven by the Group 4 smoke, not Go CI)

## 2. Panel grammar + profile_resolve (argus-Go)

**Depends on:** Group 1

- [x] 2.1 Define the `[panel]` grammar type + validator in a new review-owned package; export an injectable `func(panel) []error`. `finders` ids resolve to a known in-session model OR a configured backend, so `codex` validates as a reserved slot even though it is not composed this chunk
- [x] 2.2 Wire the injected validator into `profiles.Validate` (mirror the `knownModels` injection; keep `profiles` free of the review import)
- [x] 2.3 Build `mcp__argus__profile_resolve(cwd, [profile])` in `internal/mcp` as a thin wrapper over `profiles.Loader.ValidateName` + `Project.ResolveProfileName`; resolve daemon-side; honor `task.Profile` > project binding > `default`; return the full body (archetype/rigor/panel) as JSON with opaque archetype pass-through; fail open
- [x] 2.4 Fill the `[panel]` blocks of `docs/profiles/{default,lean,customer_grade}.toml` with **in-session finders only** (customer_grade = Opus + Fable + lenses + fix_verification; lean = light, no fix_verification; default = middle). Do NOT list `codex` in a shipped profile — the slot is reserved in the grammar, not composed
- [x] 2.5 Make Group-1 tests for 1.1 + 1.2 pass; `make test` green for touched packages

## 3. (DEFERRED) Foreign-reviewer-capture primitive

Moved to the follow-up chunk — see "Deferred" below. The linear chain skips from Group 2 to Group 4.

## 4. Skills: hera-spawn-review glue + default review instruction (argus `.claude/skills/`)

**Depends on:** Group 2

- [x] 4.1 `hera-spawn-review` glue skill: call `profile_resolve`; compose the panel; spawn broad finders (**Opus/Fable in-session via `Agent`**) each injected with the configured `review_skill`/`review_instruction` (default `hera-review`); spawn lens finders with their instructions. A foreign finder id in the panel is skipped-with-a-loud-note this chunk (capture deferred)
- [x] 4.2 Synthesizer step in the glue: normalize→dedup-with-provenance→corroboration confidence vote→classify; single-finder→adversarial-verify-or-downgrade; the synthesizer owns the final `[AUTO-FIX]` call (no finder makes it)
- [x] 4.3 Fix-verification phase (**Opus adversarial reasoning default**; optional `iris_run_checks` when `script/iris-check` present) + fix-and-re-review loop (fix-workers follow project conventions, report confidence)
- [x] 4.4 Default `hera-review` review instruction (encodes ralph's review contract) + shipped lens instruction(s) (e.g. `hera-review-test-adversary`); fallback default panel when no profile resolves
- [x] 4.5 Manual smoke on a tiny diff to confirm the glue composes + injects correctly (this is the shipped validation for this chunk; the Fable+Opus catch-rate is already backed by the prior bake-off) — see `SMOKE.md`. Ran as a 2-of-3-source panel (fable + test-adversary; opus broad finder unavailable in-session, loudly noted); verified D2/D2a corroboration + single-finder adversarial gate + provenance + codex-skip

## 5. ralph-review node (implementation quality review)

**Depends on:** Group 4

- [ ] 5.0 **Gate: `make pre-pr` GREEN on the branch BEFORE review begins.** Run the full CI-mirror gate (build → vet → fmt-check → lint-pr → vuln → test-cover-gate) now, not only at spec-audit — per-group runs piped to `tail` mask exit codes, so surface lint/test gaps here rather than discovering them at the end.
- [ ] 5.1 Run `/ralph-review` against the change's deltas over the Group 2 + 4 implementation; auto-fix confident issues, park questions
- [ ] 5.2 Resolve or park findings; keep deltas in sync if behavior shifted

## 6. spec-audit node (delta compliance)

**Depends on:** Group 5

- [x] 6.1 Run `/spec-audit` (or the coordinator's spec-audit) against the deltas; confirm code matches every requirement/scenario; fix drift — manual requirement-by-requirement audit of both `specs/cross-vendor-review/spec.md` and `specs/diligence-profiles/spec.md` (incl. the 3b-fixup's new "Profile validation CLI affordance" requirement) against `internal/review`, `internal/profiles`, `internal/mcp`, `cmd/argus/validate.go`, and the three `hera-review*` skills. No drift found — every requirement/scenario has corresponding code or skill-prose coverage and a passing test where Go-testable
- [x] 6.2 `make pre-pr` green on the branch — build/vet/fmt-check/lint-pr clean, test-cover-gate 89.6% (floor 88, no PTY flake this run); `vuln` fails only on pre-existing go1.26.3 stdlib CVEs (net/textproto, crypto/x509), advisory per CI's `continue-on-error` and `context/knowledge/gotchas/ci-gates.md`

## 7. Archive + ship (on-branch, no GitHub PR)

**Depends on:** Group 6

- [ ] 7.1 Archive the change **ON-BRANCH (no GitHub PR)**: merge deltas into base specs under `openspec/specs/`, move the change folder to `openspec/changes/archive/<date>-add-cross-vendor-review/`, and commit directly on `argus/2a-xvendor-review` (merge applies work immediately, so base-spec update lands atomically with the branch — never a post-merge step)
- [ ] 7.2 `iris_push` the final branch; report the tip + a plain-language summary to coord (who advances `argus/model-tiering`)

## Deferred to follow-up chunk

Bundle with the codex-auth `HERA_OPENAI` secret-sourcing fix (a live codex leg needs it). Design-of-record: D3, D4 (codex leg), D5/D5-findings. Formal `foreign-reviewer-capture` delta requirements live in git history (removed from this change).

- **Foreign-reviewer-capture primitive (argus-Go):** reviewer-mode spawn flag + `<<<ARGUS_REVIEW>>>`/`<<<END_ARGUS_REVIEW>>>` prompt wrap; sentinel extraction over `agent.SessionLogPath(taskID)` (tolerate UI noise, missing-closing-sentinel → "no review captured"); structured result addressable by the reviewer's task.
- **Wire the codex leg** into `hera-spawn-review` (broad finder + fix-verification adversarial pass) and add `codex` to the `customer_grade` profile's `finders`.
- **Live vendor-neutral cross-vendor validation:** build a VENDOR-NEUTRAL answer key by pooling every vendor's findings (Opus, Fable, codex, +future) and adjudicating each REAL/WAI/uncertain from code evidence; run the real panel on Sherlock PR-45 @ `cdc3a65` with every reviewer on the identical packet under one brief; score per-finder + union catch-rate, precision, unique-catch (provenance), cost; degrade to captured `codex-*.md` reports if the live codex leg can't authenticate.

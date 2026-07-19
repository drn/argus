---
name: hera-spawn-review
description: >-
  Orchestrate a vendor-diverse review panel over a diff: resolve the project's diligence-profile
  [panel] via mcp__argus__profile_resolve, spawn each broad finder and corrective lens as an
  in-session Claude sub-agent injected with its configured review instruction, synthesize every
  finder's output into the canonical finding schema with cross-vendor confidence voting, run
  fix-verification, then fix-and-re-review until clean. Use when the user wants a panel / multi-
  reviewer pass over a diff beyond a single reviewer loop — reachable from any argus project, not
  just OpenSpec ones. This chunk ships Fable+Opus in-session finders only: a foreign finder id
  (e.g. codex) is a reserved grammar slot the glue SKIPS with a loud note — the
  foreign-reviewer-capture primitive it needs is deferred (see D-SCOPE,
  openspec/changes/add-cross-vendor-review/design.md). NOT ralph-review (single reviewer,
  OpenSpec-only, no panel, no vendor diversity) — this is its hera-aware sibling; ralph stays
  untouched for solo use.
---

# hera-spawn-review — panel orchestration glue

## 1. What this is, and is not

This skill is the **glue only**: resolve the panel, spawn finders, inject their review
instruction, synthesize, fix, verify, re-review. It does not itself define "what good review
looks like" — that methodology lives in a separate, user-owned skill named by the panel's
`review_skill` (default `hera-review`) and each lens's `skill` (e.g.
`hera-review-test-adversary`). Swapping the review instruction in a profile's `[panel]` never
requires editing this skill.

**Scope this chunk (D-SCOPE, owner call 2026-07-05):** the panel runs Fable + Opus as in-session
finders. `codex` (or any other backend id) is a **reserved, valid** grammar slot — it validates,
but the `foreign-reviewer-capture` primitive it needs to actually run doesn't exist yet. When a
panel names one, **skip it with a loud, visible note** and continue with the remaining finders.
Do not build sentinel capture, reviewer-mode spawning, or anything that tries to run a foreign
backend as part of following this skill — that's a separate, deferred chunk.

## 2. Resolve the panel

Call `mcp__argus__profile_resolve(cwd=$PWD)`. It returns (per `internal/mcp/profiles.go`):

```json
{"resolved": true|false, "name": "...", "source": "...",
 "archetype": {...}, "rigor": {...}, "panel": {...}, "errors": [...]}
```

- **`resolved: false`** (fail-open — no profile, an invalid profile, or a malformed `[panel]`):
  log it loudly, e.g. `[hera-spawn-review] profile_resolve: no profile resolved (errors:
  <errors>) — falling back to the built-in default panel.`, and use the **built-in default
  panel**, which mirrors the shipped `default` profile's own `[panel]` block exactly (so behavior
  doesn't diverge depending on whether resolution happened to succeed):

  ```
  finders          = ["opus", "fable"]
  lens             = []                # none
  review_skill     = "hera-review"     # (the default anyway when unset)
  synthesizer      = "opus"            # (the default anyway when unset)
  fix_verification = false
  ```

- **`resolved: true`**: read `panel` (the opaque grammar from `internal/review/panel.go`, D7 in
  the design doc). It already passed the injected validator at profile-load time — don't
  re-validate, just read fields defensively (treat an absent or empty `finders` as "use the
  built-in default panel" too):
  - `finders` — list of reviewer ids.
  - `lens` — list of `{name, model, skill}` (may be absent/empty).
  - `review_skill` / `review_instruction` — at most one is set; if neither is, default to
    `review_skill = "hera-review"`.
  - `synthesizer` — the model that owns the final `[AUTO-FIX]` call; default `"opus"` if unset.
  - `fix_verification` — bool; default `false` if unset (matches the shipped `default` profile;
    `customer_grade` turns it on explicitly).

## 3. Determine the diff scope and gather the packet

```
1. Caller named an explicit scope (branch, SHA, commit range) → use it.
2. Unstaged or staged changes exist → diff scope = working tree.
3. Current branch != the repo's default branch → diff scope = branch vs default.
4. Otherwise → diff scope = local vs origin.
```

Gather once: the full diff, the changed-files list, and the full contents of every changed file
(plus files that import/reference them, via Grep). **Every finder and lens must see the identical
packet** — scope asymmetry between reviewers invalidates any comparison between what they find
(this is the same "all reviewers see the same packet" discipline the design's validation harness
treats as load-bearing, D5). Building the packet once here, rather than trusting each spawned
sub-agent to independently re-gather it, is what guarantees that.

## 4. Load the review instruction(s)

- **Broad finders' instruction**: if `review_skill` is set, Read
  `.claude/skills/<review_skill>/SKILL.md` from the repo root and use its body verbatim. If the
  file doesn't exist, **stop and fail loudly** — "review_skill %q not found" — do not silently
  fall back (per D7: "a missing skill fails loudly at spawn"). If `review_instruction` prose is
  set instead, use that prose verbatim. If neither is set, Read
  `.claude/skills/hera-review/SKILL.md` (the shipped default).
- **Each lens's instruction**: for every `[[panel.lens]]` entry, Read
  `.claude/skills/<lens.skill>/SKILL.md`. Same loud-failure rule on a missing file.

## 5. Spawn broad finders

For each id in `finders`:

- **Known in-session model** (`opus`, `fable`, `sonnet`, `haiku` — mirrors
  `internal/review.knownInSessionModels`): spawn via the `Agent` tool with `model = id`, full tool
  access (Read/Grep/Bash — the finder needs to trace dependent files), and a prompt built from: the
  loaded review instruction body + the diff packet from step 3 + a closing instruction to output
  *only* the tagged findings report (no other prose). Each broad finder reviews the **full diff**
  — never narrow one to a single file or lane (D2: the diversity comes from independent full
  passes, not lane-splitting).

  **Fable pin (D7 gotcha) — when `id == "fable"`:** Fable is reliable *only* at low effort; at
  medium/high it silently falls back to Opus with no error, which would make the panel believe it
  ran two independent models when it actually ran Opus twice. If the `Agent` tool in this
  environment exposes an effort/reasoning-tier parameter, set it to `low` explicitly. As of this
  chunk it does not (check the tool's schema before assuming otherwise), so instead add an
  explicit line to the Fable finder's prompt directing it to answer directly without extended
  deliberation, and **record this as a known gap in the smoke/run report** rather than claiming
  the pin is enforced — don't let the report imply a guarantee the tooling can't back up yet.

- **Foreign/backend id** (resolves to a configured backend such as `codex`, not a known in-session
  model): **do not spawn it.** Emit a loud, visible note, e.g.:

  ```
  [hera-spawn-review] SKIPPED foreign finder "codex" — foreign-reviewer-capture is deferred
  (D-SCOPE, openspec/changes/add-cross-vendor-review/design.md D3). This run has one fewer
  finder than the panel configured.
  ```

  This note must appear both in the live output and in the final report (step 11) — never drop it
  silently. Continue with the remaining finders.

## 6. Spawn lens finders

For each `[[panel.lens]]` entry, spawn via `Agent` with `model = lens.model` and the loaded lens
instruction + the same diff packet + the same closing instruction. Lenses are **always**
in-session (per D2/the spec: lens finders never run via a foreign backend) — there is no
foreign-lens case to skip.

## 7. Collect outputs with provenance

Each `Agent` call returns its final text as a string. Before handing anything to the synthesizer,
label each block with its source (`finder: "opus"`, `finder: "fable"`, `lens: "test-adversary"`,
...). Provenance travels with the raw text from here on — it is not something the synthesizer
should have to infer.

## 8. Synthesize (the safety contract — D2, D2a)

Spawn exactly one more `Agent` call, `model = synthesizer` (default `opus`), and give it every
finder/lens output block (still labeled with provenance) plus this charge:

1. **Normalize** every finding from every source into the canonical schema (`[AUTO-FIX]` /
   `[QUESTION]` / `[SPEC-DRIFT]` / `[ACKNOWLEDGED]` / `[SKIP]`, as defined in `hera-review`) — do
   not trust a finder's own tag; re-derive it from the finding's substance.
2. **Deduplicate** findings describing the same underlying issue (same file/region, same root
   cause) into one finding that records *every* source that reported it. Never collapse away
   provenance in the process.
3. **Confidence by corroboration**: a finding reported by ≥2 independent sources is corroborated.
   Exactly one source reporting it makes it single-finder.
4. **Single-finder adversarial gate (D2a) — mandatory before `[AUTO-FIX]`:** for every
   single-finder finding, argue the other side — "is this real, or is the reporting source wrong?
   Default to NOT REAL under uncertainty." If it survives its own adversarial challenge, it's
   eligible for `[AUTO-FIX]`. If refuted, or you remain uncertain, **downgrade it to `[QUESTION]`**
   — surfaced, never silently dropped.
5. **You own the final `[AUTO-FIX]` call — no finder makes it, including via its own tag.** A
   corroborated, low-risk finding may go straight to `[AUTO-FIX]`; use judgment on
   corroborated-but-high-risk findings (a sanity check before committing, even without the full
   adversarial pass single-finder findings require).
6. Output the synthesized findings list in `hera-review`'s output format, with two additions per
   entry: a `Provenance:` line (which source(s) reported it) and, for single-finder findings, a
   one-line note on how the adversarial challenge went.

## 9. Fix-verification phase (D4)

Distinct from per-area review — asks "does the fix actually work in the *shipped artifact*?", not
"is this code correct in isolation." Run once per round, after fixes land (step 10) and before
re-review:

- **Default: adversarial artifact reasoning, no build.** Spawn an `Agent` (`model="opus"`) to read
  the project's deploy surfaces (Dockerfile/entrypoint if present, build scripts, config
  precedence, CI config, the test suite meant to catch regressions) and adversarially trace
  "build → running artifact — is each accepted fix actually present and functional in what would
  ship, not just in the source diff?" This is the exact shape of check that caught the
  highest-stakes PR-45 bug (a build-stub config that shipped into the image) when ordinary code
  review missed it entirely — see the design doc's Context section.
- **Optional escalation:** if the project provides a `script/iris-check` (check for it), call
  `mcp__argus__iris_run_checks` to actually exercise the shipped path rather than only reasoning
  about it.
- A fix-verification failure is a new finding — feed it back into the synthesized list
  (`[AUTO-FIX]` if the failure mode is immediately clear, else `[QUESTION]`). Never discard it
  silently.

## 10. Fix-and-re-review loop

For every synthesized `[AUTO-FIX]` finding:

1. Spawn a fix-worker per finding (or a small batch of same-file findings) via `Agent`. Instruct
   it to: read the target project's own conventions first (its `CLAUDE.md`, an `openspec/` dir if
   present → spec-first, the project's existing test framework → write/update tests before or
   alongside the fix per that project's own TDD stance); implement the fix; run the relevant test
   package; report a confidence signal (`HIGH`/`MEDIUM`/`LOW` + one-line reasoning).
2. After all of this round's accepted fixes land, run fix-verification (step 9).
3. **Re-review**: re-run steps 5-8 (spawn finders + lenses → synthesize) against the updated diff.
4. **Terminate** when a synthesis round yields zero `[AUTO-FIX]` findings — that's the spec's
   "re-review until no auto-fixable findings remain." As a cost/safety bound (mirroring
   ralph-review's loop control, which this instruction's contract is explicitly built on), **cap
   at 3 rounds**; if fixes were still landing when the cap hits, flag "may need another pass"
   rather than looping unbounded.
5. `[QUESTION]`, `[SPEC-DRIFT]`, `[SKIP]`, `[ACKNOWLEDGED]` findings are never auto-applied — they
   accumulate across rounds into the final report for the human/coordinator to act on.

## 11. Report

Always produce a report, even on a clean first pass ("no auto-fixable issues found" is itself a
result worth stating, not a reason to exit silently). Include per round: which finders/lenses ran,
any foreign ids skipped (with the loud note from step 5), findings by tag, fixes applied with
their confidence signal, the fix-verification result, and the accumulated `[QUESTION]` /
`[SPEC-DRIFT]` lists still open at the end.

## 12. Gotchas

- **Fable:low is a documented mitigation, not an enforced guarantee** given the `Agent` tool's
  current schema (no effort parameter) — don't let a report imply otherwise; note it every time a
  Fable finder runs.
- **A missing `review_skill`/lens `skill` file fails loudly, not silently** — the panel grammar
  doesn't validate skill *existence* at profile-load time (only that at most one of
  `review_skill`/`review_instruction` is set), so this glue is the actual enforcement point.
- **Provenance must survive dedup.** A finding that loses its source list on the way through the
  synthesizer breaks the whole point of running a panel instead of one reviewer.
- **The synthesizer owns `[AUTO-FIX]`, never a finder** — even a finder that confidently tags its
  own finding `[AUTO-FIX]` only produced a recommendation; re-derive the tag in step 8.
- **A skipped foreign finder is a loud note, never a silent gap** — the whole point of D-SCOPE is
  that this chunk's panel is smaller than the grammar allows; hiding that would misrepresent what
  actually ran.

## 13. Verifying this skill (smoke test)

Before trusting this on real work, run it on a tiny, contained diff (a few lines, one file):
confirm `profile_resolve` either resolves a real panel or falls back cleanly and loudly; confirm
both broad finders spawn with the correct injected instruction (spot-check the actual prompt
text, not just that the call succeeded); confirm a foreign id in a test panel is skipped with the
loud note; confirm the synthesizer's output carries provenance and correctly gates any
single-finder finding; and, if the diff has an obvious injected bug, confirm it's actually caught.
This composition smoke is the shipped validation for this chunk — the Fable+Opus catch-rate itself
is already backed by the prior PR-45 bake-off (design doc D9), not re-proven here.

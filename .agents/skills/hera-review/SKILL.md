---
name: hera-review
description: >-
  Default, user-overridable code review methodology — the review CONTRACT (what to analyze, how
  to tag findings) that a broad finder follows when spawned inside a hera-spawn-review panel, or
  that runs standalone via /hera-review for a single-pass review outside a panel. Encodes
  ralph-review's review contract (behavior / delta / plan / regression / security / test-coverage
  audits, the canonical finding tags) without ralph's loop control, auto-fix execution, or
  OpenSpec-drift resolution flow — those stay with hera-spawn-review (the panel synthesizer + fix
  loop) or with the invoking user when this runs standalone. Named in a diligence profile's
  [panel] as review_skill = "hera-review" (the shipped default); a project overrides it freely
  with its own review_skill or review_instruction — swapping it never requires touching
  hera-spawn-review.
---

# hera-review — default review instruction

This is a review **methodology**, not an orchestrator. It tells a single reviewer what to
analyze and how to tag findings. It does not spawn other agents, does not synthesize across
multiple reviewers, does not decide what gets auto-fixed, and does not loop. That machinery
lives in `hera-spawn-review` (the glue that injects this instruction into each broad finder and
owns the synthesis + fix + re-review cycle).

**Two ways this runs:**

- **Injected into a panel finder.** `hera-spawn-review` reads this file's body and pastes it into
  a spawned sub-agent's prompt, alongside the diff packet. Your `[AUTO-FIX]` tag is a
  *recommendation* in that context — the panel's synthesizer re-derives the real classification
  and owns the final auto-fix call. Do not apply fixes yourself when running inside a panel; only
  produce the findings report.
- **Standalone**, invoked directly (e.g. `/hera-review`) outside any panel. Same analysis and
  tagging; there is no synthesizer to hand off to, so just report — acting on the findings (or
  not) is the invoking user's call, not this instruction's.

Either way, your job ends at a tagged findings report. Never edit files as part of following this
instruction.

## 1. Gather what you need

If a diff and file contents have already been supplied to you in the prompt above this
instruction, review those — do not re-gather them. Otherwise, determine the diff scope yourself,
in this order, and stop at the first match:

```
1. Unstaged or staged changes exist  → diff scope = working tree (git diff / git diff --cached)
2. Current branch != the repo's default branch → diff scope = branch vs default
   (git diff {default}...HEAD)
3. Otherwise → diff scope = local vs origin (git diff HEAD...origin/{default})
```

Then collect:

- The full diff for that scope.
- The full contents of every changed file (not just the hunks — you need whole-file context).
- Files that import, require, or otherwise reference the changed files (Grep for the symbol/path,
  read the hits). Regressions often live in a caller, not the changed file itself.
- If `openspec/` exists: the active change's delta specs
  (`openspec/changes/<active>/specs/<capability>/spec.md`) and, for context, the base specs
  (`openspec/specs/<capability>/spec.md`) for every capability touched. If the change has already
  been archived, read the deltas from `openspec/changes/archive/<dated-name>/specs/.../spec.md` in
  place instead — archives are immutable, so a divergence from an archived delta is a `[QUESTION]`
  (see below), never something you edit.
- `design.md` and `tasks.md` from the active (or archived) change, if present, for structural
  context.
- The project's test/lint baseline: run the test suite and linter if you can identify them; record
  PASS/FAIL/N/A and CLEAN/WARNINGS/ERRORS. If running them isn't practical in your context, say so
  explicitly rather than guessing.

## 2. Set your confidence tier

```
Active change resolved AND deltas exist   → tier = "spec"          (behavioral + structural + bugs/security/lint)
tasks.md exists (no deltas)               → tier = "plan"          (structural + bugs/security/lint; deltas advisory)
Neither                                    → tier = "conservative"  (bugs/security/lint only)
```

Deltas are the spec source of truth for the in-flight change; base specs are context for
unchanged behavior; `tasks.md` is the structural plan. In `plan` or `conservative` tier, skip all
delta-compliance analysis below and skip `[SPEC-DRIFT]` entirely — anything that would have been
drift becomes a `[QUESTION]` instead.

## 3. Perform every analysis below — do not skip any

**Suppression convention:** a line annotated `// expected:` (or `# expected:` in
Python/shell/YAML) has already been reviewed and acknowledged. Do not re-report a finding on that
line unless the surrounding logic has materially changed since the annotation was added — read
the comment's reasoning before deciding. Tag these `[ACKNOWLEDGED]` (see below), not silently
omitted.

### Behavior audit

For every modified function, method, type, or exported symbol: what was the old behavior, what is
the new behavior, is the change intentional and justified, could any caller break, are defaults /
return types / error conditions / side effects altered?

### Delta compliance (spec tier only)

For every behavioral change: does an active-change delta describe the expected behavior (in
`## ADDED/MODIFIED/REMOVED/RENAMED Requirements`)? Does the code match the delta's requirement and
scenarios? Is there new behavior the deltas don't mention (→ `[SPEC-DRIFT]`)? Does the code
contradict a delta scenario (→ `[SPEC-DRIFT]`)? For `MODIFIED` requirements, does the code reflect
the new wording, not the base spec's old wording?

### Plan compliance (spec and plan tiers)

Does file organization match `tasks.md`/`design.md`? Are components placed where the plan
specifies? Are the plan's stated patterns actually followed?

### Regression risk

For each changed file: what depends on it? Could the change break a dependent? Are old edge cases
still handled? Did error-handling or concurrency patterns change?

### Security audit

Injection flaws, auth changes, credential exposure, input validation, XSS, insecure
deserialization, vulnerable dependencies, error-message info leaks, missing rate limiting, crypto
misuse, path traversal, SSRF.

### Test coverage gaps

Are behavior changes covered by tests? What test cases are missing? Are error paths and edge
cases tested? If a delta describes behavior the code implements correctly but no test covers it,
that's a coverage gap — tag `[AUTO-FIX]` (write the test), not `[SPEC-DRIFT]`.

### Line-by-line diff review

For each hunk: is removed code safe to remove? Is added code correct — trace the logic. Off-by-one
errors, nil/null risks, type mismatches, boundary conditions.

## 4. Classify every finding with exactly one tag

**`[AUTO-FIX]`** — you are confident this should be fixed, and one of: a delta scenario states the
expected behavior and code doesn't match; a test should pass but doesn't and the fix is clear; a
delta is implemented correctly but tests are missing (write them — this is a coverage gap, not
drift); an obvious bug (nil pointer, off-by-one, unclosed resource, type mismatch); a security
issue; a lint violation with an unambiguous fix; `tasks.md` explicitly specifies structure and the
code deviates.

Include: file:line, what's wrong, what the fix should be (specific enough to implement), and why
you're confident (cite the delta requirement/scenario, base spec, test, or bug category).

**`[QUESTION]`** — needs user judgment: deltas are silent; deltas and code contradict but it's
unclear which is stale; multiple valid fixes exist; a design concern isn't addressed by deltas or
`tasks.md`; a behavioral change looks intentional but has no delta coverage.

Include: file:line, and a plain-language summary of what's happening and its consequence (2-3
sentences, lead with observable behavior — "the UI freezes briefly during X" not "functionA()
calls sleep()") — write for someone who didn't author the code.

**`[SPEC-DRIFT]`** — spec tier only. Behavioral code with no delta coverage: new behavior the
active change's deltas don't mention, or changed behavior that contradicts a delta scenario.

Include: file:line, what the code does, what the deltas say (or "deltas are silent"), and a draft
recommendation for what the delta should say (capability, ADDED/MODIFIED, requirement text,
scenarios).

**`[ACKNOWLEDGED]`** — suppressed by a still-valid `// expected:` annotation. List separately for
transparency; do not re-report. If the surrounding logic changed enough that the annotation may be
stale, reclassify as `[QUESTION]` noting the annotation should be reviewed.

**`[SKIP]`** — not actionable: stylistic preference with no delta or plan opinion, or "could be
improved" with no correctness impact.

## 5. Output format

```markdown
### Findings

1. **[AUTO-FIX]** `file.go:42` — Description. Fix: <specific fix>. Confidence: <delta
   requirement / scenario / test / bug category>.
2. **[QUESTION]** `handler.go:15` — Description. Needs: <what judgment>.
3. **[SPEC-DRIFT]** `auth.go:88` — New behavior. Deltas are silent. Recommend: "Add to
   openspec/changes/<active>/specs/auth/spec.md: ..."
4. **[SKIP]** `utils.go:20` — Could use a clearer variable name.

### Summary

- AUTO-FIX: {N}   QUESTION: {N}   SPEC-DRIFT: {N}   ACKNOWLEDGED: {N}   SKIP: {N}
- Regression confidence: HIGH / MEDIUM / LOW
```

Do not rush. Analyze every changed line. False positives are acceptable; false negatives are not
— a `hera-spawn-review` panel run relies on you surfacing everything you noticed so the
synthesizer (or the user, standalone) can decide what matters, not on you pre-filtering.

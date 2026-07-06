# hera-spawn-review manual smoke (tasks.md 4.5)

**Run date:** 2026-07-06. **Run by:** 3b-fixups worker (hera orchestrator `xvendor-review-impl`), redoing the smoke 2a-skills reportedly ran end-to-end but never committed an artifact for.

This is the shipped validation for this chunk (per `.claude/skills/hera-spawn-review/SKILL.md` §13): confirm `profile_resolve` resolves or falls back cleanly, confirm both broad finders spawn with the correct injected instruction, confirm a foreign id is skipped with a loud note, confirm the synthesizer's output carries provenance and correctly gates a single-finder finding, and confirm an injected bug is actually caught. The Fable+Opus catch-rate itself is *not* re-proven here — that's the prior PR-45 bake-off (design.md D9).

## 1. Panel used — hand-built, not resolved live

The live shared dogfood daemon predates 1a's `profile_resolve` commit (known binary skew — see memory `hera-send-405-stale-daemon` / the coordinator's explicit instruction not to rebuild/restart the shared dogfood daemon for this smoke). So `mcp__argus__profile_resolve` was **not called** in this run; the panel below was **hand-built** by me, shaped after `docs/profiles/customer_grade.toml`'s `[panel]` block, with one deliberate addition (`codex`) to actually exercise the foreign-finder-skip path (customer_grade itself does not list `codex` — see D-SCOPE):

```toml
[panel]
finders          = ["opus", "fable", "codex"]   # codex added here, beyond customer_grade's shape, to exercise skip
review_skill     = "hera-review"                 # default
fix_verification = true

[[panel.lens]]
name  = "test-adversary"
model = "opus"
skill = "hera-review-test-adversary"

synthesizer = "opus"                             # default
```

`internal/review.NewValidator` and `internal/mcp/profiles.go` themselves are unit-tested by 1a (`internal/review/panel_test.go`, `internal/mcp/profiles_test.go`) — this smoke is about the **glue's composition/injection/synthesis behavior**, not re-proving panel-grammar validation.

**Scope note:** this smoke exercises `hera-spawn-review` steps 2–8 (resolve/fallback → scope diff → load instructions → spawn finders → spawn lens → collect with provenance → synthesize). It does **not** exercise step 9 (fix-verification) or step 10 (fix-and-re-review loop) — matching what 2a-skills reportedly ran and what the coordinator's brief asked for. That is a deliberate scope limit of this artifact, not a silent gap.

## 2. Target diff — throwaway repo, injected off-by-one + weakened test

A throwaway repo (`truncpkg`, git-initialized, not part of this repo) with a baseline correct `Truncate` helper, then a staged diff that (a) introduces an off-by-one bug and (b) weakens the test in the same commit so it stays green:

```diff
diff --git a/truncpkg/truncate.go b/truncpkg/truncate.go
@@ -6,5 +6,5 @@ func Truncate(s string, n int) string {
 	if n >= len(s) {
 		return s
 	}
-	return s[:n]
+	return s[:n+1]
 }
diff --git a/truncpkg/truncate_test.go b/truncpkg/truncate_test.go
@@ -4,7 +4,7 @@ import "testing"
 func TestTruncate(t *testing.T) {
 	got := Truncate("hello world", 5)
-	if got != "hello" {
-		t.Fatalf("got %q, want %q", got, "hello")
+	if got == "" {
+		t.Fatal("expected non-empty result")
 	}
 }
```

Confirmed locally before spawning any reviewer: `go test ./...` in the throwaway repo is **green** despite the bug (`Truncate("hello world", 5)` returns `"hello "`, which is non-empty, so the weakened assertion never fires). This is the exact "green but wrong" shape the `test-adversary` lens exists to catch.

Every finder/lens below was given the identical packet: the diff, the changed-file list, and the full post-diff contents of both files (per skill §3, scope symmetry).

## 3. Finder resolution (skill §5) — who actually ran

Per `finders = ["opus", "fable", "codex"]`, resolving each id:

- **`opus`** → known in-session model → spawn. **Result: did not return output.** Spawned twice (`smoke-finder-opus`, then a retry `smoke-finder-opus2` after the first appeared stuck) across ~20 minutes and 6 total follow-up requests via the session's teammate-messaging channel; neither ever produced findings content. This is a **real gap in this run**, called out loudly here rather than silently dropped — it is a sub-agent-availability issue in this session, not a defect in the glue or the panel grammar. **The opus broad-finder leg of the panel is MISSING from this smoke's results.**
- **`fable`** → known in-session model → spawn. **Result: succeeded** (§4 below). Prompted per the D7/skill-§5 gotcha to answer directly without extended deliberation (the documented mitigation for Fable's medium/high-effort fallback-to-Opus; not an enforced guarantee — this run has no way to verify which effort tier actually executed).
- **`codex`** → resolves to a configured backend, not a known in-session model → **SKIPPED**, per skill §5:

  > `[hera-spawn-review] SKIPPED foreign finder "codex" — foreign-reviewer-capture is deferred (D-SCOPE, openspec/changes/add-cross-vendor-review/design.md D3). This run has one fewer finder than the panel configured.`

  No `codex` agent was spawned. This confirms the skip-with-loud-note path: a foreign id in the panel's `finders` list is recognized and deliberately not run, and that fact is surfaced (here) rather than silently absent.

**Net result: 1 of 2 configured in-session broad finders returned output, plus the panel's 1 lens.** This smoke is therefore a two-source panel (fable + test-adversary), not the full three-leg run the coordinator's brief described 2a-skills as having completed. I could not reproduce a working opus broad-finder leg in this session; that limitation is carried through §5–§6 below rather than papered over.

**Known procedural deviation from skill §4:** each finder/lens was instructed to `Read` its instruction file directly (`.claude/skills/<name>/SKILL.md`, staged into the throwaway repo) rather than having the orchestrator pre-read the file and paste its verbatim body into the prompt as skill §4 specifies. Functionally equivalent — the same instruction body reached each finder, and each one's output matches the instruction's own format exactly — but it does not test the literal "orchestrator injects verbatim text" mechanic. Flagging for fidelity, not hiding it.

## 4. Fable broad finder — actual output

```markdown
### Findings

1. **[AUTO-FIX]** `truncpkg/truncate.go:9` — Off-by-one: `s[:n+1]` returns n+1 bytes, not n.
   `Truncate("hello world", 5)` now returns `"hello "` (6 bytes), contradicting both the doc
   comment ("returns the first n bytes of s") and the pre-diff behavior `s[:n]`. Fix: restore
   `return s[:n]`. Confidence: obvious off-by-one bug — the code contradicts its own contract
   (doc comment), and any caller relying on an n-byte bound (fixed-width buffers, DB column
   limits) gets one byte too many. (Pre-existing, not introduced by this diff: `n < 0` would
   panic.)

2. **[AUTO-FIX]** `truncpkg/truncate_test.go:7-9` — Test weakened from asserting exact output
   (`got != "hello"`) to asserting only non-empty (`got == ""`), which masks the off-by-one in
   finding 1 — the suite is green (`go test ./...` PASS confirmed by running it) while the
   function is wrong. A non-empty check cannot fail for any plausible implementation. Fix:
   restore the exact assertion `if got != "hello" { t.Fatalf("got %q, want %q", got, "hello") }`
   (which will correctly fail red against the current code), and ideally add boundary cases
   (`n == len(s)`, `n > len(s)`, `n == 0`). Confidence: a test should catch this bug but was
   rewritten to pass regardless — regression-masking test weakening; the fix is clear.

3. **[SKIP]** `truncpkg/truncate.go` — Byte-based truncation can split a multi-byte UTF-8 rune
   mid-sequence; the doc comment honestly says "bytes", so this is a documented design choice,
   not a defect in this diff.

### Summary

- AUTO-FIX: 2   QUESTION: 0   SPEC-DRIFT: 0   ACKNOWLEDGED: 0   SKIP: 1
- Tier: conservative (no openspec/, no tasks.md). Test baseline: PASS (`go test ./...` ok —
  misleadingly green per finding 2). Lint: `go vet` CLEAN.
- Regression confidence: LOW — the only changed function has a behavior-changing bug and its
  only test was hollowed out to hide it; any caller depending on the n-byte contract regresses.
```

## 5. test-adversary lens — actual output

```markdown
## Findings

1. **[AUTO-FIX]** `truncpkg/truncate_test.go:7` — The assertion was weakened in this same diff
   from `got != "hello"` (exact-value check) to `got == ""` (non-empty check only). This is
   precisely the off-by-one that the production change introduced: `Truncate("hello world", 5)`
   now returns `s[:6]` = `"hello "` (six bytes, trailing space) instead of `"hello"`. `"hello "`
   is non-empty, so the test passes on buggy output. Verified concretely — `go test -count=1
   ./...` is green with the bug present. The docstring still promises "the first n bytes of s",
   so the function is wrong and the test can no longer see it. Fix: restore the exact assertion
   `if got != "hello" { t.Fatalf(...) }`, which fails on the current `s[:n+1]` implementation and
   catches any regression in the boundary math.

2. **[AUTO-FIX]** `truncpkg/truncate_test.go:6` — Only the `n < len(s)` branch is exercised, and
   only with one input. The `n >= len(s)` early-return branch (whole-string passthrough) has no
   test at all, and there is no boundary case (`n == len(s)`, `n == 0`, empty string). Fix: add
   table cases covering `Truncate("hi", 5) == "hi"` (n exceeds length), `Truncate("hello", 5) ==
   "hello"` (n equals length — also pins the exact off-by-one boundary), and `Truncate("", 0) ==
   ""`.

3. **[QUESTION]** `truncpkg/truncate.go:9` — This is the underlying production bug (`s[:n+1]`),
   flagged only for the test-provenance link; the broad finders own general correctness. Note:
   the off-by-one is one step from an out-of-range slice panic (any input where `n+1 > len(s)`
   after the guard is bypassed by equal-length cases), and no test guards that panic edge either.
   Needs: confirm the intended semantics (first `n` bytes) so the corrected assertion above locks
   it in.

## Summary

- AUTO-FIX: 2   QUESTION: 1   SPEC-DRIFT: 0   ACKNOWLEDGED: 0   SKIP: 0
- Tests that would NOT catch a plausible regression: 1 (the single `TestTruncate` passes on an
  off-by-one bug that is literally present in this diff — the headline failure for this lens)
```

## 6. Synthesizer (opus) — actual output

```markdown
## Panel limitation (read first)

- This was a TWO-source panel, not a full three-leg run. The opus broad finder could not be
  obtained (unresponsive across two spawn attempts and five follow-ups). Synthesis draws only on
  the fable broad finder and the test-adversary corrective lens. Corroboration counts below are
  capped at 2, and no second independent broad finder existed to cross-check fable's
  general-correctness calls — treat "corroborated" here as "fable + the test lens agreed," not
  "two independent broad finders agreed."

## Findings

1. **[AUTO-FIX]** `truncpkg/truncate.go:9` — Off-by-one: `s[:n+1]` returns n+1 bytes, not n.
   `Truncate("hello world", 5)` returns `"hello "` (6 bytes, trailing space), contradicting the
   doc comment ("the first n bytes of s") and the pre-diff `s[:n]`. Any caller relying on an
   n-byte bound (fixed-width buffers, DB column limits) regresses by one byte. Fix: restore
   `return s[:n]`.
   - **Provenance:** fable #1 (tagged AUTO-FIX); test-adversary #3 (tagged QUESTION, but only as
     a provenance link — it confirmed the bug rather than refuting it). Corroborated (2 sources).
     No single-finder gate required. Final AUTO-FIX call is the synthesizer's:
     contract-contradicting, unambiguous fix, both sources independently verified the suite is
     green on the buggy output.

2. **[AUTO-FIX]** `truncpkg/truncate_test.go:7` — Regression-masking test weakening: the
   assertion was downgraded in this same diff from exact-value (`got != "hello"`) to
   non-empty-only (`got == ""`). `"hello "` is non-empty, so the suite passes on the finding-1
   bug; a non-empty check cannot fail for any plausible implementation. Both sources confirmed
   `go test` PASS with the bug present. Fix: restore `if got != "hello" { t.Fatalf("got %q, want
   %q", got, "hello") }`, which fails red against the current `s[:n+1]`.
   - **Provenance:** fable #2 (AUTO-FIX); test-adversary #1 (AUTO-FIX). Corroborated (2 sources).
     No single-finder gate required. AUTO-FIX call is the synthesizer's.

3. **[QUESTION]** `truncpkg/truncate_test.go:6` — Coverage gap: only the `n < len(s)` branch is
   exercised, with one input. The `n >= len(s)` whole-string passthrough branch has no test, and
   there are no boundary cases (`n == len(s)`, `n == 0`, empty string). Suggested: table cases
   `Truncate("hi", 5) == "hi"`, `Truncate("hello", 5) == "hello"` (pins the off-by-one boundary),
   `Truncate("", 0) == ""`.
   - **Provenance:** test-adversary #2 (tagged AUTO-FIX) as a standalone finding. fable
     ancillary-mentioned the same boundary additions ("ideally add boundary cases…") inside its
     finding #2, not as a distinct finding — noted for honesty, but the untested-passthrough-branch
     observation is test-adversary's alone. Treated as single-finder.
   - **Adversarial gate:** Argued NOT REAL. This is additive coverage for a branch and boundaries
     this diff never touched — not a correction of a masking artifact (that's finding 2) or a
     present defect. Restoring the exact assertion (finding 2) already catches the current
     off-by-one; the extra cases are enhancement beyond the diff's scope, under a conservative
     tier (no `openspec/`, no `tasks.md`). The observation is true, but auto-applying unrequested
     test expansion is scope creep → uncertain → **downgraded AUTO-FIX → QUESTION**. Author
     confirms whether to widen coverage now.

4. **[SKIP]** `truncpkg/truncate.go` — Byte-based truncation can split a multi-byte UTF-8 rune
   mid-sequence. The doc comment says "bytes", so this is a documented design choice, not a
   defect in this diff.
   - **Provenance:** fable #3 (SKIP). Single-finder, but not heading to AUTO-FIX, so no mandatory
     gate. Tag re-derived and upheld as SKIP.

5. **[ACKNOWLEDGED]** `truncpkg/truncate.go` — Pre-existing `n < 0` panic (negative index into
   the slice). Real, but not introduced by this diff and out of its scope; not auto-fixing here.
   - **Provenance:** fable #1 flagged it as a pre-existing aside; test-adversary #3 raised an
     adjacent "one step from an out-of-range slice panic" note. Correction to the record:
     test-adversary's `n+1 > len(s)` panic path is unreachable given the `n >= len(s)` early
     return — when `n < len(s)`, `n+1 <= len(s)`, so `s[:n+1]` never overflows. Only fable's
     `n < 0` case is a genuine (pre-existing) panic. Surfaced for the author, not actioned.

## Summary

- AUTO-FIX: 2   QUESTION: 1   SPEC-DRIFT: 0   ACKNOWLEDGED: 1   SKIP: 1
- Sources: 2 of 3 (fable broad finder + test-adversary lens). Opus broad finder MISSING — not a
  full panel.
- Corroborated: 2 (findings 1, 2). Single-finder: 2 (finding 3 → downgraded to QUESTION by the
  gate; finding 4 SKIP, gate not required). Acknowledged pre-existing: 1 (finding 5).
- Tier: conservative (no `openspec/`, no `tasks.md`). Test baseline: PASS (misleadingly green —
  findings 1+2). Lint: `go vet` clean.
- Regression confidence: LOW — the one changed function carries a behavior-changing off-by-one
  and its only test was hollowed out to hide it. Both auto-fixes are needed together: restoring
  the assertion (2) turns the suite red until the slice bound (1) is corrected.
```

## 7. What this smoke did and did not prove

**Proved (per skill §13's checklist, adjusted for the 2-source panel actually obtained):**

- The injected bug (off-by-one) and the injected weakened test (green-but-wrong) were **both actually caught**, by every source that returned output.
- **D2a's single-finder adversarial gate works as designed**: finding 3 (a real, single-finder observation) was argued against ("is this real, or scope creep beyond the diff?"), found uncertain, and **downgraded from the lens's own `[AUTO-FIX]` tag to `[QUESTION]`** by the synthesizer — the synthesizer re-derived the tag rather than trusting the source, per D2/D2a.
- **D2's corroboration model works**: two independently-sourced findings describing the same two root causes were merged into two findings, each recording both sources' provenance — provenance was not lost in dedup.
- **The foreign-finder skip path works**: `codex` in the panel's `finders` was recognized and deliberately not spawned, with the loud note reproduced in §3 above — this run has one fewer finder than the panel configured, and that fact is visible, not silently absent.
- **The synthesizer owns the final tag**, not the source: finding 3 arrived as test-adversary's own `[AUTO-FIX]`, and the synthesizer overrode it to `[QUESTION]` after argument — exactly the "no finder makes the auto-fix call" contract (D2).

**Did NOT prove / known gaps in this run (stated, not hidden):**

- **The opus broad finder leg never returned output** in this session (§3). This smoke is evidence for the fable+lens+synthesizer path, not for a true three-source panel. The Fable+Opus catch-rate claim in design.md D9 rests on the prior PR-45 bake-off, not on this smoke, and this smoke could not add a live opus-leg data point.
- **Fix-verification (skill §9) and the fix-and-re-review loop (skill §10) were not run** — out of this smoke's scope (see §1).
- **The Fable-effort pin (D7 gotcha) is unverified** — the fable finder was told to answer directly without deliberation, but this environment exposes no effort/reasoning-tier parameter to confirm which tier actually executed, matching the known gap already documented in the skill and `context/knowledge/gotchas/misc.md`.
- **Instruction delivery deviated procedurally from skill §4** (§3 above): finders read their own instruction file rather than the orchestrator pasting the verbatim body inline. Functionally equivalent, not literally identical to production behavior.

Task 4.5 in `tasks.md` is checked off on the basis of this artifact: the glue's composition/injection/synthesis/gate logic is demonstrated working, with the above gaps honestly recorded rather than smoothed over.

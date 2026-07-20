## Context

`fix-hera-view-status-spec-drift` (archived 2026-07-19) corrected 4 `hera-view` requirements to match the shipped liveness/session/content-based model. Its own ralph-review and spec-audit quality gates — run immediately after archiving — independently surfaced four residual drift items the original change's scope boundary excluded. This change closes them out. The authoritative source for "what's actually true" remains the code itself; this change just brings the remaining stale prose (two comments, two spec sentences) in line with it.

## Goals / Non-Goals

**Goals:**

- Correct two Go doc comments (`rail.go`, `model.go`) that still describe the pre-#824 task-status-gated model, so they stop misdirecting future readers.
- Correct the `hera-view` "Needs-input summary box above the rail" requirement, which still claims the per-role rollup gates on `in_progress` — directly contradicting the requirement corrected 1300 lines earlier in the same file.
- Tighten one imprecise example in the plan-view scenario that used `in_progress` as a stand-in for "genuinely active."

**Non-Goals:**

- No behavior change. The comment edits touch no logic; the spec edits describe already-shipped behavior.
- Not addressing the unrelated `statusbar.go` rail-hint-bar bug (retired `R`/`Ctrl+R` keys still shown, newer keys missing) — tracked separately via `.workflow/todo/`, unrelated root cause.
- Not re-auditing the rest of `hera-view` beyond these four specific items — the two quality gates already confirmed the 4 originally-corrected requirements and their code have zero remaining contradictions.

## Decisions

### D1 — Fix `rail.go:1628-1645`'s stale precedence comment

**Current (wrong):** doc comment above `statusIcon`/`roleStatusInputs` asserts "ready_to_close (M4) wins over everything else... then a done assertion, then GENUINE activity (a live binding whose bound argus task is in_progress — role.IsActive)."

**Actual:** the function body is a pure delegation to `widget.RoleStatusIcon`, whose real precedence (already correct, unchanged) is `NeedsInput > Active > ReadyToClose > Failed > Done > Idle > Live > default`, with `Active` defined as `Live && SessionRunning && !SessionIdle` — no task-status term. Rewrite the comment to match, citing `widget/rolestatusicon.go` the way the rest of the package's comments already do.

### D2 — Fix `model.go:1031-1034`'s stale admission comment

**Current (wrong):** `buildRoleView`'s comment says needs-input surfaces "for ANY role kind — worker, coordinator, or freelance."

**Actual:** `needsInputForHeraRail` (`internal/tui/app.go`) only admits worker- and coordinator-kind roles into the `needsInputIDs` map (`readHeraRoles`/`mergeManagedFromMeta` have no freelance case); a freelance role can only surface `(?)` while its task is literally `in_progress`. This is the identical over-generalization the spec text was corrected to drop in the prior change. Rewrite the comment to say "worker or coordinator," matching the corrected spec.

### D3 — Fix `spec.md:1670`'s stale "gates on in_progress" claim

**Current (wrong):** "Needs-input summary box above the rail" describes the per-role rollup as gating on the SAME `in_progress`-only rule the summary box itself uses.

**Actual:** the per-role rollup (corrected by `fix-hera-view-status-spec-drift`) admits `in_progress OR live` — it is NOT the same gate as the box's own (still-correct, unaffected) `in_progress`-only rule. Rewrite the comparison sentence to describe the two gates as deliberately different (the box intentionally stays coarser/task-list-scoped; the per-role rollup is liveness-aware), rather than claiming they're identical.

### D4 — Tighten `spec.md:706,715`'s imprecise scenario wording

**Current (imprecise):** uses "a live worker role's bound task is in_progress (genuinely active)" as a parenthetical equivalence.

**Actual:** post-correction, "genuinely active" means `Live && SessionRunning && !SessionIdle`, independent of task status. Reword the parenthetical to reference the actual definition instead of implying `in_progress` is a proxy for it.

## Risks / Trade-offs

- **No test to prove prose is "correct"** → Mitigated the same way the parent change was: correctness is judged by re-reading each corrected line against the cited code, not by a test suite. `openspec validate --all --strict` catches structural errors only.
- **D3's fix touches a requirement outside the original change's stated scope** → Accepted: the contradiction is a direct, visible consequence of the prior correction landing, not new scope creep.

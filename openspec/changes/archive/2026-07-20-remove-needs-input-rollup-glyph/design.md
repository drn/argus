## Context

`internal/tui/hera` renders the needs-input "(?)" glyph on five distinct surfaces, but all five funnel through ONE shared classifier chain:

```
RoleView.ShowsNeedsInput()                  (model.go)
  → roleStatusInputs(role).NeedsInput        (rail.go)
    → widget.RoleStatusIcon(in, dim, frame)  (widget/rolestatusicon.go)
```

Call sites of `roleStatusInputs`/`statusIcon`:

1. `rail.go` `drawRoleRow` → every plain worker row AND every bridging worker row (a worker role that is itself a nested sub-coordinator).
2. `rail.go` `drawOrchRow` → the rail's collapsed orchestrator HEADER, via `statusIcon(o.CoordRole(), ...)` when a coordinator role exists.
3. `details.go` line 214 → the Details pane's `coordinator:` status line glyph.
4. `details.go` line 600 (`drawRosterRow`) / `rosterStatusText` line 553 → the Details roster's per-agent status glyph AND text label.
5. `plan.go` `planNodeIcon` → the plan-DAG widget's live node icon, required to be 1:1 with the rail by an existing base-spec requirement ("Live plan node icons are 1:1 with the rail").

Today, `ShowsNeedsInput()` returns `r.SubtreeNeedsInput || r.needsInputOwn()`. `SubtreeNeedsInput` is a rollup computed once per `BuildModel` call (`rollupNeedsInput` → `orchSubtreeNeedsInput`): true when the role itself, or any descendant transitively across bridged sub-orchestrators, has its own needs-input signal — excluding archived roles from counting toward an ancestor. A SIXTH place reads `SubtreeNeedsInput` directly rather than through the classifier: `drawOrchRow`'s `else if o.SubtreeNeedsInput { ... }` branch, which renders the needs-input glyph directly on a coordinator-LESS orchestrator header (BUG-028) — there is no coordinator role there for the classifier to run on at all.

Separately, and independently, the rail ships a partial-fold-reveal mechanism (`appendOrch`, `appendOrchWorkers`, `appendOrchRevealPath`, `appendWorkerRow`, `appendPinnedRole`): when a coordinator's fold is collapsed (`isCollapsed(o.ID)`) AND `o.SubtreeNeedsInput` is true, the rail peeks through the closed fold and renders ONLY the specific row(s) down to each needs-input leaf — every other row at every level stays hidden. This is pure rendering: it never mutates fold state, and revealed rows are normal, fully-interactive rail rows. It has been verified across every nested shape the rail supports (a plain worker under one collapsed coordinator, a worker two or more collapsed levels deep, and a coordinator-spawned nested sub-team header via `coordBridgeChildren`).

Because the reveal mechanism already guarantees a needs-input descendant is independently visible as its own row — regardless of how deeply folded its ancestors are — the rollup term in `ShowsNeedsInput()` (and the BUG-028 `drawOrchRow` fallback) is redundant: nothing becomes invisible by removing it. And it is worse than redundant — it's ambiguous. Seeing "(?)" on a coordinator's own icon today conflates two different facts an operator needs to distinguish: "this session itself is blocked, act here" versus "something elsewhere in my subtree is blocked, look elsewhere" (already shown by the reveal). Collapsing both into one glyph on the coordinator's own row means the operator has to open the fold to find out which is true even when the coordinator itself is fine.

## Goals / Non-Goals

**Goals:**

- A coordinator-shaped row's needs-input glyph (rail header, bridging worker row, Details status line, Details roster row, plan-DAG node) reflects ONLY that role's own signal, on all five surfaces uniformly, via the single shared classifier — no per-surface special-casing.
- The BUG-028 coordinator-less-header fallback is removed outright: a coordinator-less orchestrator header never renders a needs-input indicator, in any state.
- The `SubtreeNeedsInput` computation, its cross-bridge transitivity, its cycle-safety, and its archived-role exclusion are preserved byte-for-byte — they continue to gate the fold-reveal mechanism exactly as today.
- `ctrl+g`'s candidate check and the switcher's needs-input-first sort — both already keyed on `needsInputOwn()` directly, never the rollup — are unaffected; verified by reading `railRow.needsInputTaskID()` before making any change.

**Non-Goals:**

- Fixing the rail's Freelance section, which has NO reveal-through-fold mechanism today (a collapsed Freelance fold hides every freelance role unconditionally, with no needs-input peek-through and no indicator on the "Freelance (N)" header). This is real and pre-existing but requires NEW reveal logic, not a display-clause removal — named as a follow-up, not fixed here.
- Touching the `depswatcher`/`orchestration` layer, the archived-role exclusion rule, or the ctrl+g candidate check — all confirmed out of scope and confirmed unaffected by reading the code first.
- The sibling `fix-ctrlg-coordinator-own-need` change's requirement ("A top-level coordinator's own needs-input signal is not a ctrl+g jump target" / its replacement) — a different sub-coordinator's independent work in the same `hera-view` capability, touching `SelectByTaskID`/`needsInputTaskID`, not `drawOrchRow` or `ShowsNeedsInput`. No file-level or requirement-level overlap: confirmed by reading their proposal.md and design.md before starting.
- Any keymap, help text, REST/API, or schema change. This is a pure display-narrowing change to five render call sites through one shared classifier plus one direct-field fallback removal.

## Decisions

**Decision 1: Change the classifier (`ShowsNeedsInput`), not the five call sites.**

`RoleView.ShowsNeedsInput()` is the ONE place all five surfaces (bar the BUG-028 fallback) read the rollup through. Changing its body from `r.SubtreeNeedsInput || r.needsInputOwn()` to `r.needsInputOwn()` propagates the narrowing to `drawRoleRow`, `drawOrchRow`'s coordinator-present branch, the Details status line, the Details roster, and the plan node — all in one line, with zero risk of the five surfaces drifting from each other (the whole point of the shared-classifier design `roleStatusInputs`/`widget.RoleStatusIcon` already enforces via the "1:1 with the rail" base-spec requirement).

Alternative considered: leave `ShowsNeedsInput()` alone and instead change each of the five call sites to call `needsInputOwn()` directly. Rejected: this defeats the entire point of the shared-classifier architecture documented in `rail.go`'s `roleStatusInputs` comment ("Single source of truth shared with the plan-view node projection... so the two surfaces render 1:1") — it would leave `ShowsNeedsInput()` itself dead code with a misleading name, and reintroduce exactly the five-call-site-drift risk the classifier exists to prevent.

**Decision 2: Delete the BUG-028 fallback branch entirely, not narrow it.**

`drawOrchRow`'s `else if o.SubtreeNeedsInput { ...IconNeedsInput... }` fires only when `o.CoordRole() == nil` — i.e., there is no coordinator role, hence no "own" signal to narrow the fallback to. Confirmed explicitly by the project owner: rely entirely on the reveal mechanism (which does not require a coordinator role to fire — `appendOrch`'s reveal gate is `o.SubtreeNeedsInput`, checked unconditionally regardless of whether `o.CoordRole()` is nil) to show where the problem is. The branch is deleted; `drawOrchRow` renders nothing in the coordinator-less case (matching the plain `if coord := o.CoordRole(); coord != nil { ... }` with no `else`).

**Decision 3: Preserve `SubtreeNeedsInput` computation and its doc comments' INTENT, correct only the parts that describe removed display behavior.**

`rollupNeedsInput`/`orchSubtreeNeedsInput` and the `SubtreeNeedsInput` fields themselves are untouched code — they still drive the fold-reveal. But several existing comments explicitly describe the OLD display behavior ("so the rail can project '(?)' up the tree", "so the rail's collapsed header can surface it even when no coordinator role exists (BUG-028)", `needsInputTaskID`'s "a folded header always shows for any descendant"). These are corrected in place to describe the new, narrower truth (the value now exists solely to gate the reveal), so the comments don't actively lie about current behavior — this is a documentation-accuracy fix, not a logic change, and ships in the same commit as the code change per the project's "every new feature documents its non-obvious gotchas" / no-stale-comment norms.

**Decision 4: The two BUG-028 whole-screen integration tests (`internal/tui/bug028_integration_test.go`) keep passing, unmodified in assertion, but get corrected docstrings.**

Both `TestBUG028_Integration_HeraRailShowsNeedsInputForBlockedWorker` and `TestBUG028_Integration_CoordinatorlessHeaderSurfacesNeedsInput` assert `screenHasRune(sim, theme.IconNeedsInput)` while the orchestrator stays COLLAPSED (default). Because the reveal mechanism is untouched and fires independent of whether a coordinator role exists, the blocked worker's OWN row is still rendered (peeked through the closed fold) in both cases, so the assertion still finds the rune on screen — just supplied by the revealed worker row instead of the header. The assertions are NOT changed (they were never header-specific — `screenHasRune` scans the whole screen); only the docstrings and inline comments that claimed the header itself carries the glyph are corrected, so the test intent stays honest about WHY it passes.

Alternative considered: rewrite these two tests to assert on the specific row/cell rather than "somewhere on screen." Rejected as scope creep — the tests' existing black-box, whole-screen-scan style is a deliberate choice elsewhere in this file (e.g. `TestBUGA_Integration_LiveInReviewWorkerAtPromptSurfaces` uses the same pattern) and rewriting the assertion style is not required to make the tests honestly reflect the new behavior; a docstring fix suffices.

**Decision 5: `rail_test.go`'s `TestStatusIcon_NeedsInputSources` — two subtests flip, one is corrected to use the right field.**

- `"subtree rollup shows (?) on an otherwise-idle coordinator"` and `"rollup beats a done coordinator"` construct a `RoleView` with ONLY `SubtreeNeedsInput: true` set (no `NeedsInput`, no `blocked` status) — exactly the coordinator-with-a-blocked-descendant-but-not-itself shape this change removes display for. Both flip to assert the OPPOSITE: the coordinator's own status glyph shows through (moon-stars for the idle/working case, `✓` for the done case), NOT the needs-input glyph.
- `"needs-input rollup wins over ready_to_close (BUG-A)"` constructs `RoleView{ReadyToClose: true, SubtreeNeedsInput: true}` with no `NeedsInput`. This is mislabeled: the REAL BUG-A invariant (a role's OWN needs-input beats its OWN `ready_to_close`) is unaffected by this change and remains true — but the synthetic input used to exercise it (bare `SubtreeNeedsInput`) is exactly the rollup-only case that no longer produces "(?)". The fix corrects the test's construction to `NeedsInput: true` (the actual own-signal field), which continues to correctly prove BUG-A holds post-change, rather than flipping the assertion (which would incorrectly suggest BUG-A itself changed).

**Decision 6: `plan_test.go`'s `TestPlanNodeIcon_NeedsInputNotAnimated`'s "descendant subtree rollup" subtest flips.**

This subtest builds a two-orchestrator bridge (`R(coord tr, worker w→tc) → C(coord tc, worker wc→twc)`), marks only the leaf `wc` as needing input, calls `m.rollupNeedsInput()`, and asserts the BRIDGING node `w` (itself active, not blocked) renders the static "?" — purely from `wc`'s rollup. Post-change this must flip: `w`'s plan node renders its own active spinner (genuinely working), NOT "?", because `w` itself never needs input. The subtest is rewritten to assert this, keeping the same seed shape (it still proves the plan node reads the SAME classifier as the rail — `statusIcon(w, ...)` and `planNodeIcon` must agree on the new, non-"?" result — which is the parity property this test exists to pin in the first place).

**Decision 7: New Details coverage, since neither surface has needs-input-specific tests today.**

`details_test.go`'s existing coordinator-status tests cover only status/task-status label TEXT (`coordStatusLabel`), never the needs-input GLYPH. New tests are added asserting: (a) a coordinator with its own needs-input signal renders the needs-input glyph on the Details status line; (b) a coordinator with ONLY a blocked descendant (no own signal) does NOT render the needs-input glyph on its Details status line (mirrors rail_test's flipped rollup subtests, but through `DetailsView`'s actual draw path); (c) the equivalent pair for a roster row (`rosterStatusText`/`drawRosterRow`) — a worker's own needs-input signal shows `"needs-input"` + the glyph; a bridging worker-row-as-sub-coordinator with only a descendant rollup does not.

## Risks / Trade-offs

- **[Risk]** An operator who previously relied on "coordinator icon lights up = something in here needs attention" as a coarse, at-a-glance summary loses that specific affordance; they must now open the fold (or trust the reveal, which already surfaces the specific row without opening it) to see it. **[Mitigation]** The reveal mechanism already does this automatically and more precisely — it shows WHICH row, not just THAT something is wrong — so the coarse signal is being replaced by a strictly more actionable one, not removed outright. No mitigation code needed; this is the intended UX improvement stated in the Why.
- **[Risk]** A coordinator-less orchestrator (its coordinator role nuked) now shows NO indicator anywhere on its header even with a blocked descendant several folds deep, if the operator has manually collapsed every intervening fold in a way that happens to also be reveal-gated correctly — in practice this can't happen: the reveal is unconditional on `SubtreeNeedsInput`, so the descendant's row is ALWAYS shown regardless of nesting depth. **[Mitigation]** None needed; verified by reading `appendOrch`'s reveal gate, which does not require `CoordRole() != nil`.
- **[Risk]** The two BUG-028 whole-screen integration tests continuing to pass "for the wrong reason" (i.e., without code review reading design.md) could look like a no-op change. **[Mitigation]** Docstrings corrected in the same commit explicitly stating the new reason; `TestBUG028_CoordinatorlessOrchSurfacesSubtreeNeedsInput`'s file-level comment already correctly scopes itself to the COMPUTATION, not the display, so it needs no change — read carefully before assuming it does.

No migration plan or open questions — this is a pure display-narrowing change to existing, shipped rendering with no data model, schema, or API implications.

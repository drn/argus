## Why

The `fix-hera-view-status-spec-drift` change (archived 2026-07-19) corrected 4 requirements in `openspec/specs/hera-view/spec.md` that had drifted from the shipped liveness/session/content-based model (PR #824, BUG-A/BUG-C/BUG-F/#707). Its own quality gates (ralph-review + spec-audit) found the correction was accurate, but surfaced residual drift the original change's scope boundary excluded: two Go doc comments still describe the pre-#824 precedence/admission model, and one untouched requirement elsewhere in the same spec file now visibly contradicts the just-corrected text. Left alone, these keep pointing future readers at the wrong model — the exact failure mode the original change existed to fix.

## What Changes

- Correct the stale doc comment above `statusIcon`/`roleStatusInputs` in `internal/tui/hera/rail.go:1628-1645`, which still asserts "ready_to_close wins over everything… then GENUINE activity (a live binding whose bound argus task is in_progress)" even though the function body delegates to the already-correct `widget.RoleStatusIcon`.
- Correct the stale doc comment in `internal/tui/hera/model.go:1031-1034` (`buildRoleView`), which still says needs-input surfaces for "worker, coordinator, or freelance" — the same over-generalization the spec text was corrected to drop, since `needsInputForHeraRail`'s admission feed excludes freelance-kind roles.
- Correct `openspec/specs/hera-view/spec.md:1670` ("Needs-input summary box above the rail" requirement), which still claims the per-role rollup "gates on in_progress" — contradicting the corrected "Needs-input propagates up" requirement ~1300 lines earlier, which admits `in_progress OR live`.
- Tighten the imprecise example wording at `spec.md:706,715` ("in_progress (genuinely active)") in the plan-view scenario, which used task-status as a stand-in for "genuinely active" — no longer an accurate equivalence post-correction.

No behavioral change to the running code (comment-only edits) except the one spec-text correction, which documents already-shipped behavior.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `hera-view`: correct the "Needs-input summary box above the rail" requirement's stale in_progress-only gate description, and tighten the plan-view scenario's imprecise "in_progress (genuinely active)" example wording.

## Impact

- `internal/tui/hera/rail.go` — doc comment only, no behavior change.
- `internal/tui/hera/model.go` — doc comment only, no behavior change.
- `openspec/specs/hera-view/spec.md` — two requirement text corrections (no code to match; both describe already-shipped behavior).

A separate, unrelated pre-existing bug was also found during the same quality-gate pass (`internal/tui/widget/statusbar.go`'s rail hint bar still shows retired `R`/`Ctrl+R` keys and omits newer bound keys) — tracked separately via `.workflow/todo/`, out of scope for this change.

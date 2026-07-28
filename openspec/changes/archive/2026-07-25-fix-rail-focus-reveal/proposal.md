## Why

The rail's partial-fold-reveal mechanism (`appendOrchWorkers`/`appendOrchRevealPath`, "Rail reveals the ancestor path to a hidden needs-input descendant through closed folds") peeks a specific needs-input descendant's row through an otherwise-closed ancestor fold. That reveal is recomputed from scratch on every `Rail.SetModel` rebuild, driven purely by the FRESH model's `SubtreeNeedsInput` rollup — it carries no memory of what was visible a moment ago.

That statelessness is fine most of the time, but it breaks the one case an operator actually depends on: they jump to a folded-away worker via the reveal, land the rail cursor on it (the panes now show its session), and start typing an answer to its prompt. The instant the prompt clears — the exact moment they were waiting for — the worker's own needs-input signal flips false, its `SubtreeNeedsInput` rollup goes false with it, and the very next rebuild (the next tick) no longer has any reason to peek through the closed ancestor. The row vanishes mid-rebuild, `restoreCursor`'s identity lookup fails to find it, and the cursor (and the panes bound to it) get yanked onto whatever row the stale cursor INDEX now happens to land on — often the ancestor's own header. The operator loses their place in the exact instant they were most engaged with it.

## What Changes

- `Rail.SetModel` now keeps the previously-selected row (role or orchestrator header) forcibly revealed through a closed ancestor fold for one additional rebuild whenever it would otherwise disappear — by forcing `SubtreeNeedsInput` true along its ancestor chain (and, for a role, on the role itself) in the freshly-received model, before `buildRows` runs. This reuses the existing reveal machinery verbatim (no new gates, no new fold state) rather than forking a parallel "sticky" code path.
- Because the forced flag is re-derived from the CURRENT cursor identity on every `SetModel` call, the effect persists for exactly as long as the operator's selection doesn't move — the instant they navigate to a different row (or a rebuild's `restoreCursor` naturally lands them elsewhere), the next rebuild computes stickiness from the NEW identity and the old row is free to fold away again, exactly like today.
- No behavior changes when the previously-selected row was already visible through normal (non-collapsed) expansion — forcing the rollup flag on an already-expanded ancestor is a no-op, since the non-`revealOnly` render path never consults it.
- Everything else about the reveal mechanism — which specific descendant paths get revealed, sibling suppression, `Space` toggle semantics, `ctrl+g`/`ctrl+b` excursion behavior — is unchanged.

## Capabilities

### Modified Capabilities

- `hera-view`: the rail's partial-fold-reveal gains a "sticky" extension keyed on the previously-selected row's identity, so a focused/selected role's row survives its own needs-input flag clearing instead of vanishing out from under the operator.

## Impact

- **Modified code:**
  - `internal/tui/hera/model.go` — new `Model.roleByID` (mirrors the existing `roleOrchID` scoping: Pinned/Active/Archived only, Freelance excluded) and `OrchView.bridgingRoleFor` (mirrors `hasWorkerBridging` but returns the role pointer) helpers.
  - `internal/tui/hera/rail.go` — new `Rail.applyStickyReveal(ref int64)`, called from `SetModel` right after resolving the effective previous-selection ref (including the one-shot `pendingSelRef` override) and before `buildRows`.
- **No schema change, no daemon RPC, no persistence** — purely an in-memory rendering behavior over a per-rebuild model snapshot, exactly like the reveal mechanism it extends.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build wiring is added or changed. The quality gate stays `make pre-pr`.

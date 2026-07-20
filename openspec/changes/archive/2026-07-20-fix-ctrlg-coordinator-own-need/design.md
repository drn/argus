## Context

`ctrl+g` (`Rail.NextNeedsInputTaskID` / `HeraPage.JumpToNextNeedsInput`, `internal/tui/hera/rail.go` + `page.go`) cycles the rail cursor to the next role whose OWN needs-input signal is set. It was shipped scoped to role-bearing rows only: `railRow.needsInputTaskID()` requires `row.role != nil`, and the landing primitive it calls through, `Rail.SelectByTaskID`, matches only `row.role.TaskID`.

A coordinator role is never its own row. `appendOrch` folds a coordinator entirely into its orchestrator's `rrOrch` HEADER row (`appendOrchWorkers` explicitly skips `db.HeraKindCoordinator` — "folded into the header"). This is true for two distinct shapes, both rendered through the identical header-only path:

1. A **top-level orchestrator's** own coordinator.
2. A **coordinator-spawned nested sub-team's** own coordinator — one coordinator agent simultaneously driving a second orchestrator via `hera_new_orchestrator` (`coordBridgeChildren`, placed by `appendOrchWorkers` via `r.appendOrch(child, ...)`, the SAME header-only path as a root).

Because neither shape ever produces a role-bearing row, `ctrl+g` structurally cannot reach either coordinator's own need — not merely filtered out, but absent from `r.rows` under any existing criterion. The base spec currently documents this as an intentional exclusion (`openspec/specs/hera-view/spec.md`, "A top-level coordinator's own needs-input signal is not a ctrl+g jump target"). This change reverses that decision.

The underlying signal already exists and needs no new plumbing: `OrchView.CoordRole().needsInputOwn()` is a fully independent, already-computed value (task-keyed off `App.needsInputIDs`, admits coordinator roles by design — see BUG-028), distinct from `SubtreeNeedsInput` (the rollup that drives the header's "(?)" glyph today and must keep doing exactly that, unchanged).

The already-working THIRD shape — a nested sub-coordinator that bridges as an ordinary WORKER row in its parent orchestrator (`appendWorkerRow`, since the worker's task IS the child coordinator's live task) — is unaffected by any of this; it already has a role-bearing row and is already a reachable candidate.

## Goals / Non-Goals

**Goals:**

- A top-level coordinator's own needs-input signal becomes a reachable `ctrl+g` candidate, landing the rail cursor/focus on that coordinator (details/coordinator pane), exactly as manual cursor navigation onto its header already does today.
- A coordinator-spawned nested sub-team's own needs-input signal becomes reachable the same way, via the same code path (no shape-specific branching — both are `rrOrch` header rows).
- `SelectByTaskID` gains the ability to resolve a coordinator's own task id to its header row when no role row matches — the single shared primitive both `ctrl+g` and `JumpToTask` already funnel through.

**Non-Goals:**

- Changing the header's "(?)" glyph semantics. `SubtreeNeedsInput`-driven rendering (`drawOrchRow`) is untouched; this change only affects what `ctrl+g`/`SelectByTaskID` can navigate *to*.
- Touching the already-working worker-bridge nested sub-coordinator path.
- Solving the shared-task multi-header edge case (see Risks) with new disambiguation logic — the existing multi-binding "first match in row order wins" convention already covers it, and inventing a richer resolution scheme is out of scope for what's otherwise a two-function bugfix.
- Any keymap/help text change — the existing label ("jump to next needs-input (?)") stays accurate for the new behavior.
- Any change to `ctrl+j`'s unified switcher. It happens to share `SelectByTaskID`/`JumpToTask`, so a hera-managed coordinator entry that previously silently fell through to the classic per-task view will, as an incidental side effect, now land in the Hera tab instead — a strict improvement consistent with the switcher's own documented intent, not a behavior this change needs to design for or test against.

## Decisions

**Decision 1: Extend the two existing shared primitives rather than add a parallel "header jump" path.**

`railRow.needsInputTaskID()` gains an `row.kind == rrOrch` branch: resolve `coord := row.orch.CoordRole()`; if `coord != nil && coord.needsInputOwn() && coord.TaskID != ""`, it qualifies, returning `coord.TaskID`. `Rail.SelectByTaskID()` gains a second scan pass — run only if the existing role-row scan finds nothing — matching `row.kind == rrOrch && row.orch.CoordRole() != nil && row.orch.CoordRole().TaskID == taskID`.

Alternative considered: introduce a distinct ref-based addressing scheme (reusing `currentRef()`'s signed role-id/`-orch-id` convention) threaded through a new `SelectByRef`/`NextNeedsInputRef` pair, since that convention already exists for cursor-restore-after-rebuild. Rejected: `SelectByTaskID`/`JumpToTask` are already task-id-keyed end to end (including the ancestor-expansion loop below), and a coordinator's own task id is a perfectly valid, already-unique key for this purpose — introducing a second addressing scheme would touch more call sites for no behavioral gain.

**Decision 2: No change to ancestor-expansion.**

`JumpToTask` (`page.go`) already does `for _, orchID := range p.rail.Model().OrchIDsForTask(id) { p.rail.EnsureAncestorsExpanded(orchID) }` before calling `SelectByTaskID`. `OrchIDsForTask` walks every role in every orchestrator's `.Roles` (coordinator-kind included, no filter) — it already resolves a coordinator's own task id to the right orchestrator id(s), including the coordinator-spawned child's id via its own coordinator role. This loop was written role-kind-agnostic from the start and needs no change.

Separately: a header row is placed in `r.rows` **unconditionally**, regardless of fold state (`appendOrch` appends the `rrOrch` row before checking `isCollapsed`). And when an ancestor IS collapsed, the partial-fold-reveal path (`appendOrchWorkers`'s `revealOnly` branch, `appendOrchRevealPath`) already recurses into a coordinator-spawned child whenever `child.SubtreeNeedsInput` is true — which it will be, since `orchSubtreeNeedsInput` already walks the coordinator role when computing subtree rollup. So the candidate scan sees these header rows today even behind a closed fold; only actually landing the cursor there (not just peeking through the reveal) goes through the existing `EnsureAncestorsExpanded` call, which real-expands the fold exactly as it does for any other jump target.

**Decision 3: Accept, document, and test the shared-task multi-header edge case rather than solve it.**

A coordinator-spawned sub-team's parent and child orchestrators can have coordinator roles that share the SAME underlying task (one agent, two `OrchView`s, two distinct `RoleView`s with the same `TaskID`). If that task needs input, both header rows independently qualify as candidates. `SelectByTaskID`'s scan (role rows today, extended to header rows here) resolves the FIRST match in `r.rows` order — the existing, already-shipped convention for any multi-binding task (see `page.go`'s own comment: "multi-binding: SelectByTaskID lands on whichever bound orchestrator's row it resolves first"). Practical effect: `ctrl+g` may keep landing on the same (first) header rather than alternating between parent and child. This mirrors pre-existing behavior for ordinary multi-binding role rows and isn't a regression this change introduces — it's the first time the pattern becomes *visible* for coordinator headers specifically, because coordinator headers were previously unreachable altogether. Captured as an explicit test (see tasks.md) rather than left as a silent surprise.

## Risks / Trade-offs

- **[Risk]** The shared-task multi-header case above could read as "ctrl+g is stuck" to an operator with a coordinator-spawned sub-team. **[Mitigation]** Behavior is identical to today's accepted multi-binding convention elsewhere in the rail; documented in the base spec delta and covered by a dedicated test asserting the observed (accepted) resolution, not a silent gap.
- **[Risk]** Widening `SelectByTaskID` to match header rows changes `ctrl+j`'s (unified switcher's) fallback behavior for hera-managed coordinator entries, a shared code path not requested by this change. **[Mitigation]** The change is strictly additive/improving (falls through to the Hera tab instead of the classic view) and doesn't alter any base-spec requirement for the switcher; called out as a Non-Goal rather than silently shipped unacknowledged.

No migration plan or open questions — this is a pure bugfix to existing, shipped behavior with no data model or deployment implications.

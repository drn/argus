# Proposal: Hera-view needs-input summary box

## Why

While working in the Hera tab the operator has no signal that a task **outside** Hera (a standalone Tasks-tab task) is blocked in the `(?)` needs-input state — the agent/task view has its attention bar, Hera has nothing. The needs-input set is already piped into the Hera page each tick; only the rendering is missing.

## What Changes

- Add a `widget.AttentionSummary` widget: a fixed one-line bordered box rendering `"N task(s) need input"`, height 0 when N=0.
- Draw it at the top of the Hera rail column whenever ≥1 needs-input task has **no presence in the Hera model**, shrinking the rail by the box height. Hidden (rail full height) otherwise.
- Compute the count in `HeraPage.Draw` as `needsInput − managedTaskIDs`, where the managed set is every role's `TaskID`/`BridgeTaskID` across all orchestrator sections plus the Freelance section. Coordinators, managed workers (including folded-subtree ones, already cued via the rollup), and Hera freelance-roles are excluded.
- Passive heads-up: no keybinding, no focus, no click-to-jump. Inert in remote mode.
- `uxlog` on the show/hide (count 0↔N) transition.

No App-side feed change (`SetNeedsInput([]string)` unchanged), no schema/config/API change, no keybinding change.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- **hera-view** — adds one requirement: a needs-input summary box above the rail.

## Impact

- Code: new `internal/tui/widget/attentionsummary.go`; `internal/tui/hera/page.go` (`Draw` geometry + count computation, likely a small `model.go`/page helper for the managed-set walk).
- Tests: `internal/tui/widget` unit tests for the widget; `internal/tui/hera` unit tests for the residual-count computation + a SimulationScreen smoke test for the layout shrink/hide.
- Docs: `context/knowledge/gotchas/hera-view.md` gets the geometry/exclusion gotcha. No README/help-modal change (no keys).
- No dependencies, APIs, or persisted state touched.

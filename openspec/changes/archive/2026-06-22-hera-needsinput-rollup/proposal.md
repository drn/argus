## Why

BUG-018: when a worker/agent in the Hera rail enters the needs-input "(?)"
state, that attention does NOT propagate up the orchestration tree. The operator
has to expand and scan every sub-team to discover that something, somewhere, is
blocked on a prompt. The user wants the attention to bubble: the agent's PARENT
coordinator shows "(?)" too, transitively all the way to the ROOT coordinator —
so a single glance at the top of the rail reveals that the subtree needs a human.

Today the rail's only "(?)" source is the per-role hera `blocked` STATUS
(`statusIcon` at `internal/tui/hera/rail.go`). Worse, native Hera carried NO
per-role needs-input flag at all (`model.go` notes "Native has no per-task
idle/needs-input flag"), so a worker genuinely blocked on a prompt — the exact
case the task list flags with "(?)" via `App.needsInputIDs` — did not even show
"(?)" on ITSELF in the rail, let alone roll up.

## What Changes

- Native Hera RoleViews gain a real needs-input signal sourced from the SAME
  authoritative set the task list consumes — `App.needsInputIDs`
  (`agent.DetectNeedsInput`, idle-gated sticky PTY-tail scan). The App threads it
  into the Hera page each tick (`HeraPage.SetNeedsInput`), and `BuildModel`
  stamps each live role's `RoleView.NeedsInput`. No new detection is invented.
- `BuildModel` computes a needs-input ROLLUP in the MODEL: each role's new
  `RoleView.SubtreeNeedsInput` is true when the role itself OR any descendant
  role in its orchestration subtree (transitively across BRIDGED
  sub-orchestrators) needs input. The walk reuses the existing
  `BridgeSubtree`/`bridgeIndex` traversal (cycle-safe visited set), so it crosses
  bridges exactly like the rail's nesting and the Ctrl+D cascade.
- `statusIcon` stays a pure projection: its needs-input branch now reads
  `RoleView.ShowsNeedsInput()` (the rollup OR the role's own
  needs-input/`blocked` signal) instead of only `Status == blocked`. A
  coordinator header (the folded coordinator role) thus shows "(?)" whenever its
  subtree needs input; a bridging worker row (a nested sub-coordinator) shows
  "(?)" for its bridged child's subtree; both chain to the root.
- Precedence (documented): the needs-input rollup ranks immediately below a
  role's own `ready_to_close` mark and ABOVE `done`/active-spinner/idle/live — so
  a descendant needing input surfaces on an ancestor even when the ancestor is
  itself idle, working, or done. `ready_to_close` (a distinct actionable
  check-off mark) still wins on the role that carries it.

## Capabilities

### Modified Capabilities

- `hera-view`: A coordinator's rail status icon shows the needs-input "(?)"
  indicator when it itself OR any descendant role (transitively, across nested
  sub-orchestrators) is in needs-input, computed as a model-side rollup and
  cleared when no descendant needs input.

## Impact

- **Modified code:** `internal/tui/hera/model.go` (`RoleView.NeedsInput` +
  `SubtreeNeedsInput` fields, `ShowsNeedsInput`/`needsInputOwn`, `BuildModel`
  needs-input param + `rollupNeedsInput` post-pass), `internal/tui/hera/rail.go`
  (`statusIcon` reads `ShowsNeedsInput`), `internal/tui/hera/page.go`
  (`SetNeedsInput` + `doRefresh` threads it), `internal/tui/app.go` (push
  `needsInputIDs` to the Hera page each tick).
- **Tests:** `internal/tui/hera/model_test.go` (rollup to parent + root, clears,
  multi-level bridged propagation, no false-positive, cycle-safe),
  `internal/tui/hera/rail_test.go` (`statusIcon` glyph + precedence),
  `internal/tui/hera/page_test.go` (`SetNeedsInput` threads through).
- **Docs:** `context/knowledge/gotchas/hera-view.md` (the needs-input subtree
  rollup, cross-bridge traversal, precedence).
- **No new keys** (no help-overlay / README change), **no schema change, no
  daemon RPC, no `screen.Sync()`** — this is a projection/render change. Specs
  stay LOCAL DOCS only; the quality gate stays `make pre-pr`.

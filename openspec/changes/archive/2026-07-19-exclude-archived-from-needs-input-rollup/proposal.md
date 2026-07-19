## Why

The Hera rail already rolls up a blocked descendant's needs-input "(?)" to
every ancestor coordinator, up to the root. Archiving a role (`a`) is meant to
be a reversible way to dismiss a row from attention, but today it does not stop
that role's needs-input signal from counting toward its ancestors — so a user
who archives a blocked worker (or a nested sub-coordinator's bridging row)
still sees the root coordinator flagged "(?)" for something they already
dismissed.

## What Changes

- The needs-input rollup (`orchSubtreeNeedsInput` in `internal/tui/hera/model.go`)
  excludes any archived role's own needs-input signal, AND anything bridged
  beneath an archived role (a nested sub-coordinator's whole hidden subtree),
  from counting toward any ancestor coordinator or coordinator-less
  orchestrator header.
- An archived role's OWN rail row is unaffected — it keeps showing its own
  "(?)" glyph exactly as today. Only what ancestors count changes.
- No changes to `BridgeSubtree`, rail nesting/dimming, or the Ctrl+D cascade —
  archived rows still render dimmed in place, unchanged.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the "Needs-input '(?)' propagates up the orchestration tree to
  the root (area rail)" requirement gains an explicit archived-node exclusion.

## Impact

- `internal/tui/hera/model.go`: `orchSubtreeNeedsInput` rewritten to a
  dedicated archive-aware recursive walk (see design.md); `rollupNeedsInput`
  itself is unchanged.
- `internal/tui/hera/model_test.go`: new test cases for the archived-role,
  archived-bridging-row, and archived-orchestrator exclusion cases, plus a
  regression case proving the archived role's own row still shows "(?)".
- No API, storage, or migration changes — the fix is confined to in-memory
  rollup computation in the TUI's Hera model.

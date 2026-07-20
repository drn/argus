## Why

`ctrl+g` (jump to the next role needing input) currently cannot reach a coordinator's own needs-input signal at all — a coordinator role is always folded into its orchestrator's header row rather than rendered as a row of its own, and both the candidate scan and the landing primitive it uses key exclusively off role rows. An operator whose coordinator itself is blocked on a prompt sees the header's "(?)" glyph but has no way to jump directly to it — this was confirmed live and matches a base-spec requirement that documents the gap as an intentional (now reversed) exclusion.

## What Changes

- `Rail.SelectByTaskID` gains a second match pass: when no role row matches a task id, check orchestrator header rows via their coordinator's own task id.
- `railRow.needsInputTaskID()` (the per-row candidate check `ctrl+g`'s cycle scans) gains a header-row branch: a header qualifies when its coordinator's OWN needs-input signal (`needsInputOwn()`) is set — not the subtree rollup that already drives the header's "(?)" glyph, which is unchanged.
- Applies uniformly to both shapes that render through the header-only path: a top-level orchestrator's own coordinator, and a coordinator-spawned nested sub-team's own coordinator. No shape-specific branching.
- Two existing docstrings/comments that assert the old "deliberately excluded" framing are corrected.
- One existing test is flipped (it currently asserts the exclusion as correct); new tests cover the nested coordinator-spawned case and the shared-task multi-header edge case.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: two requirements change.
  - "ctrl+g jumps to the next role needing input in rail order, cycling on repeat" — a header row's own coordinator needs-input signal now independently qualifies it as a candidate (previously only a role row's own signal could).
  - "A top-level coordinator's own needs-input signal is not a ctrl+g jump target" — removed. This requirement documented the exact limitation being reversed; the new behavior (for both top-level and coordinator-spawned nested coordinators) is folded into the requirement above rather than kept as a separate carve-out.

## Impact

- Code: `internal/tui/hera/rail.go` only (`SelectByTaskID`, `railRow.needsInputTaskID`, plus their docstrings). No changes to `page.go`, rendering, ancestor-expansion, or the header's glyph logic.
- Tests: `internal/tui/hera/jumpneedsinput_test.go` (flip one existing test, add new ones); possibly a small addition alongside existing `SelectByTaskID` coverage in `rail_test.go`.
- Incidental, non-breaking side effect: `ctrl+j`'s unified switcher shares `SelectByTaskID`/`JumpToTask`, so a hera-managed coordinator entry that previously fell through to the classic per-task view will now land in the Hera tab instead — a strict improvement, not a regression, and not a base-spec requirement change (see design.md Non-Goals).
- No API, schema, or keymap/help changes.

## Why

The kanban rail (PR #869) always renders every non-empty status group (Backlog/Blocked/Done) fully expanded alongside Active. In practice this makes the rail noisy: while working in Active, unrelated Backlog/Blocked/Done coordinators (and their subtrees) stay visible and take up scroll real estate. The rail should show detail only for whichever kanban group currently holds the operator's attention, and summarize the rest to a single header line.

## What Changes

- Kanban groups (Active, Backlog, Blocked, Done) become mutually-exclusive fold sections: only the group containing the current rail selection renders its member rows; the other three render as a header + count only (e.g. `"Backlog (3)"`).
- The `Active` group gains its own header row (`"Active (N)"`), matching Backlog/Blocked/Done, replacing its current headerless rendering. **BREAKING** (visual only): the Pinned→Active divider special-case is retired in favor of the same unconditioned per-group divider convention Backlog/Blocked/Done already use.
- Kanban headers remain non-selectable (no change there). Stepping (`j`/`k`/arrows) past the boundary of the focused group transparently expands the adjacent non-empty group (collapsing the one just left) and lands the cursor on its first (moving down) or last (moving up) member row, in the same keystroke — the header itself is never a landing spot.
- Any selection jump that targets a role/orchestrator outside the currently-focused kanban group (the `m`/`M` status-cycle keys, `SelectByTaskID` from the plan view, `EnsureAncestorsExpanded`) re-focuses the kanban group containing that target before locating the row, so the jump always lands and the "exactly one kanban group expanded" invariant holds.
- Kanban fold state is NOT persisted across restarts (unlike per-orchestrator/Freelance/Archive fold) — it is fully derived from wherever the restored selection lands; default focus is `active` when no prior selection resolves.
- Pinned, Freelance, and Archive sections are unaffected — they keep their existing independent, manually-toggled, persisted fold behavior.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the "Rail sections — Pinned, Active, Freelance, Archive (area 2)" requirement changes (Active gains a header, uniform per-group divider convention, groups become collapsible); the "Cursor navigation and collapse over selectable rows (area 1)" requirement gains the crossing-expands-and-collapses stepping behavior; a new requirement documents kanban-group auto-fold and the re-focus-on-jump invariant.

## Impact

- `internal/tui/hera/rail.go`: `buildRows` (kanban group loop — add Active header, drive child rendering off a new focused-group field instead of "always render"), `step` (detect crossing into a differently-focused, currently-collapsed kanban group and re-expand+relocate), `SetModel`/`SelectByTaskID`/`EnsureAncestorsExpanded` (resolve+set the focused kanban group from the target ref's top-level orchestrator before building rows, so the target row exists to select).
- No DB schema, REST API, or cross-frontend (web/macOS) impact — the kanban view is native-TUI-only already (existing documented parity gap), and this is pure rail rendering/navigation behavior within that surface.
- Tests: `internal/tui/hera/rail_test.go` (new fold/nav scenarios), existing kanban rail tests updated for the Active header.

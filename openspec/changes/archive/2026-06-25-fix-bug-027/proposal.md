## Why

**BUG-027 — the Hera rail's "Pinned" section runs directly into the Active list with no visual separation.**

The native Hera rail (`internal/tui/hera/rail.go`) draws a horizontal-rule
divider (`─`, `theme.StyleBorder`) above the Freelance section and above the
bottom Archive section, setting each apart from the roster. The Pinned section
gets no such treatment: pinned orchestrators (and floated pinned-role
breadcrumbs) abut the first Active orchestrator with no boundary, so the operator
cannot tell where the curated Pinned set ends and the live Active roster begins.

## What Changes

- **A horizontal-rule divider (the same `rrRule` row the Freelance / Archive
  sections already use) separates the Pinned section from the Active list.** It
  renders only when a Pinned section is actually present AND at least one Active
  entry follows it — no stray rule when nothing is pinned, and none when the
  Pinned section is the only content.
- **The divider is non-selectable** (the existing `rrRule` kind already returns
  `false` from `selectable()`), so `j`/`k` cursor navigation skips it exactly as
  it skips the Freelance and Archive rules. Row indexing and the two-line pinned
  breadcrumb pairing are unaffected.

## Capabilities

### Modified Capabilities

- `hera-view`: the rail now draws a horizontal-rule divider between the Pinned
  section and the Active list, rendered only when both are present — mirroring the
  dividers already drawn above the Freelance and Archive sections.

## Impact

- **Modified code:**
  - `internal/tui/hera/rail.go` (`buildRows`) — emit an `rrRule` row between the
    Pinned section and the first Active row when the Pinned section rendered and
    the Active section produced at least one row.
- **No new key, no new dependency, no schema change, no daemon RPC.** Pure
  read-only view rendering; reuses the existing `rrRule` row kind and `StyleBorder`
  rune.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.

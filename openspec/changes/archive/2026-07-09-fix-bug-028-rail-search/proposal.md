## Why

**BUG-028 — the Hera rail's `/` search flow takes too many steps to jump into an
agent.** Today, finding and entering an agent by name requires: focus the rail,
press `/`, type a query, press Enter to "lock" the filter (input mode off, query
stays applied), arrow down to the target row, then press Enter AGAIN to jump into
it — and the filter stays applied afterward, so the rail stays narrowed even
after the operator has already navigated away. Five distinct steps (plus a
lingering filter) to do something that should be "type, hit Enter, you're in."

## What Changes

- **A single Enter now selects the current match, jumps into it, AND clears the
  filter — one keystroke, not two.** There is no more "accepted but still
  narrowed, input mode off" resting state: `Enter` (like `Esc`) always fully
  exits search mode and restores the unfiltered rail; the difference is that
  `Enter` first resolves the reattach/focus-advance against the row under the
  cursor before clearing.
- **The FIRST real match auto-selects live as the operator types or backspaces.**
  No more "type, then arrow down to find it" — the cursor jumps to the top
  candidate on every query change, so `/bugb<Enter>` jumps straight into an agent
  named "hera-bugbash" if that's the first (or only) real match.
- **Up/Down still navigate the narrowed set while remaining in the search input**
  (unchanged from today), but now only ever land on rows that are themselves a
  text match against the query.
- **Ancestry-only heading rows are no longer valid selection targets.** When a
  filter narrows the tree, an orchestrator/coordinator heading kept on screen
  only to preserve tree context for a matching descendant (its own name — or its
  folded-in coordinator's name — does NOT match the query) is skipped entirely by
  arrow navigation and by first-match auto-select, and renders visually dimmed so
  it's obvious it can't be selected. "First match" / Enter always resolves to an
  actual matching row, never a context-only ancestry heading.

This is a Hera-rail-only UX change. The Tasks-tab (`internal/tui/taskview`)
`/` filter, which historically mirrored the OLD two-Enter convention, is
explicitly NOT touched by this change — the two filters now intentionally
diverge (see the doc updates in Non-Goals below).

## Non-Goals

- The Tasks-tab `/` filter keeps its existing two-step (type → Enter locks →
  navigate → Enter selects) convention. `context/knowledge/gotchas/hera-view.md`
  and `context/knowledge/gotchas/keybindings.md` are updated in this change to
  stop describing the two conventions as mirrored.
- No change to Freelance/Archive section fold-header selectability, arrow
  reachability, or fold behavior while filtering — those remain exactly as
  today. Only coordinator/orchestrator headings (and rows that stand in for a
  nested coordinator via a worker-bridge or pinned breadcrumb) gain the
  ancestry-only non-selectable/dimmed treatment.
- No change to the Tasks-tab filter, persistence (BUG-002), or any non-Hera-rail
  keybinding.

## Impact

- Affected capability: `hera-view` (the `/` rail filter requirement).
- Affected code: `internal/tui/hera/rail.go` (filter state machine, row
  building, rendering), `internal/tui/hera/page.go`
  (`handleRailMutation`'s Enter-while-filtering branch).
- Affected docs: `context/knowledge/gotchas/hera-view.md`,
  `context/knowledge/gotchas/keybindings.md`.

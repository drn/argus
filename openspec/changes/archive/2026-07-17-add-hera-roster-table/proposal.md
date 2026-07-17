## Why

The native Hera coordinator "readout" — the `Agents (N):` section of the
Details pane shown when a coordinator is selected — renders each agent as a
single bullet-style line: an icon, the role name, and an optional trailing
`ready`/`PR` mark. `add-diligence-profiles`/model-tiering already resolves and
threads a per-agent **diligence archetype** (`RoleView.Archetype`) and
**resolved model** (`RoleView.AppliedModel`) onto every role — the plan-view
tier readout already shows them (`Tier: <archetype> → <model>`) — but the
coordinator roster never surfaces either, so a coordinator glancing at "what is
my team running" has no way to see it without drilling into the plan DAG.

Separately: today a long roster (18+ agents, the real repro that surfaced this)
simply cuts off at the bottom of the panel — the overflow is unreachable, with
no way to see agents beyond whatever fits in the roster's row budget (itself
capped at half the Details region so the embedded plan graph keeps room). Both
land in this same change.

## What Changes

- **The Agents roster renders as a compact, aligned TABLE** with four columns:
  status (icon + label — needs-input / working / ready(+PR) / failed / done /
  idle / live / unbound), name, diligence archetype, and resolved model. A
  lightweight `STATUS  NAME  ARCHETYPE  MODEL` header row precedes the data
  rows.
- **Archetype and model are sourced from the already-annotated `RoleView`**
  (`Archetype` from the role row, `AppliedModel` stamped by `HeraPage`'s
  `tierResolver` during `doRefresh`) — no daemon, store, or MCP change. An
  unresolved archetype/model renders `—`, never a blank cell.
- **`ready`/`PR` marks fold into the status cell's text** (e.g. `ready PR`)
  instead of being appended after the agent's name — the same information,
  now column-aligned instead of trailing free text.
- **Column widths size to content, capped, and shrink (model → archetype →
  name → status, in that priority order) when the pane is narrower than the
  ideal total** — every value is truncated rune-safely with a trailing `…`; a
  column can shrink to zero width in an extreme-narrow pane rather than
  corrupting the layout or bleeding into a neighboring pane.
- **`ContentHeight()` grows by one row** (the new column header) whenever the
  roster is non-empty, staying in exact lockstep with `Draw`'s row budget as
  before.
- **The roster SCROLLS instead of truncating** when it has more agents than
  its row budget affords. `j`/`k`/`Up`/`Down` in the focused Details region
  scroll the roster FIRST — the SAME keys the embedded plan widget already
  binds for its own stage nav — falling through to the plan widget's own
  navigation only once the roster can't move further in that direction, so
  the two never fight over a keystroke and no new focus mode was needed. The
  scroll offset is a direct, clamped `scrollOffset` (the terminal-pane idiom,
  gotchas/pty-terminal.md — not the rail's cursor-following window, since the
  roster has no selectable row), re-clamped every Draw against the live agent
  count and pane height, and preserved across a same-orchestrator refresh
  (reset only on a genuine selection change).

## Capabilities

### Modified Capabilities

- `hera-view`: the Agents roster (Details pane, coordinator selection) renders
  as an aligned, scrollable table with status/name/archetype/model columns
  instead of a bullet list that silently truncates; the PR/ready mark folds
  into the status cell.

## Impact

- **Modified code:** `internal/tui/hera/details.go` (roster rendering,
  scroll state, `ContentHeight`), `internal/tui/hera/page.go`
  (`handleDetailsKey` roster-scroll-then-plan routing), `internal/tui/hera/details_test.go`,
  `internal/tui/hera/dag_test.go`.
- **No daemon, store, MCP, or REST change.** `RoleView.Archetype` /
  `AppliedModel` were already threaded onto every role by the model-tiering
  work (`add-diligence-profiles`); this change only surfaces them in a second
  place (the coordinator roster, alongside the existing plan-view tier
  readout).
- **TUI-only, no web/macOS parity work needed.** Per `CLAUDE.md`'s Frontend
  Parity rule, a client-surface change normally triggers a three-client check —
  but hera is TUI-only for mutations AND for this read-only roster: the
  `GET /api/hera` REST payload and the web/macOS Hera tabs are unaffected (this
  change touches no REST-exposed surface). The standing gap ("hera view is
  read-only in web/macOS") already covers this; no new follow-up is needed.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.

## Why

The plugin Hera rail (the parity oracle) supported a `/` substring name filter that narrowed the rail to matching rows while preserving ancestry, so an operator could jump to a worker by name across a large, deeply-nested orchestrator tree. The native rail (`internal/tui/hera/rail.go`) — now a recursive nested tree after `rail-parity`/`rail-nesting` — has no filter at all; the only way to find a row is to scroll. This is a tracked parity regression (hera-view plugin spec, "The rail supports a `/` name filter").

This change ports the `/` filter to the native rail, adapted to the POST-nesting model: the filter must keep parent rows of any match (so the tree stays coherent) and auto-expand collapsed nodes that contain a match.

## What Changes

- **`/` enters a rail search input mode** (RAIL focus only — a focused COORD/AGENT pane forwards `/` to the PTY, respecting the focus-gating established by the ctrl+z / BUG-001 work). Typed characters build a case-insensitive substring query; whitespace-separated terms each must match (AND semantics).
- **The rail narrows to matching rows, ancestry-preserving.** An orchestrator (root or nested sub-orchestrator) stays visible when its own name matches OR any descendant role / sub-orchestrator matches, so a matching agent always keeps its parent coordinator header. A bridging worker row stays visible when it bridges a visible sub-orchestrator.
- **Collapsed nodes that contain a match auto-expand** while a filter is active (the persisted fold state is preserved and restored when the filter clears), and a section header / separator (Pinned / Freelance / Archive) renders only when it has at least one visible row beneath it.
- **`Esc` (in input mode) clears the filter and restores the full rail; `Enter` accepts** — keeping the query applied but leaving input mode so `j`/`k` navigate the filtered set and normal rail key handling (mutations, tab switches) resumes.
- **The active query is shown unobtrusively** — a `/ <query>` input line at the top of the rail while typing, and the accepted query reflected in the rail border title.
- **While in input mode the rail's mutation keys** (`w`/`r`/`a`/`s`/`S`/`P`/`Ctrl+D`/`Enter`-reattach) and the global rune shortcuts (`1`/`2`/`3`/`q`/`?`) do NOT fire; those keystrokes are filter input instead. This requires the global key handler to be filter-aware (the same `break`-on-filtering guard the task-list filter already uses).

## Capabilities

### Modified Capabilities

- `hera-view`: the rail gains a `/` substring name filter that narrows the nested tree ancestry-preserving, auto-expands collapsed nodes containing a match, prunes empty section headers, and reflects the query in the rail title; `Esc` clears, `Enter` accepts. Rail mutation keys and global rune shortcuts are suppressed while in filter input mode.

## Impact

- **Modified code:** `internal/tui/hera/rail.go` (filter state + input mode + filter-aware `buildRows` with ancestry preservation, force-expand, empty-header pruning + the `/ <query>` input line + dynamic title), `internal/tui/hera/page.go` (`/` reaches the rail via the FocusRail path; `handleRailMutation` is skipped while filtering; a `RailFiltering()` accessor), `internal/tui/app.go` (global rune-shortcut guard so `1`/`2`/`3`/`q`/`?` reach the page while the rail is filtering — mirrors the existing `a.tasklist.Filtering()` guard).
- **No schema change**, no DB change, no new dependency.
- **Specs are LOCAL DOCS only** (`openspec/project.md`). Do NOT wire `openspec validate` into Go CI or `make`; the quality gate stays `make pre-pr`. Run `openspec validate --strict` locally only.

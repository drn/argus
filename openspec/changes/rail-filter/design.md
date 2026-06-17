# Design: rail `/` name filter

## State (Rail struct, `rail.go`)

Three fields hold filter state; all default zero (no filter):

- `filterInput bool` — the rail is in search INPUT mode (typing). Drives the `/ <query>` line, the key routing, and the global-handler guard.
- `filterQuery string` — the current query text. Non-empty ⇒ the rail is NARROWED. Survives leaving input mode (Enter accepts).
- (derived) the filter is APPLIED when `filterQuery != ""`; INPUT mode is `filterInput`.

`Filtering()` (exported, read by `page.go` + `app.go`) returns `r.filterInput` — the guard predicate for "keystrokes are filter input, not commands."

## Matching

`filterMatches(name string) bool`: lowercase the name once; split `filterQuery` on whitespace (`strings.Fields`); every term must be a substring (`strings.Contains`) of the lowercased name. An all-whitespace / empty query matches everything (so an empty query never hides rows — narrowing only kicks in once a real term is typed).

Freelance rows match on their `Name` (the native `RoleView` has no repo field; the plugin's "or its repo" clause has no native analogue — note this in the delta).

## Ancestry-preserving visibility (the load-bearing part)

`buildRows` already projects the model into a nested tree via `appendOrch` / `appendOrchWorkers` / `appendWorkerRow`, using the bridge index. The filter adds a PRE-PASS that computes which orchestrators and roles are visible, then the existing append functions consult it.

Compute on each `buildRows` when `filterQuery != ""`:

- `roleVisible map[int64]bool` — a role is directly visible when `filterMatches(role.Name)`.
- `orchVisible map[int64]bool` — an orchestrator is visible when its own `Name` matches, OR any of its roles is directly visible, OR any sub-orchestrator it bridges (recursively, via the same `bridgeIndex`) is visible. Computed with a memoized recursion over the bridge tree, cycle-safe via a `visiting`/`done` set (mirror `BridgeSubtree`'s visited guard).
- A worker ROW is rendered when the role is directly visible OR it bridges a visible child orchestrator (so the bridge parent is kept for the nested match). The per-coordinator `Archive (N)` expando and its archived rows follow the same role-visibility rule; the expando header renders only if ≥1 archived role under it is visible.

Then the append functions gain a `filtered bool` mode:

- `appendOrch` skips an orchestrator whose `orchVisible[id]` is false; emits the header otherwise.
- While `filterQuery != ""`, treat every node as EXPANDED regardless of `r.collapsed[id]` / `r.coordArchiveOpen[id]` (auto-expand). The persisted fold maps are untouched, so clearing the filter restores them.
- `appendOrchWorkers` emits a worker row only when the row-visibility rule holds; recurses into a bridged child only when that child is `orchVisible`.
- Section headers (`Pinned`, `Freelance (N)`, the bottom `Archive (N)`) and the `rrRule` separators are emitted only when their following content has ≥1 visible row. Implementation: build each section's rows into a scratch slice first and append the header+rows only if the scratch is non-empty (or count visible members up front). The `(N)` count on a section/orchestrator header reflects the FULL membership, not the filtered subset (the count is an attribute of the group, not the view); only ROW visibility is filtered. [Confirm during review that this reads well; acceptable either way — keep counts stable.]

When `filterQuery == ""`, `buildRows` behaves exactly as today (no pre-pass, honor fold state).

## Key routing

`page.go` (FocusRail branch only — `/` in a focused pane already forwards to the PTY via `forwardKey`, satisfying focus-gating):

- `handleRailMutation` returns `false` immediately when `p.rail.Filtering()` — so `w`/`r`/`a`/`s`/`S`/`P`/`Ctrl+D`/`Enter` are NOT consumed as mutations while typing; they fall through to `rail.InputHandler`, which treats them as filter text / control keys.
- No new key wiring in `page.go` for `/` itself: the rail's `InputHandler` owns `/` (enter input mode) and all input-mode keys.

`rail.go` `InputHandler`:

- When `filterInput`:
  - `Esc` → clear (`filterInput=false`, `filterQuery=""`), rebuild, clamp. (Restores full rail.)
  - `Enter` → accept (`filterInput=false`, keep `filterQuery`), rebuild, clamp. `j`/`k` now navigate the filtered set; mutations resume.
  - `Backspace` / `Backspace2` → trim the last rune of `filterQuery`, rebuild.
  - `Rune` → append the rune to `filterQuery`, rebuild.
  - All other keys: swallowed (no-op) so nothing leaks while typing.
- When NOT `filterInput`:
  - `/` (Rune) → enter input mode (`filterInput=true`), PRESERVING the current `filterQuery` so re-opening `/` lets the operator edit/extend the accepted query (then `Esc` clears it). Does NOT rebuild (query unchanged).
  - existing nav (`j`/`k`/↑/↓/Space) unchanged.

Every query mutation calls `buildRows` then `restoreCursor`(prev ref)+`clampCursor` so the cursor lands on a visible selectable row.

`app.go` `handleGlobalKey`, in the `KeyRune` block (next to the existing task-list-filter and settings-editing guards):

```go
if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabHera && a.heraPage.RailFiltering() {
    break // rune keys are filter input — don't run 1/2/3/q/? global shortcuts
}
```

`RailFiltering()` on `HeraPage` delegates to `p.rail.Filtering()` (false in remote mode — the rail still exists but is never focused; safe regardless).

## Rendering (`Draw`)

- While `filterInput`: draw a top input line `/ <query>` (with a trailing cursor block) inside the panel at `inner.Y`, then render rows starting at `inner.Y+1` over `inner.H-1` rows (the existing `adjustOffset` takes the reduced height). Style: `theme.StyleSelected` or `StyleTitle` for the prompt.
- Border title: ` Hera ` normally; ` Hera /<query> ` when `filterQuery != ""` (accepted or typing). The title is passed to `widget.DrawBorderedPanel`. Keep it short; truncate the query in the title if the panel is narrow (reuse the existing rune-safe truncation discipline — never slice bytes mid-rune).
- No `screen.Sync()` anywhere (CLAUDE.md UX-rendering rules) — `tview.Clear()` + full-rect `DrawBorderedPanel` coverage handles stale cells, as today.

## Tests (TDD, `rail_test.go` / `page_test.go`)

- Match semantics: single term, multi-term AND, case-insensitivity, empty query = all rows.
- Ancestry: a query matching only a deeply-nested worker keeps its parent coordinator header (and intermediate bridging rows) visible and expanded; a non-matching sibling subtree is hidden.
- Auto-expand: a collapsed orchestrator containing a match renders expanded while filtered; the `collapsed` map is unchanged after clearing the filter (fold state restored).
- Empty-header pruning: the Freelance / Archive headers and `rrRule` separators do not render when no member matches.
- `Esc` clears (full rail returns, query empty, input off); `Enter` accepts (query stays, input off, `j`/`k` move within filtered set).
- Mutation suppression: a SimulationScreen smoke test asserting `w`/`a`/`P`/`Enter` typed while `filterInput` append to the query and do NOT fire the mutation callbacks; and that `app.go`'s global guard lets `1`/`2`/`q` reach the rail as filter input (tab does not switch while filtering).
- `/` focus-gating: `/` while a pane is focused forwards to the PTY (does not enter filter mode).
- Title + input-line render via SimulationScreen (`simApp`/`wireApp`).

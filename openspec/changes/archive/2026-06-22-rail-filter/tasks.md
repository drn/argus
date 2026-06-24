# Tasks: rail-filter

One commit for the whole change so it can ship as its own PR (separate from
rail-state-persist). Tests land with the code (TDD: red → green). `make pre-pr`
must pass before pushing.

## 1. Filter state + matching

- [ ] 1.1 Add `filterInput bool` and `filterQuery string` to the `Rail` struct; add an exported `Filtering() bool` returning `filterInput`.
- [ ] 1.2 Add `filterMatches(name string) bool` (lowercase once, `strings.Fields` on the query, every term a `strings.Contains` substring; empty query matches all).
- [ ] 1.3 Tests: match semantics — single term, multi-term AND, case-insensitivity, empty query = all.

## 2. Ancestry-preserving, filter-aware buildRows

- [ ] 2.1 Add the visibility pre-pass: `roleVisible` (direct name match) + `orchVisible` (own name OR any role OR any bridged sub-orchestrator visible, memoized + cycle-safe like `BridgeSubtree`).
- [ ] 2.2 Make `appendOrch`/`appendOrchWorkers`/`appendWorkerRow` filter-aware: skip non-visible orchestrators/roles; render a bridging worker row when it bridges a visible child; force-expand (ignore `collapsed`/`coordArchiveOpen`) while `filterQuery != ""` WITHOUT mutating the fold maps.
- [ ] 2.3 Prune empty section headers (`Pinned`, `Freelance (N)`, bottom `Archive (N)`) and their separator rules — render only when ≥1 visible row follows.
- [ ] 2.4 Confirm `filterQuery == ""` keeps `buildRows` byte-for-byte equivalent to today (honor fold state, no pre-pass).
- [ ] 2.5 Tests: ancestry preservation (nested-match keeps parents + bridging rows, expanded); auto-expand + fold-state-restored-on-clear; empty-section pruning.

## 3. Key routing

- [ ] 3.1 `rail.InputHandler`: in `filterInput`, route `Esc` (clear), `Enter` (accept), Backspace/Backspace2 (trim rune), Rune (append), swallow the rest; rebuild + restore + clamp on every query change. Not-in-input: `/` enters input mode preserving the query.
- [ ] 3.2 `page.handleRailMutation`: return `false` at the top when `p.rail.Filtering()` so mutation keys fall through to the rail as filter input.
- [ ] 3.3 `page.go`: add `RailFiltering() bool` → `p.rail.Filtering()`.
- [ ] 3.4 `app.go` `handleGlobalKey`: in the `KeyRune` block add a guard `if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabHera && a.heraPage.RailFiltering() { break }` so `1`/`2`/`3`/`q`/`?` reach the page while filtering.
- [ ] 3.5 Tests: `Esc`/`Enter` behavior; mutation-suppression smoke test (typing `w`/`a`/`P`/`Enter` while filtering appends, no callback); global-guard smoke test (`1`/`q` reach the rail, tab does not switch); `/` in a focused pane forwards to PTY (no filter mode).

## 4. Rendering

- [ ] 4.1 `Draw`: render the `/ <query>` input line at the top while `filterInput` (rows shift down one, viewport height -1); reflect the query in the border title (` Hera /<query> `) when `filterQuery != ""`. Rune-safe truncation; no `screen.Sync()`.
- [ ] 4.2 Tests: SimulationScreen — input line renders while typing; title shows the accepted query; no marker/content shift regressions.

## 5. Docs

- [ ] 5.1 Add the `/` filter row to the Hera rail section of the help modal (`internal/tui/modal/help.go`) + assert it in `help_test.go`; mirror into the README Reference keybinding table (per CLAUDE.md keybinding rule).
- [ ] 5.2 Add the non-obvious gotchas (filter force-expands without mutating fold maps; global-handler must be filter-aware or `1`/`2`/`q` leak; `/` is FocusRail-only) to `context/knowledge/gotchas/hera-view.md`.

# Tasks: rail-state-persist (BUG-002)

One commit for the whole change so it can ship as its own PR, STACKED on
rail-filter (both touch rail.go). Tests land with the code (TDD: red → green).
`make pre-pr` must pass before pushing.

## 1. DB seam

- [ ] 1.1 Add `internal/db/hera_rail_state.go`: `railStateConfigKey = "hera.rail_view_state"`, `LoadRailState() (string, error)` (wraps `GetConfigValue`), `SaveRailState(string) error` (wraps `SetConfigValue`).
- [ ] 1.2 Tests: `LoadRailState` returns "" when absent; round-trips a saved value; stores under the `hera.rail_view_state` key.

## 2. Rail persistence seam

- [ ] 2.1 Define `RailStateStore` interface (`LoadRailState`/`SaveRailState`) and `railViewState` JSON struct in `rail.go`; add `store RailStateStore` and `pendingSelRef int64` fields.
- [ ] 2.2 `SetStateStore(s)`: record the store, load + parse the blob, populate `collapsed`/`coordArchiveOpen`/`freelanceCollap`/`archiveCollapsed`, stash `pendingSelRef`. Tolerate empty/malformed (keep defaults; log via uxlog).
- [ ] 2.3 `persist()`: build `railViewState` from live maps + `currentRef()`, marshal, `store.SaveRailState`; nil store = no-op; errors logged not fatal.
- [ ] 2.4 Call `persist()` from `ToggleCollapse` and `setCursor`. Apply `pendingSelRef` once in `SetModel` (after `buildRows`, then zero it).
- [ ] 2.5 Tests: restore (collapsed ids, expando ids, section bools, selection after SetModel); one-shot selection; save-on-toggle + save-on-cursor-move; nil store no-op; defaults from malformed blob.

## 3. Page + app wiring

- [ ] 3.1 `HeraPage.SetRailStateStore(s)` → `p.rail.SetStateStore(s)`.
- [ ] 3.2 `app.go`: after `NewHeraPage`, in the existing `if d, ok := a.db.(*db.DB); ok` block, call `a.heraPage.SetRailStateStore(d)`.
- [ ] 3.3 Tests: SimulationScreen smoke — a fold + selection on the Hera tab persists through a fresh page constructed against the same fake store (restore round-trip).

## 4. Filter interaction (depends on rail-filter landing first)

- [ ] 4.1 Confirm filter rebuilds move the cursor via `clampCursor` (direct write), NOT `setCursor`, so `/` filtering never persists. Add a test capturing zero writes on a filter-only rebuild.

## 5. Docs

- [ ] 5.1 Add the persistence gotchas (selection restore is one-shot; filter is transient/never persisted; save is synchronous-but-cheap on the UI thread, async-able since the rail hands a pre-serialized string; remote = nil store = off) to `context/knowledge/gotchas/hera-view.md`.
- [ ] 5.2 No keybinding/help change (no new key) and no README change (behavior, not a documented fact surface) — verify and note.

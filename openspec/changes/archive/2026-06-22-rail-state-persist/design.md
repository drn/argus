# Design: rail state persistence (BUG-002)

## Storage seam

A narrow interface in package `hera`, satisfied implicitly by `*db.DB`:

```go
// RailStateStore persists the rail's UI state across restarts. *db.DB
// satisfies it; remote mode passes nil (persistence off).
type RailStateStore interface {
    LoadRailState() (string, error)   // "" (no error) when absent
    SaveRailState(state string) error
}
```

`internal/db/hera_rail_state.go` implements both as thin wrappers over the existing
`config` key-value table (the same table behind `GetConfigValue`/`SetConfigValue`),
under the constant key `hera.rail_view_state`:

```go
const railStateConfigKey = "hera.rail_view_state"
func (d *DB) LoadRailState() (string, error) { return d.GetConfigValue(railStateConfigKey) }
func (d *DB) SaveRailState(state string) error { return d.SetConfigValue(railStateConfigKey, state) }
```

No new table, no migration — the `config` table already exists and survives daemon
restart / crash (it is on disk in `data.sql`).

## Serialized shape

```go
type railViewState struct {
    Collapsed        []int64 `json:"collapsed"`           // orchestrator ids currently collapsed
    CoordArchiveOpen []int64 `json:"coord_archive_open"`  // orchestrator ids whose per-coord Archive expando is open
    FreelanceCollap  bool    `json:"freelance_collapsed"`
    ArchiveCollapsed bool    `json:"archive_collapsed"`
    SelectionRef     int64   `json:"selection_ref"`       // the rail's currentRef() identity (role id, or -orch id)
}
```

Only the NON-default entries of the fold maps are listed: `collapsed[id]==true` and
`coordArchiveOpen[id]==true`. Defaults (orchestrators expanded, expandos closed) are
implicit by absence, so the blob stays small and forward-compatible. `FreelanceCollap`
default false, `ArchiveCollapsed` default true (matches `NewRail`).

## Wiring (app.go)

Mirror the existing `heraReader` / `heraOps` local-only type-assert:

```go
if d, ok := a.db.(*db.DB); ok {
    a.heraPage.SetRailStateStore(d) // loads persisted state immediately
}
```

`HeraPage.SetRailStateStore(s)` calls `p.rail.SetStateStore(s)`. Remote mode never
type-asserts to `*db.DB`, so the seam stays nil and persistence is off.

## Load (rail.go)

`Rail.SetStateStore(s RailStateStore)`:

1. record `r.store = s`.
2. `raw, err := s.LoadRailState()`; on error or empty, leave defaults (log via `uxlog`).
3. parse the JSON; populate `r.collapsed` / `r.coordArchiveOpen` from the id lists,
   set `r.freelanceCollap` / `r.archiveCollapsed`, and stash `r.pendingSelRef =
   state.SelectionRef` (applied after the first build — rows don't exist yet).

`SetStateStore` is called once at construction time (before the first `Refresh`), so
the fold maps are in place before the first `buildRows`. A malformed blob is tolerated
(unmarshal error ⇒ keep defaults) per the no-migration policy.

## Restore selection

`SetModel` already calls `buildRows` → `restoreCursor(prev)` → `clampCursor`. Add: when
`r.pendingSelRef != 0`, after `buildRows`, call `restoreCursor(r.pendingSelRef)` then
`clampCursor`, then ZERO `pendingSelRef` (one-shot — subsequent rebuilds keep the live
cursor, not the stale persisted ref). `restoreCursor` already maps a ref (role id or
`-orch.ID`) back to a row, so no new lookup logic is needed.

## Save (rail.go)

`r.persist()`: when `r.store == nil`, no-op. Otherwise build a `railViewState` from the
live maps + `r.currentRef()`, `json.Marshal`, and `r.store.SaveRailState(string(b))`.
Errors are logged via `uxlog`, never fatal.

Call `r.persist()` from the two user-driven state-change sites:

- `ToggleCollapse` (after the fold flip + rebuild) — fold + selection.
- `setCursor` (selection move; already the single funnel for cursor changes via `step`).

Guards so persistence never fires spuriously:

- The initial restore path (`restoreCursor`) writes `r.cursor` directly and does NOT
  call `setCursor`, so loading never triggers a save.
- Filter rebuilds move the cursor via `clampCursor` (direct write), not `setCursor`, so
  filter state changes are NOT persisted (filter is transient by design).
- `pendingSelRef` is consumed once, so a persisted selection that was filtered out at
  load time simply clamps to a visible row and the next move re-persists.

Local SQLite `INSERT OR REPLACE` is sub-millisecond and these are infrequent
UI-thread actions (a fold toggle, a cursor step), so synchronous on the tview thread is
fine — matching how the task list persists pinned/archive state. (If key-repeat j/k
ever shows latency, the App can wrap `SaveRailState` async since the rail hands it a
pre-serialized immutable string — note this seam in the gotcha, but ship synchronous.)

## Tests (TDD)

- `internal/db`: `LoadRailState` returns "" when absent; round-trips a `SaveRailState`
  value; uses the `hera.rail_view_state` key (assert via `GetConfigValue`).
- `rail.go`: `SetStateStore` with a fake store restores collapsed ids, expando ids,
  section booleans, and (after a `SetModel`) the selection; defaults survive a
  malformed/empty blob.
- save-on-change: a fake store captures writes; `ToggleCollapse` and a `CursorDown`
  each persist; the persisted JSON reflects the new fold/selection; `nil` store = no
  panic, no write.
- restore is one-shot: a second `SetModel` after a live cursor move does NOT snap back
  to the persisted ref.
- filter rebuilds do NOT persist (no write captured when only the query changes).
- remote-mode parity: nil store ⇒ rail fully functional, zero persistence calls.

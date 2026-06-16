## Why

The native Hera rail (`internal/tui/hera/rail.go`) holds all of its UI state in memory — the per-orchestrator fold map (`collapsed`), the per-coordinator Archive expandos (`coordArchiveOpen`), the Freelance / bottom-Archive section folds (`freelanceCollap`/`archiveCollapsed`), and the cursor selection. `NewRail` initializes these fresh on every launch, so when argus (or the daemon) restarts and the operator returns to the Hera tab, every fold reopens/recloses to defaults and the selection resets. BUG-002: Aaron — "would be nice if it survived TUI restarts."

This is an enhancement (the plugin's rail state was also in-memory; this is a net improvement, not a regression). It is queued behind `rail-nesting`/`rail-parity` because it must serialize the POST-nesting fold/selection model — per-node fold state keyed by stable orchestrator id, and selection keyed by role/orch id.

## What Changes

- **Persist the rail UI state to the argus daemon DB** (`~/.argus/data.sql`) via the existing `config` key-value table under one key (`hera.rail_view_state`), serialized as JSON. The DB is the natural home (it already survives daemon restart / crash, and the rail's identifiers are stable DB primary keys). No new table — a single config row.
- **Serialize the POST-nesting model:** the set of collapsed orchestrator ids, the set of open per-coordinator Archive expando ids, the Freelance and bottom-Archive section fold booleans, and the selection (the rail's existing stable `currentRef()` identity — role id, or negated orchestrator id for a header).
- **Restore on rail construction:** fold maps and section booleans are applied immediately (keyed by stable ids, valid before any model loads); the selection ref is applied after the first model build (rows must exist), reusing the rail's existing `restoreCursor` machinery.
- **Save on change:** every fold toggle and selection move writes the serialized state through a narrow, injected store seam. Local SQLite writes are sub-millisecond; remote mode (no `*db.DB`) leaves the seam nil and persistence is simply off (the rail still works, just unpersisted — matching the existing remote-mode hera degradation).

## Capabilities

### Modified Capabilities

- `hera-view`: the rail's fold/collapse state (per-orchestrator, per-coordinator Archive expandos, Freelance and bottom-Archive sections) and selection are persisted to the daemon DB and restored on relaunch, surviving a daemon restart / crash. Filter state is transient and is NOT persisted.

## Impact

- **Modified code:** `internal/tui/hera/rail.go` (a `RailStateStore` seam, `railViewState` JSON struct, load-on-store-set, save-on-change, pending-selection-ref restore), `internal/tui/hera/page.go` (a `SetRailStateStore` passthrough), `internal/db/` (a `hera_rail_state.go` with `LoadRailState()`/`SaveRailState(string)` thin wrappers over the existing `config` table), `internal/tui/app.go` (wire the store to the page in local mode only, mirroring the `heraReader` / `heraOps` `*db.DB` type-assert).
- **No schema change** — reuses the existing `config` key-value table (one new key, `hera.rail_view_state`). Per the repo's no-legacy-migration policy, a malformed / absent value is tolerated (fall back to defaults), never migrated.
- **Specs are LOCAL DOCS only** (`openspec/project.md`). Do NOT wire `openspec validate` into Go CI or `make`; the quality gate stays `make pre-pr`. Run `openspec validate --strict` locally only.

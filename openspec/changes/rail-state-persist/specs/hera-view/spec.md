# hera-view delta: rail state persistence (BUG-002)

## ADDED Requirements

### Requirement: The rail persists its fold and selection state across restarts

The system SHALL persist the Hera rail's UI state to the argus daemon database and
restore it when the rail is reconstructed, so the operator's view survives an argus /
TUI restart and a daemon restart or crash. The persisted state SHALL include:

- the set of COLLAPSED orchestrators (per-node fold state in the post-nesting tree),
- the set of OPEN per-coordinator `Archive (N)` expandos,
- the Freelance-section and bottom-Archive-section fold booleans, and
- the current SELECTION (the rail's stable row identity — role id, or the negated
  orchestrator id for a coordinator header).

Persistence SHALL use the existing `config` key-value table under a single key
(`hera.rail_view_state`), serialized as JSON — NO new table and NO schema migration.
Only non-default fold entries SHALL be serialized (orchestrators default expanded,
expandos default closed), so an absent or empty value restores all defaults.

Restore SHALL apply the fold maps and section booleans immediately on construction
(they key off stable database ids, valid before any model is loaded) and SHALL apply
the selection after the first model build, reusing the rail's existing cursor-restore
machinery. The selection restore SHALL be ONE-SHOT: once applied (or once the operator
moves the cursor), later rebuilds keep the live cursor, not the stale persisted ref.

Saving SHALL occur on every fold toggle and selection move. A malformed or absent
persisted value SHALL be tolerated (fall back to defaults), never fatal. In remote mode
(no local database) the persistence seam SHALL be absent and the rail SHALL operate
normally without persisting. Transient FILTER state (the `/` query and input mode) is
NOT part of the persisted state, and a filter-driven rebuild SHALL NOT trigger a save.

Derived from: `internal/tui/hera/rail.go` (`RailStateStore` seam, `railViewState`,
load-on-store-set, one-shot pending-selection restore, save-on-change), `internal/db/hera_rail_state.go`
(`LoadRailState`/`SaveRailState` over the `config` table), `internal/tui/app.go`
(local-only `*db.DB` wiring).

#### Scenario: Fold and selection survive a restart

- **WHEN** the operator collapses some orchestrators, opens a per-coordinator Archive expando, folds the Freelance section, and selects a row, then argus restarts and reopens the Hera tab
- **THEN** the rail MUST restore exactly those collapsed orchestrators, that open expando, the Freelance fold, and the selected row

#### Scenario: Defaults restore from an absent or malformed value

- **WHEN** no persisted rail state exists, or the stored value is malformed
- **THEN** the rail MUST fall back to its defaults (orchestrators expanded, expandos closed, Freelance expanded, Archive collapsed) without error

#### Scenario: Selection restore is one-shot

- **WHEN** the persisted selection is applied on the first model build and the operator then moves the cursor
- **THEN** a subsequent model rebuild MUST keep the live cursor and MUST NOT snap back to the persisted selection

#### Scenario: Remote mode operates without persistence

- **WHEN** the rail runs in remote mode (no local database seam)
- **THEN** the rail MUST function normally and MUST NOT attempt to load or save rail state

#### Scenario: Filter changes are not persisted

- **WHEN** the operator activates or clears the `/` name filter (which rebuilds the rail)
- **THEN** no rail-state save MUST be triggered by the filter change (filter state is transient)

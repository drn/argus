# hera-coordination delta: two-state archive/nuke representation

## ADDED Requirements

### Requirement: Two-state end-of-life representation (hidden vs nuked) in the store

The hera store SHALL represent two distinct, additive end-of-life states on BOTH `hera_orchestrators` and `hera_roles`, layered on the existing `archived_at` marker with a second nullable `nuked_at TEXT` column:

- **ACTIVE** — `archived_at IS NULL AND nuked_at IS NULL`.
- **HIDDEN (Tier 1)** — `archived_at` set, `nuked_at IS NULL`. The row is reversibly archived; the rail nests it in the parent coordinator's archive expando. Clearing `archived_at` (unhide) restores it.
- **NUKED (Tier 2)** — `nuked_at` set (and `archived_at` set, so the row also leaves the partial active-name unique index and frees its name for reuse). The rail omits the row entirely; the row, its inbox, and its argus task remain retrievable from the DB.

The store SHALL expose `NukeHeraRole(id)` and `NukeHeraOrchestrator(id)` that stamp `nuked_at` (idempotently, preserving an existing value) and ensure `archived_at` is set, clearing `pinned_at`. Every list/lookup that feeds the rail (`ListHeraOrchestrators`, `ListHeraRoles`) SHALL exclude `nuked_at`-stamped rows; primary-key lookups by id MAY still return them (so recovery tooling can read them). The active-name uniqueness lookups SHALL treat a nuked row as not occupying the name (it is archived).

This representation is the storage substrate for the Hera-view two-state EOL keys (`a` hide, `Ctrl+D`/`C` nuke). NO end-of-life path SHALL hard-delete a hera row: nuke stamps `nuked_at`, it never calls `DeleteHeraRole` / `DeleteHeraOrchestrator`, and message rows are never deleted.

Derived from: `internal/db/schema.go` (`nuked_at` column + idempotent ALTER), `internal/db/hera.go` (`NukeHeraRole`, `NukeHeraOrchestrator`, nuked-aware list/scan), `internal/tui/hera/model.go` (BuildModel skips rows with `nuked_at` set).

#### Scenario: Nuke stamps the marker and frees the name

- **WHEN** `NukeHeraRole` is called on an active role
- **THEN** the role's `nuked_at` and `archived_at` are stamped, its `pinned_at` cleared, and a later `CreateHeraRole` with the same (orchestrator, name) succeeds because the nuked row no longer occupies the active-name index

#### Scenario: Rail-feeding lists exclude nuked rows but id lookups do not

- **WHEN** an orchestrator or role is nuked
- **THEN** `ListHeraOrchestrators`/`ListHeraRoles` omit it (so BuildModel never renders it), while `HeraOrchestrator(id)`/`HeraRole(id)` still return it for recovery

#### Scenario: Nuke is reversible only via the DB, never a hard delete

- **WHEN** any end-of-life path nukes a row
- **THEN** the row is stamped (not deleted), its bindings/inbox/status/argus-task rows survive, and no `DeleteHeraRole`/`DeleteHeraOrchestrator`/message delete is issued

# Pin non-root Hera rail items, rendered with a lineage breadcrumb

## Why

The native Hera rail (`internal/tui/hera`) can pin only top-level coordinators (orchestrator roots). The `P` mutation path already accepts a non-root role end-to-end — `Ops.PinToggle` (`ops.go:128`) handles roles and `db.PinHeraRole` (`hera.go:484`) stamps `pinned_at` — but the read projection drops it: `Model.Pinned` is `[]OrchView` only (`model.go:145`) and the rail's Pinned section renders only orchestrators (`rail.go:521-526`). So pinning a worker / agent / sub-coordinator silently writes the DB and renders nothing as pinned. The out-of-tree Hera plugin already solved this (`anutron/hera`, BUG-025 + BUG-021): a pinned non-root item floats to the Pinned block as a two-line entry with a lineage breadcrumb. This change ports that behavior to native and fixes the silent-failure path.

## What Changes

- Project per-role pin state into the read model: add `RoleView.Pinned` (set in `BuildModel` from `hera_roles.pinned_at`).
- Render pinned non-root roles in the rail's Pinned section as a **two-line breadcrumb entry**: a selectable line 1 (dimmed status icon + full lineage trail `root › sub ›`, left-truncated so the nearest parent stays visible) and a non-selectable line 2 (the role name + age).
- Float a pinned role OUT of its parent subtree (single placement); a pinned **sub-coordinator** hoists its **whole subtree** into the Pinned block (full plugin parity).
- Compute the breadcrumb lineage from native's existing `canonicalParents()` chain so the trail matches how the rail actually nests.
- Fix the live silent-failure bug: pinning a non-root role now renders coherently end-to-end.
- The Pinned section header now appears when pinned orchestrators **or** pinned non-root roles exist (today: orchestrators only).

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `hera-view` — the "Rail sections — Pinned, Active, Freelance, Archive (area 2)" requirement changes (Pinned appears for pinned roles too), plus new pinned-role rendering / single-placement / sub-coordinator-hoist / breadcrumb behavior.

## Impact

- Code: `internal/tui/hera/model.go` (`RoleView.Pinned`, `buildRoleView`), `internal/tui/hera/rail.go` (new `rrPinnedBreadcrumb` row kind + `breadcrumb`/`breadcrumbCont` fields, `collectPinnedRoles`, float-skip in `appendOrchWorkers`, sub-coord subtree hoist, two-line draw path, cursor/selectable/restore handling).
- No DB schema change (`pinned_at` already exists on `hera_roles`), no new keybinding (`P` already bound + documented), no `railViewState` change (pin is DB-backed; cursor restore already keys on role id).
- Tests: `internal/tui/hera/*_test.go` (model projection, rail render, single-placement, sub-coord hoist, breadcrumb truncation, filter/collapse states, cursor anchoring) + a SimulationScreen smoke test for the pin→float path.
- Docs: gotcha bullets in `context/knowledge/gotchas/hera-view.md`. Help modal + README untouched (no key change).

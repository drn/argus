## Why

The merge-safety review popup's global Cleanup action (`add-merge-safety-review`)
lists the entire cross-project stuck-task backlog as a FLAT NOT-SAFE/SAFE list.
Many of the ~737 real-world candidates are bare Hera role names like
`1a-build` — meaningless without knowing which coordinator/orchestrator effort
they belonged to. Hera never hard-deletes role/binding/orchestrator rows on
archive or nuke, so that context is still reconstructible from the DB; the
popup just never surfaced it.

## What Changes

- **`db.StuckTaskCandidates` now resolves each candidate's originating Hera
  orchestrator.** A correlated subquery over `hera_bindings`/`hera_roles`/
  `hera_orchestrators`, keyed on the task id and ordered by
  `hera_bindings.started_at DESC`, picks the MOST RECENT orchestrator a task
  ever held a role in — reaching back through ended, archived, and nuked rows
  alike. A task that never held a Hera role resolves to an empty
  orchestrator name. Return type changes from `[]*model.Task` to
  `[]*db.StuckTaskCandidate` (task + orchestrator name).
- **The REST wire contract (`GET /api/maintenance/cleanup-candidates`) gains
  an `orchestrator` field** on each candidate row, omitted when empty.
- **The merge-safety review popup renders a TREE, not a flat list.** Within
  each existing NOT-SAFE/SAFE/PENDING section, candidates carrying the same
  orchestrator name are grouped under one coordinator group header (mirroring
  the native Hera rail's own orchestrator-header visual language — an icon
  plus the name, no fixed label word); candidates with no orchestrator
  (non-Hera stuck tasks) still render as flat top-level rows, never nested
  under a fabricated header. The NOT-SAFE-before-SAFE section ordering is
  unchanged — grouping is a sub-structure within each section, not a
  replacement for it.
- **The single-role nuke popup is unaffected in practice.** Its sole
  candidate is always the task being nuked right now (not a backlog
  reconstruction), so it renders flat exactly as before; the widget itself
  makes no distinction between the two call sites beyond the data it's given.

## Capabilities

### Modified Capabilities

- `hera-view`: the merge-safety review popup's global Cleanup action now
  groups candidates by their originating Hera coordinator/orchestrator
  instead of rendering one flat list.

## Impact

- `internal/db/tasks.go`, `internal/db/schema.go` (new supporting index)
- `internal/api/cleanup_candidates.go`
- `internal/tui/mergesafety.go`, `internal/tui/mergesafetypopup.go`
- Tests in each of the above packages
- `context/knowledge/gotchas/hera-view.md`

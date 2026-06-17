## Why

Freelancer rendering is being dropped from the Hera view (#2). The operator model is being split: the Hera view shows coordinated (hera-managed) workers, and the Tasks tab (view #1) becomes the home for freelancers — tasks running without hera coordination.

To make the Tasks tab usable as a freelancer surface, it needs a way to hide every hera-managed agent and show only freelancers. The Tasks tab today has a related but weaker toggle (`H`, "show/hide hera workers") that hides only *worker*-kind rows and derives membership from the `task_meta` `hera` sidecar — which is stamped on spawn/join but never cleared when a binding ends, so it misclassifies finished workers and ignores coordinators entirely. This change adds an authoritative, binding-derived "freelancers-only" filter.

## What Changes

- Add a **`f` ("freelancers only") toggle** to the Tasks tab. When active, the task list shows ONLY freelancer tasks and hides every hera-managed task. Pressing `f` again restores the full list. Default OFF.
- Define the predicate authoritatively from the **hera DB bindings/roles**, not the `task_meta` sidecar: a task is **hera-managed** when it holds at least one *live* binding (`ended_at IS NULL`) to a role of kind `coordinator` or `worker`; a task is a **freelancer** when it has no live binding, or only `freelance`-kind live bindings.
- Surface the active filter in the panel title (an obvious `freelancers only` indicator) so it is never ambiguous that the list is filtered. This composes with, and renders distinctly from, the existing `/` substring filter.
- Add a single-query DB helper that returns the set of managed task IDs (`SELECT DISTINCT argus_task_id … JOIN hera_roles … WHERE ended_at IS NULL AND kind IN ('coordinator','worker')`), so the filter costs one read per refresh, not one per task.
- Keep the change **additive and non-invasive**: the existing `H` worker-hide toggle, `/` filter, sections, cursor, and row composition are untouched. The new toggle is an orthogonal exclusion applied in the same `buildRows` filter pass. When `f` is on it is a strict superset of `H`'s hiding, so `H` becomes a no-op while `f` is active — documented, not enforced.

## Capabilities

### Modified Capabilities

- `task-list-view`: Adds the "freelancers-only" filter toggle, its binding-derived managed/freelancer predicate, and the title indicator. Leaves the existing substring filter, sections, and navigation requirements unchanged.

## Impact

- **New code:** `internal/db/hera.go` (`ManagedTaskIDs()` live-binding query); `internal/tui/taskview/tasklist.go` (`freelancersOnly` field, `SetManagedTasks`/`ToggleFreelancersOnly`, `f` key, buildRows filter, title indicator).
- **Modified code:** `internal/tui/app.go` (compute the managed set on the refresh tick — type-assert to `*db.DB` for the authoritative query in local mode, fall back to the `task_meta` union in `--remote` mode); `internal/tui/modal/help.go` + `help_test.go` (help overlay entry); `README.md` Reference appendix keybinding table.
- **Dependencies:** none.
- **Data:** none — read-only over existing `hera_bindings`/`hera_roles`.
- **Remote mode:** `--remote` has no REST endpoint for the binding query, so the managed set falls back to the `task_meta` `hera` union already read by `readHeraRoles()` — best-effort parity, documented as a known limitation.

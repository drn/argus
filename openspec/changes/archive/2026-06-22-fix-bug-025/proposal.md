## Why

The Tasks tab grew TWO overlapping hera-visibility controls that confuse operators (BUG-025):

- `H` ("show/hide hera workers", default ON) hides only *worker*-kind rows, derived from the `task_meta` `hera.role=worker` sidecar (permanent, stamped at spawn, never cleared).
- `f` ("freelancers only", default OFF) hides *every* hera-managed task (live coordinator- or worker-kind binding), derived from the live `hera_bindings` store.

Two keys, two data sources, two defaults, overlapping effect. The product decision is to collapse them into a single control: `H` is the one hera-visibility toggle, and it hides everything that lives in the Hera tab — spawned workers AND live coordinators — while freelancers and plain non-hera tasks stay visible.

## What Changes

- **Remove the `f` "freelancers-only" filter entirely** — the key handler, the `freelancersOnly`/`OnFreelancersOnlyToggle` state, the title indicator, and its dedicated tests. This reverses the (un-archived) `freelancer-filter-view1` requirement.
- **Extend `H`** so its predicate covers the UNION: hide a task if it is a hera-spawned worker (`task_meta` `hera.role=worker`) OR holds a live coordinator/worker binding (the signal the removed `f` consumed via `SetManagedTasks`/`ManagedTaskIDs()`). The `managed` feed is folded into the `H` predicate rather than driving a separate toggle. `H` stays default ON, still bound to `H`.
- Rename the field/getter/toggle for clarity: `hideHeraWorkers` → `hideHeraManaged`, `HideHeraWorkers()`/`ToggleHeraWorkers()`/`OnHeraWorkersToggle` → `HideHeraManaged()`/`ToggleHeraManaged()`/`OnHeraManagedToggle`.
- Update the `H` help-overlay entry and README keybinding row; drop the `f` rows.

## Capabilities

### Modified Capabilities

- `task-list-view`: Removes the freelancers-only filter requirement and adds a unified hide-hera-managed (`H`) toggle whose predicate is the union of the spawned-worker sidecar signal and the live coordinator/worker binding signal.

## Impact

- **Modified code:** `internal/tui/taskview/tasklist.go` (rename field/methods, fold `managed` into the `H` predicate, drop `freelancersOnly`/`ToggleFreelancersOnly`/`FreelancersOnly`/`OnFreelancersOnlyToggle` and the title indicator, drop the `f` key handler); `internal/tui/app.go` (rename callback wiring, drop the `f` wiring); `internal/tui/modal/help.go` + `help_test.go`; `README.md` Reference appendix.
- **Removed tests:** `internal/tui/taskview/freelancers_only_test.go`, `TestSmoke_FreelancersOnlyFilter`.
- **Retained:** `db.ManagedTaskIDs()` and `SetManagedTasks` — still fed every tick, now consumed by the `H` predicate.
- **Dependencies / data:** none — read-only over existing `hera_bindings`/`hera_roles` plus the `task_meta` `hera` sidecar.
- **Remote mode:** `--remote` still falls back to the `readHeraRoles()` union for the managed set (possibly stale until the next tick) — unchanged degradation.

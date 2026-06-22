# Design — freelancers-only filter on the Tasks tab

## Context

The Tasks tab (`internal/tui/taskview/tasklist.go`) flattens `[]*model.Task` into a row list grouped by project, partitioned into Pinned / Active / Archive sections. A single `buildRows()` pass applies all exclusions:

- `matchesFilter(t)` — the `/` substring filter (`filter`, `filtering` fields).
- `hideHeraWorkers && isHeraSpawnedWorker(t)` — the `H` toggle, keyed off `heraWorkers map[string]bool` fed from the `task_meta` `hera` sidecar by `readHeraRoles()` in `app.go`.

The new "freelancers-only" filter slots into this same pass as one more exclusion, governed by a new managed-task set.

## Decision 1 — Hotkey: `f`

`f` is unbound in the tasklist `InputHandler`. It is mnemonic ("freelancers"). `ctrl+f` (fork) is a distinct tcell event and does not collide. The file panel's `f` ("reveal in Finder") is a different widget, so there is no conflict.

`f` toggles `freelancersOnly` on/off (binary), rebuilds rows, clamps the cursor, and fires a log-only callback (mirroring `OnHeraWorkersToggle`).

## Decision 2 — Managed/freelancer predicate from live bindings (NOT task_meta)

Authoritative predicate, confirmed against `internal/db/hera.go` + `internal/db/schema.go`:

- **Managed** — the task holds ≥1 binding with `ended_at IS NULL` to a role whose `kind` is `coordinator` or `worker`.
- **Freelancer** — everything else: no live binding at all, or only `freelance`-kind live bindings.

Why not reuse `readHeraRoles()` / the `task_meta` `hera` sidecar (the existing `H` feed)?

- `task_meta` `hera.role` is written on spawn (`agent/hera_spawn.go`), new-orchestrator, and join (`mcp/hera.go`) but is **only cleared by `DeleteMetaForTask` on full task deletion** — never when a binding ends. A worker that finished (binding ended) would still read `role=worker` and be wrongly classified as managed, when Aaron's definition makes it a freelancer the moment its binding ends.
- `readHeraRoles()` buckets only `worker`/`coordinator`; the live-binding table is the single source of truth for "is this task currently coordinated."

New DB method (one query, set-shaped for O(1) lookup during the filter pass):

```go
// ManagedTaskIDs returns the set of argus task IDs that currently hold at least
// one live hera binding (ended_at IS NULL) to a coordinator- or worker-kind role.
// Freelance-kind bindings do NOT count. Used by the Tasks tab freelancers-only filter.
func (d *DB) ManagedTaskIDs() (map[string]bool, error)
```

SQL: `SELECT DISTINCT b.argus_task_id FROM hera_bindings b JOIN hera_roles r ON r.id = b.role_id WHERE b.ended_at IS NULL AND r.kind IN ('coordinator','worker')`.

## Decision 3 — Data feed: compute managed set on the refresh tick

`app.go`'s `refreshTasksWithIDs` already feeds the tasklist (`SetHeraWorkers`, `SetHeraCoordinators`, …). Add a sibling:

```go
a.tasklist.SetManagedTasks(a.readManagedTasks())
```

`readManagedTasks()`:

- **Local mode** — type-assert `a.db` to `*db.DB`; on success call `ManagedTaskIDs()` (authoritative). This follows the established "type-assert to `*db.DB` for local-only ops" pattern (see `gotchas/remote-tui.md`).
- **Remote mode** (`--remote`, `a.db` is `*apistore.Store`) — no binding-query endpoint exists, so fall back to the union of the worker + coordinator maps already returned by `readHeraRoles()`. This is best-effort (subject to the staleness above) and documented. No new REST endpoint is added, keeping blast radius small.

This is the ONE place blast radius could grow (interface + apistore + REST endpoint). The type-assert/fallback keeps it contained and is consistent with existing code. If a future change needs authoritative remote parity, it adds a `/api/...` endpoint then — out of scope here.

## Decision 4 — buildRows exclusion + title indicator

In `buildRows()`, after `matchesFilter` and the `hideHeraWorkers` check, add:

```go
if tl.freelancersOnly && tl.managed[t.ID] {
    continue
}
```

`managed map[string]bool` is the set from `SetManagedTasks`. Order relative to the `H` check is irrelevant (both `continue`).

Title indicator: the panel title rendering already appends `/<filter>` when the substring filter is active. Add a distinct `freelancers only` marker (its own styled segment) when `freelancersOnly` is true, so the two filters read independently. Exact glyph/color chosen at implementation time to match the existing title-decoration style; the requirement is only that an unambiguous indicator is shown.

## Decision 5 — Composition with the existing `H` toggle

`H` (hide hera *workers*, default ON) and `f` (freelancers-only, default OFF) are orthogonal exclusions. When `f` is ON, every managed task — workers and coordinators — is already hidden, so `H` has no additional effect. We do NOT remove or fold `H` (the brief requires additive, non-invasive changes). Behavior is documented in code comments and the gotcha file; not enforced in code.

## Testing strategy

- **DB unit test** (`internal/db/hera_test.go` or a new `_test.go`): seed orchestrator + roles (coordinator, worker, freelance) + bindings (live + ended); assert `ManagedTaskIDs()` contains only tasks with a live coordinator/worker binding, excludes ended bindings and freelance-only tasks.
- **Tasklist unit tests** (`tasklist_test.go`): `freelancersOnly=true` + a `managed` set → `VisibleTaskIDs()` excludes managed, retains freelancers; toggle off restores; composes with `/` filter and `H`.
- **Title indicator render test**: assert the indicator string appears when `freelancersOnly` is true (SimulationScreen or the existing title-draw test seam).
- **SimulationScreen smoke test** (`smoke_test.go`): press `f` on the Tasks tab, assert the visible set narrows to freelancers and the indicator renders; press `f` again to restore.
- **Help modal test** (`help_test.go`): assert the new `{"f", "freelancers only"}` action string is present.
- Gate: `make pre-pr` (build → vet → fmt-check → lint-pr → vuln → test-cover-gate). stdlib-only `vuln` findings are non-blocking per CLAUDE.md.

## Out of scope

- Removing or changing the `H` toggle.
- A REST endpoint / `--remote` authoritative parity for the managed set.
- Any change to the Hera view (#2) — the freelancer-drop there is a separate track.
- Web PWA parity (the brief targets the native Tasks tab).

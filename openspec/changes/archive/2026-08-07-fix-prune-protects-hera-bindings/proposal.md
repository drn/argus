## Why

**Ctrl+R ("prune completed tasks") can silently orphan live Hera roles.**

`db.DB.PruneCompleted` deletes every task row with `status='complete'`,
across every project, with no awareness that Hera exists. `hera_bindings`
holds no foreign key to `tasks` (only `role_id` / `orchestrator_id` cascade),
so deleting a task's row does not end its Hera binding — it just leaves the
binding pointing at a task that no longer exists.

A live investigation found 16 of 140 currently-complete tasks still hold a
live (non-ended) Hera binding — 5 of them not even archived, i.e. ordinary
entries a user would still see in the Projects/Hera tab today (e.g. an
ARGUS-project coordinator role sitting on top of an otherwise fully-merged,
clean task). Confirming the Ctrl+R modal as it exists today would delete
those tasks' rows, worktrees, and branches (local **and** remote) while
leaving their Hera roles dangling instead of properly ended.

## What Changes

- **`PruneCompleted` excludes any completed task that still has a live Hera
  binding** (`hera_bindings.ended_at IS NULL`) from both the rows it reports
  and the rows it deletes. Such a task is left untouched — same as an
  in-progress task — until its Hera binding ends naturally (worker/coordinator
  finishes, is detached, or is archived).
- **The number of tasks skipped for this reason is reported and logged**, so
  a bulk prune that skips work is visible rather than silent: `agent.PrunePrepare`
  logs a `uxlog` line when the skip count is non-zero, and the TUI's prune flow
  shows a statusbar notice with the skipped count (including the case where
  every completed task is skipped and nothing else happens).
- No change to the single-task delete flow (`Ctrl+D`) — that's an explicit,
  one-at-a-time action the operator already reviews before confirming, not the
  bulk, easy-to-fire-blind operation this fixes.

## Capabilities

### Modified Capabilities

- `worktree-management`: the "Pruning completed tasks" requirement now
  excludes tasks with a live Hera binding from both phases of the prune, and
  reports how many were skipped for that reason.

## Impact

- **Modified code:**
  - `internal/db/tasks.go` — `PruneCompleted` gains a `skippedHeraBound int`
    return value and excludes live-hera-bound tasks from its SELECT and
    DELETE.
  - `internal/agent/prune.go` — threads the skip count through `PrunePlan`
    (`SkippedHeraBound`) and logs it.
  - `internal/tui/app.go` — surfaces the skip count via a statusbar notice on
    both the local and remote (`pruneCompletedRemote`) prune paths.
  - `internal/api/handlers.go`, `internal/apiclient/tasks.go`,
    `internal/apistore/store.go` — thread `skippedHeraBound` through the
    `POST /api/maintenance/prune-completed` response so remote mode carries
    the same visibility as local.
  - `internal/api/static/index.html` — the web SPA's "Clean up" toast reports
    the skipped count too (plus a matching `sw.js` `SW_VERSION` bump, since a
    shell asset changed). The macOS app has no prune-completed UI, so there is
    nothing to update there.
  - Tests updated for the new signatures across
    `internal/db/db_test.go`, `internal/agent/prune_test.go`,
    `internal/apistore/store_test.go`, `internal/tui/app_test.go`.
- **No schema change** — `hera_bindings` keeps its existing shape; this is a
  query-scope fix, not a migration.
- **No new key, no new dependency, no daemon RPC change** beyond the one
  additive JSON field on the existing maintenance endpoint.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make /
  Go-build wiring is added or changed. The quality gate stays `make pre-pr`.

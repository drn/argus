## 1. Protect live Hera bindings from prune

- [x] 1.1 `db.DB.PruneCompleted` excludes any task with a live
  (`ended_at IS NULL`) `hera_bindings` row from both the SELECT it reports and
  the DELETE it executes; returns a `skippedHeraBound int` alongside the
  pruned tasks.
- [x] 1.2 `agent.PrunePrepare` threads the skip count into a new
  `PrunePlan.SkippedHeraBound` field and logs it via `uxlog` when non-zero.
- [x] 1.3 `App.pruneCompletedTasks` surfaces the skip count via
  `a.statusbar.SetInfo`, including the case where every completed task is
  skipped (nothing else to prune).
- [x] 1.4 Frontend parity: threaded the skip count through
  `POST /api/maintenance/prune-completed`'s JSON response
  (`internal/api/handlers.go`), `apiclient.PruneReport`, `apistore.Store`'s
  remote-pruner signature, the TUI's `pruneCompletedRemote` statusbar notice,
  and the web SPA's cleanup toast (`internal/api/static/index.html`, with a
  matching `SW_VERSION` bump). The macOS app has no prune-completed UI today,
  so there is nothing to update there.

## 2. Tests

- [x] 2.1 DB test: a completed task with a live Hera binding is excluded from
  both the returned slice and the actual DB delete.
- [x] 2.2 DB test: a completed task whose only binding has already
  `ended_at` set is still pruned normally.
- [x] 2.3 DB test: `skippedHeraBound` count is correct with a mix of
  live-bound, ended-bound, and unbound completed tasks.
- [x] 2.4 `agent/prune_test.go`: `PrunePlan.SkippedHeraBound` reflects the
  DB-layer skip count end to end.
- [x] 2.5 `tui/app_test.go`: statusbar shows the skip note after a prune with
  skipped tasks, and when everything is skipped.
- [x] 2.6 Update existing callers/tests for the new `PruneCompleted`
  signature (`internal/agent/prune.go`, `internal/db/db_test.go`,
  `internal/apistore/store_test.go`).

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/worktree.md`
  documenting: `hera_bindings` has no FK to `tasks`, so any bulk task deletion
  path must explicitly exclude live-bound tasks or it silently orphans Hera
  roles instead of ending them.

## Why

Two pieces of Tasks-view UI state — the `H` hide-hera-managed toggle and the
active top-level tab — reset to their defaults on every argus restart, even
though the underlying Hera rail selection they'd otherwise reveal already
survives restarts. A user who filters out hera workers, or who leaves argus
parked on the Hera/Projects tab focused on a specific coordinator, loses that
context every time they relaunch.

## What Changes

- The Tasks-view hide-hera-managed toggle (`H` key) persists across a full
  argus restart, defaulting to its current behavior (visible) on first run.
- The last active top-level tab (Tasks / Hera·Projects / Settings) persists
  and is restored on launch, in place of always booting on the Tasks tab.
- No change to the Hera rail's existing fold/selection persistence — restoring
  the last tab simply makes that already-persisted selection visible again on
  relaunch.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `task-list-view`: the "Hide hera-managed tasks toggle" requirement gains a
  persistence clause — the toggle's state survives a restart instead of
  always defaulting to visible.
- `tui-shell`: the "Top-level tab navigation" requirement gains a
  restart-restore clause — the shell boots into the last active tab instead
  of always defaulting to Tasks.

## Impact

- `internal/db/`: new file `ui_view_state.go` adding two config-table-backed
  accessor pairs (`LoadHideHeraManaged`/`SaveHideHeraManaged`,
  `LoadLastTab`/`SaveLastTab`), local-only (`*db.DB`), no schema migration.
- `internal/tui/taskview/tasklist.go`: new `SetHideHeraManaged` restore-only
  setter on `TaskListView`.
- `internal/tui/app.go`: wire persistence into the existing
  `OnHeraManagedToggle` callback, `switchTab()`, and `exitAgentView()`; add a
  `restoreLastTab()` call alongside the existing pre-`Run()`
  `applyStartupSkew()` call.
- `--remote` mode: unaffected (both new keys are local-only, matching the
  existing rail-state guard).

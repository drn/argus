**Design doc:** `openspec/changes/persist-tasks-view-ui-state/design.md`

## 1. Tests

- [x] 1.1 `internal/db`: failing table tests for `LoadHideHeraManaged`/`SaveHideHeraManaged` and `LoadLastTab`/`SaveLastTab` — round-trip, and absent-key defaults (`false` / `""`), mirroring the existing `hera_rail_state_test.go` style.
- [x] 1.2 `internal/tui/taskview`: failing test asserting `SetHideHeraManaged(true)` sets `HideHeraManaged()` to `true` without invoking `OnHeraManagedToggle`.
- [x] 1.3 `internal/tui`: failing test(s) asserting `switchTab()` and `exitAgentView()` call through to a stub `*db.DB`'s `SaveLastTab` with the correct value in local mode, and are no-ops in remote (`apistore.Store`) mode.
- [x] 1.4 `internal/tui`: failing test asserting a pre-populated `ui.last_tab` value causes the shell to be on the Hera (or Settings) page/tab after setup, in place of the Tasks default — exercised through whatever seam `app_test.go` already uses to assert `a.header.ActiveTab()` / `a.pages` state without a live terminal.
- [x] 1.5 Confirm every scenario in `specs/task-list-view/spec.md` and `specs/tui-shell/spec.md` (this change) has a corresponding failing test from 1.1–1.4.

## 2. DB-layer persistence primitives

**Depends on:** Stage 1

- [x] 2.1 Add `internal/db/ui_view_state.go`: `hideHeraManagedConfigKey = "ui.hide_hera_managed"` and `lastTabConfigKey = "ui.last_tab"`, with `LoadHideHeraManaged() (bool, error)`, `SaveHideHeraManaged(bool) error`, `LoadLastTab() (string, error)`, `SaveLastTab(string) error` — thin wrappers over `GetConfigValue`/`SetConfigValue`, mirroring `internal/db/hera_rail_state.go`.
- [x] 2.2 `make test-pkg PKG=./internal/db/` green.

## 3. Persist the hide-hera-managed toggle (Tasks view)

**Depends on:** Stage 2

- [ ] 3.1 Add `TaskListView.SetHideHeraManaged(hidden bool)` in `internal/tui/taskview/tasklist.go` — sets `hideHeraManaged` directly, does NOT call `OnHeraManagedToggle` (restore, not a user action).
- [ ] 3.2 In `internal/tui/app.go`, at `a.tasklist` construction: if `d, ok := a.db.(*db.DB); ok`, load the persisted value and call `a.tasklist.SetHideHeraManaged(...)`, logging (never failing) on a load error.
- [ ] 3.3 Extend the existing `OnHeraManagedToggle` callback to also persist the new value through the same local-only type-assert, logging (never failing) on a save error.
- [ ] 3.4 `make test-pkg PKG=./internal/tui/taskview/` green.

## 4. Persist and restore the last active tab (shell)

**Depends on:** Stage 2

- [ ] 4.1 In `switchTab()`, persist the new tab (as `"tasks"`/`"hera"`/`"settings"`) through the local-only type-assert, logging (never failing) on a save error.
- [ ] 4.2 In `exitAgentView()`, persist `"tasks"` the same way (covers the direct call sites that bypass `switchTab()`).
- [ ] 4.3 Add `restoreLastTab()`: local-only load of the persisted tab; if it resolves to `TabHera` or `TabSettings`, call `a.switchTab(...)` to land there (reusing `switchToHeraTab2()`'s refresh/focus/label logic unchanged); an absent/unrecognized value is a no-op (stays on the Tasks default).
- [ ] 4.4 Call `a.restoreLastTab()` from `Run()` alongside the existing `a.applyStartupSkew()` call, before `a.tapp.Run()`.
- [ ] 4.5 `make test-pkg PKG=./internal/tui/` green.

## 5. Documentation and verification

**Depends on:** Stages 3, 4

- [ ] 5.1 Add a gotcha bullet to `context/knowledge/gotchas/tasklist-ui.md` (hide-hera persistence) and one to `context/knowledge/gotchas/misc.md` or a shell-relevant file (last-tab persistence + restore-on-launch injection point), noting the new `config` table keys and the local-only guard, per this repo's CLAUDE.md documentation requirements.
- [ ] 5.2 Update `context/knowledge/index.md`'s topic-file bullet cells to reference the new behavior where relevant.
- [ ] 5.3 `openspec validate persist-tasks-view-ui-state --strict` passes.
- [ ] 5.4 `make pre-pr` passes clean.

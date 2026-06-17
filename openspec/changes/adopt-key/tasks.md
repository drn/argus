# Tasks: adopt-key

Restore the `J` adopt/reparent key to the native Hera rail (parity with the plugin), carrying the BUG-026 teardown invariant.

## 1. Tests (red first)

- [x] 1.1 `internal/db/hera_test.go`: `ListHeraBindingsByTask` returns ALL bindings (live + ended) for a task, ordered newest-first; empty for an unknown task.
- [x] 1.2 `internal/tui/hera/adopt_test.go`: `AdoptTaskIntoOrchestrator` creates worker role + live binding; de-collides the role name; stamps `meta:hera.role=worker` best-effort; rejects empty task id; rejects a duplicate live binding under the same orchestrator.
- [x] 1.3 `internal/tui/hera/adopt_test.go`: `ReparentCoordinator` creates the link role+binding under the parent; rejects self; rejects a descendant (cycle) via `SubtreeOrchIDs`; rejects a coordinator with no coord role / no binding; resolves task+worktree from the LATEST (live-else-ended) coord binding.
- [x] 1.4 `internal/tui/hera/adopt_test.go` (BUG-026): a coordinator with a live parent-link AND a leftover ended link role, re-parented again, ends the live link (`reparented`) AND deletes all prior link roles → exactly one clean link, no de-collided duplicates.
- [x] 1.5 `internal/tui/herapicker_test.go`: `OrchPickerModal` substring-filters by name, Enter selects, Esc cancels, narrow-terminal Draw doesn't panic.
- [x] 1.6 `internal/tui/heraactions_test.go` / smoke: `heraOpenAdopt` dispatches freelance→adopt picker, coordinator→reparent picker (self excluded), not-applicable→statusbar feedback; remote-mode (`heraAdoptOps==nil`) is inert.
- [x] 1.7 `internal/tui/modal/help_test.go`: the "Hera View (rail)" section lists the `J` action.

## 2. DAO

- [x] 2.1 `internal/db/hera.go`: add `ListHeraBindingsByTask(taskID string) ([]*HeraBinding, error)` (mirror `ListHeraBindingsByRole`, ordered `started_at DESC, id DESC`).

## 3. Ops (new file)

**Depends on:** Stage 2

- [x] 3.1 `internal/tui/hera/adopt.go`: `AdoptStore` interface (the read+write DAOs the ops need), `AdoptOps` + `NewAdoptOps(AdoptStore)`.
- [x] 3.2 `AdoptInput`/`AdoptResult` + `AdoptTaskIntoOrchestrator`: already-bound-under-orch guard, `uniqueRoleName` de-collide, transactional `CreateHeraRoleWithBinding` (no orphan role on a worktree-orch collision), best-effort `SetMeta`.
- [x] 3.3 `ReparentInput`/`ReparentResult` + `ReparentCoordinator`: self/cycle guards, latest-binding resolution, BUG-026 teardown (end live links `reparented`, delete all link roles by id), new link create.
- [x] 3.4 `ListActiveOrchestrators()` for the picker; `uniqueRoleName` helper over `UniqueHeraRoleName`.

## 4. Picker widget (new file)

- [x] 4.1 `internal/tui/herapicker.go`: `OrchPickerModal` (type-to-filter, Enter/Esc) mirroring `SessionPickerModal`; `Selected()/Canceled()/SelectedOrch()`, `InputHandler()`, `PasteHandler()`, `Draw()`.

## 5. Wiring

**Depends on:** Stages 3, 4

- [x] 5.1 `internal/tui/hera/page.go`: add `OnAdopt func(Selection)` + `case 'J':` in `handleRailMutation` (rail-focus-only; pane forwards `J` to PTY unchanged).
- [x] 5.2 `internal/tui/app.go`: `modeHeraOrchPicker`, `heraOrchPicker`/`heraOrchPickerPick`/`heraAdoptOps` fields, key routing in `handleGlobalKey`, `a.heraPage.OnAdopt = a.heraOpenAdopt` + `a.heraAdoptOps = hera.NewAdoptOps(d)` in the local-mode block.
- [x] 5.3 `internal/tui/heraactions.go`: `heraOpenAdopt` dispatch, `heraAdoptFreelancer`/`heraAdoptCoordinator`, `openHeraOrchPicker`/`handleHeraOrchPickerKey`/`closeHeraOrchPicker`; resolve worktree+project from the task row; refresh + statusbar feedback.

## 6. Docs

- [x] 6.1 `internal/tui/modal/help.go`: add `{"J", "adopt freelancer / reparent coordinator"}` to the "Hera View (rail)" section.
- [x] 6.2 README Reference appendix: add `J` to the Hera rail keybinding table.
- [x] 6.3 `context/knowledge/gotchas/hera-view.md` + `keybindings.md`: capture the native-divergence guard + BUG-026 teardown gotcha.

## 7. Gate

- [x] 7.1 `make pre-pr` green; commit; `iris_push` to drn; HOLD the PR; report branch + summary + PR command to coord; `hera_status(done)`.

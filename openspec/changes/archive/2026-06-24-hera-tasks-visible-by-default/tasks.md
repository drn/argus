# Tasks

TDD throughout (red → green → refactor). `make pre-pr` must pass before PR.

## 1. Worker indicator glyph

- [ ] 1.1 Add `IconWorker` + `StyleWorker` to `internal/tui/theme/theme.go` (distinct from the cyan `IconCoordinator`; pick a nf-md worker/leaf glyph + a non-coordinator colour).
- [ ] 1.2 In `drawTaskRow` (`internal/tui/taskview/tasklist.go`), render the worker glyph in the existing hera-role indicator cell when the task is a hera-spawned worker (or holds a live worker binding) AND is not a coordinator. Coordinator branch unchanged and takes precedence.
- [ ] 1.3 Render test: worker row draws `IconWorker`; coordinator+worker row draws `IconCoordinator`; plain row draws neither and the name reclaims the cell.

## 2. Flip the default

- [ ] 2.1 Set `hideHeraManaged` default to `false` (`tasklist.go:148`); update the field/comment.
- [ ] 2.2 Confirm `OnHeraManagedToggle` initial wiring (App side) reflects the new default — no stale "hidden" status hint at startup.
- [ ] 2.3 Tests (`internal/tui/taskview/hera_workers_test.go`): default `VisibleTaskIDs()` includes hera workers + live coordinators; `ToggleHeraManaged()` once hides them; toggle again reveals; freelancer/plain always visible; composes with filter.

## 3. Docs

- [ ] 3.1 `context/knowledge/gotchas/tasklist-ui.md`: default flipped to visible; worker indicator cell + precedence (coordinator > worker; orthogonal to status/PR glyphs).
- [ ] 3.2 README Reference: `H` now *hides* (default is visible); add the worker glyph to the indicator legend if one is documented.
- [ ] 3.3 If `help_test.go` asserts an indicator legend, extend it; the `H` key entry text is unchanged.

## 4. Archive (same PR, before merge)

- [ ] 4.1 Fold the delta into `openspec/specs/task-list-view/spec.md`; move this change folder to `openspec/changes/archive/<YYYY-MM-DD>-hera-tasks-visible-by-default/`. Commit on the change branch.

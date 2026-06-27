# Tasks: add-optional-task-name-input

**Design doc:** `openspec/changes/add-optional-task-name-input/design.md`

## 1. Tests

- [ ] 1.1 Write failing tests from each scenario in `specs/forms-and-modals/spec.md` — name field renders after prompt with `(optional)` placeholder, reachable via Tab focus order, accepts paste, whitespace-only treated as blank, submit-with-name yields trimmed/sanitized name + user-chosen signal, submit-without-name yields prompt-derived name (`internal/tui/newtaskform_test.go`).
- [ ] 1.2 Write failing tests from `specs/hera-view/spec.md` — `w` with explicit name names the worker; `n` with explicit name names orchestrator + coordinator task; blank derives from prompt (hera action tests, e.g. `internal/tui/heraactions_test.go` and/or `internal/agent/hera_spawn` tests).
- [ ] 1.3 Write failing test from `specs/auto-naming/spec.md` — a task created with a user-supplied name disables auto-naming (`AutoName=false`), so the LLM rename does not run.
- [ ] 1.4 Confirm every `it should X` criterion in the design doc has a failing test (Prove-It Pattern).

## 2. Form field + new-task submission

**Depends on:** Stage 1

- [ ] 2.1 Add `ntFieldName = 5` and bump `ntFieldCount → 6` in `internal/tui/newtaskform.go`; add the `[]rune` name buffer + cursor field.
- [ ] 2.2 Render the name input after the prompt with placeholder `(optional)`, mirroring the project/branch single-line input (caret, dim placeholder when empty+unfocused).
- [ ] 2.3 Insert the name field into Tab/Backtab focus order immediately after the prompt; add its case to `PasteHandler()`'s `switch f.focused`.
- [ ] 2.4 On submit: trim + sanitize the name via the existing safe-name path; set `Task.Name` to it when non-empty, else `GenerateNameFromPrompt(prompt)`; pass the trimmed name (`""` if blank) through the submit callback's second `string` parameter as the user-chosen signal.
- [ ] 2.5 In the new-task create path, set `CreateInput.AutoName = false` when the callback reports a user-supplied name (keep today's `AutoName` behavior when blank).
- [ ] 2.6 Make Stage-1 forms-and-modals + auto-naming tests pass.

## 3. Hera `w` / `n` naming

**Depends on:** Stage 2

- [ ] 3.1 Extend `agent.SpawnHeraWorker` / `agent.SpawnHeraCoordinator` (`internal/agent/hera_spawn.go`) to accept an explicit name override; when non-empty use it (worker: task/role name; coordinator: orchestrator name + coordinator task name) instead of `DeriveHeraWorkerName(prompt)`; when empty derive as today. Coordinator role stays `coord`.
- [ ] 3.2 Thread the modal's entered name from `heraSpawnWorker` / `heraNewCoordinator` (`internal/tui/heraactions.go`) into the spawn primitives via the callback's name parameter.
- [ ] 3.3 Confirm orchestrator-name collision still surfaces via the existing status-bar error path (now auto-clearing) and the form stays open.
- [ ] 3.4 Make Stage-1 hera-view tests pass.

## 4. Verify

**Depends on:** Stage 2, Stage 3

- [ ] 4.1 `make test-pkg PKG=./internal/tui/` and `./internal/agent/` green; full `make pre-pr` clean.
- [ ] 4.2 Confirm no TUI key added/rebound (help modal + README unchanged); confirm blank-field behavior is byte-identical to pre-change for all three entry points.

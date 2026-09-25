## 1. Data access and safety

- [x] 1.1 Add `Artifacts(taskID)` to the TUI store interface and implement it in `apistore` through a typed `apiclient` call to the existing list endpoint.
- [x] 1.2 Add a bounded/streaming artifact byte reader to `apiclient`; use manifest filenames, bearer auth, cancellation, and typed HTTP errors.
- [x] 1.3 Share the registered-artifact path guard between REST serving and local TUI opening, preserving manifest scoping and symlink protection.
- [x] 1.4 Test local/remote manifest parity, task scoping, traversal/symlink refusal, missing bytes, cancellation, and large streamed transfers.

## 2. TUI browser

- [x] 2.1 Build the task-scoped browser with loading, empty, error, and populated states; list metadata and support `r`, Enter, Esc, Ctrl+Q, and external open.
- [x] 2.2 Add bounded, scrollable text/Markdown preview. Escape terminal controls and render untrusted content as text, never as TUI markup.
- [x] 2.3 Add system-viewer opening for non-text types; stream remote bytes to a managed temporary file, avoid UI-thread I/O, and report failures in the status bar.
- [x] 2.4 Fetch the selected task's count asynchronously and reject stale fetch results after task changes. Refresh on browser reopen and `r`.
- [x] 2.5 Add render and SimulationScreen smoke tests for navigation, focus restoration, stale-request guards, and live agent key interception.

## 3. Keymap and documentation

- [x] 3.1 Add `tasklist.artifacts` (`v`) and `agent.artifacts` (`ctrl+t`) to the keymap, dispatchers, command palette registries, and generated help assertions.
- [x] 3.2 Update the README Reference keybinding tables and `artifact_register` success text.
- [x] 3.3 Add the non-obvious local/remote artifact safety and temporary-file lifecycle rules to the relevant gotcha file; log artifact fetch/open success, failure, and stale-result skips via `uxlog`.

## 4. Verification and archive

- [x] 4.1 Run focused tests, `make test`, `make test-cover`, and `make pre-pr`; address failures.
- [x] 4.2 Archive this OpenSpec change on the same branch before opening or updating a PR, merging the deltas into base specs.

## Why

`artifact_register` persists a per-task manifest and durable file, and Argus Web can list and view it. The TUI has no artifact list, indicator, or open action, so a user working in the terminal has to switch to the web app to discover what an agent produced. The MCP tool's success text currently directs the agent to Argus Web only.

## What Changes

- Show the selected task's artifact count in the Tasks detail panel. Fetch it asynchronously when the selected task changes and after an explicit artifact refresh; never block the tview thread on SQLite, HTTP, or file I/O.
- Add a task-scoped artifact browser listing title, type, size, and registration time, including clear empty, loading, missing-file, and error states. Open it from the Tasks list with a rebindable `v` action and from the agent view with a rebindable `ctrl+t` action. Both actions also appear in help and the command palette.
- Let the browser refresh with `r`. Enter previews bounded text and Markdown within the TUI. For HTML, PDF, images, audio, and video, Enter opens the registered file with the system's default viewer. The browser also offers an explicit open-externally action for text and Markdown. Esc or Ctrl+Q returns to the original TUI focus.
- Keep local and `--remote` behavior equivalent: local mode reads the registered manifest and guarded artifact file; remote mode uses the existing authenticated artifact list and byte endpoints. External opening in remote mode streams to a temporary file without loading the whole artifact into memory. No new REST endpoint is required.
- Update the MCP tool's success text to say the artifact is available in Argus's task Artifacts browser and Argus Web.

## Design Boundaries

- Artifacts remain task-scoped and read-only in this change. The browser never treats arbitrary worktree files as artifacts and never opens an unregistered filename.
- No terminal rendering of HTML/PDF/images/media is proposed. The TUI displays their metadata and hands an explicit open request to the OS viewer. Remote media downloads are user-initiated, streamed, cancellable, and size-bounded by the registered type's cap.
- No new artifact event is added. Opening or refreshing the browser obtains the current manifest; the count is refreshed when selecting a task and after `r`.
- **Named follow-up: macOS artifact browser.** `macos/` has no artifact UI today. This change adds no REST field or endpoint, so the existing macOS wire surface is unaffected; a native artifact list/viewer remains a separate feature.

## Capabilities

### Modified Capabilities

- `session-artifacts`: TUI discovery, navigation, preview, external open, refresh, and local/remote access.
- `keybindings`: rebindable task-list and agent-view actions, help, and command palette.

## Impact

- TUI task detail, artifact browser modal, App wiring, keymap/help, and command palette.
- `internal/tui/store`, `internal/apistore`, and `internal/apiclient` for manifest and byte access in both modes.
- Shared artifact-path guard for local opening and REST serving; no schema or REST route changes.
- Tests, README Reference keybinding table, MCP registration text, and relevant gotchas.

## Approval

Per `AGENTS.md`, implementation starts only after this change is approved. The proposal and delta specs are review material; no behavior has changed yet.

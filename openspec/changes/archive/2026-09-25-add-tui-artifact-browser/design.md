## Current path

`artifact_register` copies the source into `~/.argus/artifacts/<task-id>/<filename>` and inserts/upserts a manifest row. `db.Artifacts` lists those rows. The REST API exposes the list and manifest-scoped raw bytes; the web viewer consumes both. The TUI store interface exposes artifact deletion only, and `apiclient` has no artifact methods. `macos/` has no artifact UI.

## UI flow

1. The selected task's detail panel shows `Artifacts: N` after an asynchronous manifest fetch. While loading, show a neutral `Artifacts: …`; an error shows `Artifacts: unavailable` rather than a stale count.
2. `v` in the Tasks list or `ctrl+t` in the agent view opens the same browser for the current task. It initially shows loading, then a scrollable list with title, type, size, and time. `j/k` and arrow keys move; `r` refetches; Esc/Ctrl+Q closes. An empty list says no artifacts registered for this task.
3. Enter on text/Markdown opens a scrollable plain-text preview capped at 256 KiB, with a truncation note. Enter on every other type explicitly opens the system viewer. `o` opens the highlighted artifact externally regardless of type. Returning from preview keeps the list selection.
4. Opening the system viewer leaves the TUI browser in place, so the user can open another artifact or return to the agent. The user sees an error in the browser/status bar when the file is gone or the viewer fails to launch.

## Data flow

- The App requests the manifest through `store.Store.Artifacts(taskID)`. Local mode uses `db.Artifacts`; remote mode uses `apistore` and a typed `apiclient` call to `GET /api/tasks/{id}/artifacts`.
- A fetch carries task ID plus a monotonically increasing request generation. Results are applied on the tview thread only if both still match the active selection/browser. Repeated cursor movement cancels or discards previous requests. Opening and `r` always refetch; no SSE event or recurring poll is needed.
- The local reader first resolves a manifest row for `(taskID, filename)` and uses the same symlink-aware containment check as REST serving. Extract the current API path resolver to a shared internal package rather than maintaining two security implementations.
- The remote reader requests `GET /api/tasks/{id}/artifacts/{filename}` with bearer auth. A text preview requests at most 256 KiB with HTTP Range. If the server returns a full body, the client still caps its read and closes the response. An external open streams to a 0600 temporary file with the registered extension and verifies the type-specific size ceiling while copying. The temp directory is owned by the TUI process and removed at shutdown, after viewers have had time to open the file. Cancellation removes an incomplete file immediately.
- Neither HTML nor Markdown content is interpreted as tview formatting or ANSI. Embedded control bytes are escaped or replaced before painting. HTML is only handed to the external viewer after the user chooses to open it.

## Tradeoffs

- `ctrl+t` currently reaches the agent PTY. Intercepting it is a deliberate shortcut change; the action is rebindable. The Tasks-list `v` key is presently unused.
- Large remote media requires transfer to a temporary file for OS viewing. No auto-download occurs when browsing metadata. Download progress and cancellation should remain responsive because copying runs off the tview thread.
- The macOS client is a named follow-up. This change uses only existing REST routes and does not alter their contract.

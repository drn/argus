# Session Artifacts

## Purpose

Session artifacts are files (HTML reports, PDFs, images, markdown, text, audio, and video) that an agent produces during a task and registers in a per-task manifest. Argus Web and the TUI list and open registered artifacts; the REST API serves the remote clients. Only registered files can be served or opened, no file outside the task's artifact directory can be reached, and embedded web artifacts can be safely iframed without leaking the caller's auth token.
## Requirements
### Requirement: List task artifacts

The API SHALL return the registered-artifact manifest for an existing task as a JSON object. When a task has no artifacts the response SHALL contain an empty array rather than a null value. Requests for a non-existent task SHALL be rejected.

#### Scenario: Task with registered artifacts

- **WHEN** a client requests the artifact list for a task that has registered artifacts
- **THEN** the response is HTTP 200 with a JSON `artifacts` array containing each registered artifact's metadata

#### Scenario: Task with no artifacts

- **WHEN** a client requests the artifact list for an existing task that has no registered artifacts
- **THEN** the response is HTTP 200 and the `artifacts` field is an empty array (`[]`), never null

#### Scenario: Unknown task

- **WHEN** a client requests the artifact list for a task ID that does not exist
- **THEN** the response is HTTP 404

### Requirement: Serve registered artifact bytes

The API SHALL serve the raw bytes of a single artifact selected by its on-disk filename, with the Content-Type that matches the artifact's recorded type. Range requests and HEAD handling SHALL be supported for the served content.

#### Scenario: Serve by artifact type

- **WHEN** a client requests a registered artifact whose type is HTML, markdown, PDF, image, text, audio, or video
- **THEN** the response is HTTP 200, the body is the exact stored bytes, and the Content-Type matches the artifact type (for example `text/html; charset=utf-8` for HTML, `application/pdf` for PDF, `image/png` for a PNG image, `audio/mpeg` for an mp3, `video/mp4` for an mp4)

#### Scenario: Range request against a large media artifact

- **WHEN** a client requests a byte range of a registered audio or video artifact via a `Range` header
- **THEN** the response serves the requested range (HTTP 206) so the client can scrub/seek without downloading the full file first

### Requirement: Manifest-scoped serving

A filename SHALL only be served when a manifest row exists for that (task, filename) pair. The presence of a file on disk without a corresponding manifest row SHALL NOT make it servable. A manifest row whose backing bytes are missing SHALL produce a not-found response rather than an error.

#### Scenario: Unregistered file on disk

- **WHEN** a file physically exists in the task's artifact directory but has no manifest row, and a client requests it
- **THEN** the response is HTTP 404 and the bytes are not served

#### Scenario: Registered row with missing bytes

- **WHEN** a manifest row exists but its backing file was never written or has been deleted
- **THEN** the response is HTTP 404

### Requirement: Path-escape defense

The API SHALL ensure a served file resolves to a location inside the task's artifact directory. A filename is a slash-separated relative path whose segments SHALL be non-empty, SHALL NOT be `.` or `..`, SHALL NOT be the reserved thumbnail-cache directory, and SHALL NOT contain backslashes or NUL. Filenames that traverse outside the directory, or that resolve through a symlink (file or directory) to a target outside it, SHALL be refused, even when a manifest row would otherwise select them.

#### Scenario: Path traversal

- **WHEN** the resolved artifact filename attempts to traverse above the artifact directory (for example `../../../etc/passwd` or `sub/../../x`)
- **THEN** the path is refused and the bytes are not served

#### Scenario: Nested folder member

- **WHEN** the filename is a relative path such as `shots/01.png` inside the artifact directory and its real path is still inside that directory
- **THEN** the resolved real path is accepted and its bytes are served

#### Scenario: Symlink escape

- **WHEN** a file or folder inside the artifact directory is a symlink pointing to a target outside that directory
- **THEN** the path is refused and the symlink target is not served

#### Scenario: Legitimate basename

- **WHEN** the filename is a direct child of the artifact directory and its real (symlink-resolved) path is still inside that directory
- **THEN** the resolved real path is accepted and its bytes are served

### Requirement: Framing and caching headers

When serving an artifact, the API SHALL relax the global frame-deny policy to permit same-origin embedding, set an equivalent content-security-policy that restricts frame ancestors to the same origin, and instruct intermediaries not to cache the response so a regenerated artifact under the same name is never served stale.

#### Scenario: Headers on a served artifact

- **WHEN** a registered artifact is served successfully
- **THEN** the response sets `X-Frame-Options: SAMEORIGIN`, `Content-Security-Policy: frame-ancestors 'self'`, and `Cache-Control: no-store`

### Requirement: Authenticated access

Both the artifact list endpoint and the raw artifact endpoint SHALL require authentication; neither is in the auth-skip allowlist. Read-only access via a device token SHALL be sufficient.

#### Scenario: Missing token

- **WHEN** a client requests either the artifact list or a raw artifact without a valid token
- **THEN** the response is HTTP 401

#### Scenario: Valid token

- **WHEN** a client requests a raw artifact with a valid bearer token
- **THEN** the response is HTTP 200

### Requirement: Artifact cleanup on task deletion

Deleting a task SHALL remove both its artifact manifest rows and its on-disk artifact directory.

#### Scenario: Delete task with artifacts

- **WHEN** a task that has registered artifacts is deleted
- **THEN** the manifest rows for that task are removed and the task's on-disk artifact directory no longer exists

### Requirement: Discover task artifacts in the TUI

The TUI SHALL show the registered artifact count for the selected task and SHALL provide a task-scoped browser listing each registered artifact's title, type, size, and registration time. An empty manifest SHALL show an explicit empty state. A task change SHALL not show the prior task's artifact data while an asynchronous request is pending.

#### Scenario: Selected task has artifacts

- **WHEN** a task with registered artifacts is selected in the Tasks list
- **THEN** its artifact count is shown in the detail panel, and its browser lists the registered metadata without blocking input

#### Scenario: Task has no artifacts

- **WHEN** the browser opens for a task with an empty manifest
- **THEN** it shows an empty state and offers refresh

#### Scenario: Selection changes during fetch

- **WHEN** the user selects another task before the previous manifest request finishes
- **THEN** the previous result does not replace the newly selected task's count or browser contents

### Requirement: Preview or open a registered artifact from the TUI

The TUI SHALL preview bounded text and Markdown within the browser. For HTML, PDF, image, audio, and video artifacts, it SHALL open the registered bytes with the system viewer only after an explicit user action. Text and Markdown SHALL also offer an explicit external-open action. A missing backing file or failed open SHALL produce a visible error and keep the browser usable.

#### Scenario: Preview text

- **WHEN** the user selects a registered text or Markdown artifact and presses Enter
- **THEN** the TUI shows a scrollable text preview with a stated truncation limit and a way back to the list

#### Scenario: Open a non-text artifact

- **WHEN** the user selects a registered HTML, PDF, image, audio, or video artifact and presses Enter
- **THEN** the TUI opens those bytes in the system viewer and remains in the artifact browser

#### Scenario: Missing backing bytes

- **WHEN** a registered artifact has no readable backing file
- **THEN** the TUI reports the failure without exiting or showing an unrelated file

### Requirement: Artifact browser works in local and remote TUI modes

The local TUI SHALL use the per-task manifest and the same path-escape protections as REST serving. The remote TUI SHALL list and retrieve artifacts through the existing authenticated REST endpoints, using the manifest filename as the selector. Remote external opening SHALL stream bytes to a temporary file without buffering the whole artifact in memory and SHALL clean up temporary files when their ownership ends. Neither mode SHALL expose unregistered files.

#### Scenario: Remote artifact browser

- **WHEN** a remote TUI opens the artifact browser for a task
- **THEN** it shows the same registered metadata and can preview or externally open the same artifact types as local mode

#### Scenario: Unregistered or escaping file

- **WHEN** a file is absent from the manifest or resolves outside the task artifact directory
- **THEN** the TUI refuses to open it

#### Scenario: Large remote media

- **WHEN** a user explicitly opens a registered audio or video artifact over the remote TUI
- **THEN** the transfer is streamed to disk, can be canceled, and does not require memory proportional to file size

### Requirement: Refresh artifact discovery

The browser SHALL offer manual refresh, and reopening it SHALL fetch the current manifest so registration or replacement performed during a live session is discoverable. Refresh errors SHALL be visible while retaining the last successful list.

#### Scenario: Agent registers a new artifact

- **WHEN** an agent registers an artifact after the browser was last loaded and the user refreshes or reopens it
- **THEN** the new manifest entry and task count appear

### Requirement: Zoom an image artifact in Argus Web

When an image artifact is rendered in Argus Web (paneled viewer or full-screen overlay), clicking or tapping the image SHALL toggle it between fit-to-pane display and actual pixel size. While at actual size, the image's container SHALL be scrollable so the full image can be panned into view. A second click/tap, or leaving the artifact view, SHALL return the image to fit-to-pane. This behavior SHALL be identical in both presentations since they share one rendering path.

#### Scenario: Zoom in on a large screenshot

- **WHEN** a user clicks or taps an image artifact that is currently fit to its pane
- **THEN** the image switches to actual pixel size and its container becomes scrollable

#### Scenario: Zoom back out

- **WHEN** a user clicks or taps an image artifact that is currently at actual size
- **THEN** the image returns to fit-to-pane display

#### Scenario: Same behavior in both presentations

- **WHEN** the same image artifact is opened via the paneled viewer versus the full-screen "Open" overlay
- **THEN** the zoom toggle behaves identically in both

#### Scenario: Leaving the artifact resets zoom

- **WHEN** a user navigates away from a zoomed image artifact and later reopens it
- **THEN** it is shown fit-to-pane, not still zoomed

### Requirement: Register a folder of artifacts

`artifact_register` SHALL accept a directory path and register every regular, non-hidden file beneath it (recursively) as an artifact tagged with a `folder` equal to the directory's basename. Each file SHALL be copied to `<task artifact dir>/<folder>/<relative path>` and recorded under that slash-separated filename, with its type inferred from its extension. Symlinks, hidden files and hidden directories SHALL be skipped. A folder SHALL be rejected when it holds more than 500 registrable files or more than 2 GiB in total, or when it has none. Files over their per-type size cap SHALL be skipped. Re-registering a folder SHALL remove manifest rows and bytes for files no longer present in the source directory.

#### Scenario: Register a directory

- **WHEN** an agent registers a directory containing `01.png`, `clips/a.mp4` and a hidden `.DS_Store`
- **THEN** two artifacts are recorded with filenames `<dir>/01.png` and `<dir>/clips/a.mp4`, each with `folder` set to the directory name, and the hidden file is not registered

#### Scenario: Symlink inside the folder

- **WHEN** the directory contains a symlink to a file outside it
- **THEN** the symlink is skipped and its target is never copied

#### Scenario: Re-register a changed folder

- **WHEN** a folder is registered again after a file was removed from the source directory
- **THEN** the removed file's manifest row and stored bytes are deleted

#### Scenario: Folder over the file-count cap

- **WHEN** the directory holds more than 500 registrable files
- **THEN** registration fails and no artifacts are recorded

### Requirement: Folder member listing

The artifact list payload SHALL include a `folder` field on artifacts registered as part of a folder and omit it for standalone files.

#### Scenario: List includes folder

- **WHEN** a client lists artifacts for a task that has a registered folder
- **THEN** each member's metadata carries `folder` and a nested `filename` such as `shots/sub/01.png`

### Requirement: Artifact thumbnails

`GET /api/tasks/{id}/artifacts/{filename}?thumb=1` SHALL return a JPEG thumbnail whose longest edge is at most 320 pixels for image artifacts (png, jpeg, gif, webp, bmp) and, when `ffmpeg` is available, for video artifacts. The thumbnail SHALL be generated on demand, cached on disk, and regenerated when the source is newer. Transparent pixels SHALL be flattened onto white. When no thumbnail can be produced (unsupported type or format, undecodable or oversized image, no `ffmpeg`) the response SHALL be HTTP 404. Thumbnail requests SHALL be gated by the same manifest row and path checks as raw serving.

#### Scenario: Image thumbnail

- **WHEN** a client requests `?thumb=1` for a registered 800x600 PNG
- **THEN** the response is an `image/jpeg` of 320x240

#### Scenario: No thumbnail available

- **WHEN** a client requests `?thumb=1` for a text artifact or an undecodable image
- **THEN** the response is HTTP 404

### Requirement: Folder gallery in Argus Web

Argus Web SHALL show a folder's members as one list row that opens a grid of thumbnails loaded lazily as tiles scroll into view, authenticated through the API (never via a token in the URL). Tiles without a thumbnail SHALL show a type icon. Selecting a tile SHALL open the full-screen viewer with previous/next navigation across the folder's items, including arrow-key navigation.

#### Scenario: Open a folder

- **WHEN** a user taps a folder row in the artifacts list
- **THEN** a grid of the folder's items is shown with image and video thumbnails

#### Scenario: Step through items

- **WHEN** a user opens an item from the grid and presses the right arrow or the next button
- **THEN** the next item in the folder opens full-screen


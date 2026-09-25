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

The API SHALL ensure a served file resolves to a location directly inside the task's artifact directory. Filenames that traverse outside the directory, name a nested subpath, or resolve through a symlink to a target outside the directory SHALL be refused, even when a manifest row would otherwise select them.

#### Scenario: Path traversal

- **WHEN** the resolved artifact filename attempts to traverse above the artifact directory (for example `../../../etc/passwd`)
- **THEN** the path is refused and the bytes are not served

#### Scenario: Nested subpath

- **WHEN** the resolved artifact filename names a path inside a subdirectory rather than a direct child of the artifact directory
- **THEN** the path is refused

#### Scenario: Symlink escape

- **WHEN** a file inside the artifact directory is a symlink pointing to a target outside that directory
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

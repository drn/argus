# Session Artifacts

## Purpose

Session artifacts are files (HTML reports, PDFs, images, markdown, text) that an agent produces during a task and registers in a per-task manifest. This capability exposes those artifacts over the REST API so the remote SPA can list and view them, while enforcing that only registered files are served, that no file outside the task's artifact directory can be reached, and that embedded artifacts can be safely iframed without leaking the caller's auth token.

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

- **WHEN** a client requests a registered artifact whose type is HTML, markdown, PDF, image, or text
- **THEN** the response is HTTP 200, the body is the exact stored bytes, and the Content-Type matches the artifact type (for example `text/html; charset=utf-8` for HTML, `application/pdf` for PDF, `image/png` for a PNG image)

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

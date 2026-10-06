# Session Artifacts

## ADDED Requirements

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

## MODIFIED Requirements

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

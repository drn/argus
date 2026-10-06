# Proposal: Artifact folders with thumbnail galleries

## Why

`artifact_register` takes one file. Sharing a set of related files — a screenshot series, a render batch, a recording folder — means registering each one separately, and Argus Web lists them as an undifferentiated flat list. There is no way to view a folder as a unit or browse a gallery of images/videos.

## What Changes

- `artifact_register` accepts a **directory** as `path`: every regular, non-hidden file under it (recursively; symlinks skipped) is copied into `~/.argus/artifacts/<task>/<folder>/<rel path>` and registered as one manifest row per file tagged with a `folder`.
- Artifact `filename` may now be a slash-separated relative path (`folder/sub/x.png`); the `folder` field is added to the manifest, the REST list payload and the `Artifact` model.
- Re-registering a folder replaces its contents (rows/bytes for files no longer in the source directory are pruned).
- `GET /api/tasks/{id}/artifacts/{filename...}` accepts nested paths and a `?thumb=1` query that returns a server-generated JPEG thumbnail (≤320px) for image artifacts (png/jpeg/gif/webp/bmp) and, when `ffmpeg` is installed, video artifacts. Thumbnails are cached under the task's artifact dir; 404 means "no thumbnail".
- Argus Web groups folder members into one list row that opens a lazy-loaded thumbnail grid; tapping a tile opens the existing full-screen viewer with prev/next (buttons + arrow keys).
- Limits: 500 files and 2 GiB per folder, plus the existing per-file caps.

## Impact

- Specs: `session-artifacts`.
- Code: `internal/model`, `internal/db` (new `folder` column), `internal/mcp`, `internal/artifacts` (nested `ResolvePath`, new thumbnailer; adds the `golang.org/x/image` dependency), `internal/api`, `internal/apiclient`, `internal/tui` (list shows `folder/name`), `internal/api/static`.

## Non-Goals / Named Follow-ups

- **TUI and macOS gallery views.** Both clients keep working (the TUI lists folder members flat as `folder/name`; macOS ignores the new `folder` field) but have no folder/gallery UI. Follow-up: native folder grouping in the TUI artifact browser and the macOS artifacts view.
- Serving a folder as a mini-site (HTML with relative asset links) — would need a different sandbox model than the blob-URL frame.
- EXIF orientation is not applied to thumbnails.

## 1. Model & storage
- [x] 1.1 `Artifact.Folder`, `ValidateArtifactRelPath`, folder limits; `folder` column + migration; `PruneFolderArtifacts`
- [x] 1.2 `artifacts.ResolvePath` accepts nested relative paths (symlink/traversal defenses kept)

## 2. Registration
- [x] 2.1 `artifact_register` directory support (walk, skip hidden/symlinks, limits, prune on re-register)

## 3. Serving & thumbnails
- [x] 3.1 `{filename...}` route; `?thumb=1` image/video thumbnailer with disk cache, pixel cap, bounded concurrency
- [x] 3.2 `apiclient` per-segment path escaping; TUI list shows `folder/name`

## 4. Argus Web
- [x] 4.1 Folder row + lazy thumbnail grid; full-screen prev/next; `SW_VERSION` bump

## 5. Docs & tests
- [x] 5.1 Tests (model, db, artifacts, mcp, api); gotchas; README reference tables

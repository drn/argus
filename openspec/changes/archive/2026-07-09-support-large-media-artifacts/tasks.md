## 1. Delta specs

- [x] 1.1 `mcp-server` delta: tiered cap + audio/video in the type list for
      `artifact_register` (this folder's `specs/mcp-server/spec.md`).
- [x] 1.2 `session-artifacts` delta: audio/video Content-Type + Range-request
      scenario for serving (this folder's `specs/session-artifacts/spec.md`).

## 2. Model layer (`internal/model/artifact.go`)

- [ ] 2.1 Add `ArtifactAudio`, `ArtifactVideo` to `ArtifactType` consts; add
      both to `ValidArtifactType`.
- [ ] 2.2 `InferArtifactType`: map `.mp3/.wav/.m4a/.flac/.ogg/.aac` → audio,
      `.mp4/.mov/.webm/.mkv/.m4v` → video.
- [ ] 2.3 `ArtifactContentType`: add audio/video branches backed by a small
      deterministic extension→MIME map (not `mime.TypeByExtension`, to avoid
      platform-dependent behavior across CI/dev), with a sane default
      (`audio/mpeg`, `video/mp4`) for unrecognized extensions of that type.
- [ ] 2.4 Add `MaxMediaArtifactBytes` (1 GiB) alongside the existing
      `MaxArtifactBytes` (25 MiB, unchanged, still the default/text/html/pdf/
      image cap). Add `MaxBytesForType(t ArtifactType) int64` resolving which
      cap applies.

## 3. MCP tool layer (`internal/mcp/artifacts.go`)

- [ ] 3.1 Update the `artifact_register` tool description: list audio/video,
      describe the tiered cap (25 MiB default, 1 GiB for audio/video).
- [ ] 3.2 Update the invalid-type error message to list audio/video.
- [ ] 3.3 `copyArtifact` takes the resolved `model.ArtifactType` and enforces
      `model.MaxBytesForType(atype)` instead of the hardcoded
      `model.MaxArtifactBytes` (both the pre-copy stat check and the
      `io.LimitReader`/post-copy check). Error messages report the actual cap
      that applied.

## 4. Web SPA (`internal/api/static/index.html`)

- [ ] 4.1 `artifactIcon`: add `audio`/`video` cases.
- [ ] 4.2 `makeArtifactNode`: add `<audio controls>` / `<video controls>`
      branches (new `.artifact-media` CSS class, `max-width: 100%`).
- [ ] 4.3 Add a small helper building the direct authenticated artifact URL
      (`API + /api/tasks/{id}/artifacts/{filename}?token=...`, same pattern
      `openStream` already uses for `EventSource`).
- [ ] 4.4 `openArtifactViewer` and `openArtifactFullscreen`: for `art.type ===
      'audio' | 'video'`, skip the `fetch()`+`blob()` step entirely and pass
      the direct URL straight to `makeArtifactNode` — playback starts
      immediately and the browser's native Range-request handling drives
      scrubbing without buffering the whole file in JS memory.
- [ ] 4.5 `downloadArtifact`: for audio/video, trigger the save via a plain
      anchor `download` click against the direct URL instead of
      `fetch()`+`blob()`, for the same reason.

## 5. Tests

- [ ] 5.1 `internal/model/artifact_test.go`: `TestInferArtifactType` cases for
      audio/video extensions; `TestValidArtifactType` accepts audio/video (the
      existing negative case using `ArtifactType("video")` as an example of an
      *invalid* type must be replaced — video is valid now); `TestArtifactContentType`
      cases for known audio/video extensions; fallback cases for unrecognized
      audio/video extensions (mirroring `TestArtifactContentType_ImageFallback`);
      a test for `MaxBytesForType` returning the media cap for audio/video and
      the default cap for everything else.
- [ ] 5.2 `internal/mcp/artifacts_test.go`: a registration test for an audio
      file sized between the default cap and the media cap succeeding; the
      existing `TestArtifactRegister_SizeCap` (text file, default cap) stays
      unchanged since `MaxArtifactBytes` itself didn't move.
- [ ] 5.3 `internal/api/artifacts_test.go`: extend
      `TestHandleGetArtifact_ContentTypeAndHeaders` with audio/video cases.

## 6. Docs

- [ ] 6.1 `context/knowledge/gotchas/web-remote.md` (session artifacts entry):
      note the direct-URL `?token=` media streaming pattern (bypasses
      `fetch().blob()` for audio/video so large files aren't buffered
      client-side; mirrors the existing `EventSource` query-token usage).
- [ ] 6.2 `context/knowledge/gotchas/misc.md` (or the MCP-adjacent section):
      note the tiered artifact size cap (`MaxBytesForType`) and that it exists
      because audio/video are streamed/scrubbed, not loaded whole.

## 7. Archive + gate

- [ ] 7.1 `openspec archive support-large-media-artifacts` (merge deltas into
      `openspec/specs/mcp-server/spec.md` and
      `openspec/specs/session-artifacts/spec.md`, move this change folder to
      `openspec/changes/archive/<date>-support-large-media-artifacts/`) before
      opening the PR.
- [ ] 7.2 `make pre-pr` clean.

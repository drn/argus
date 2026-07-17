## Why

`artifact_register` hard-caps every artifact at 25 MiB (`model.MaxArtifactBytes`) and only recognizes html/markdown/pdf/image/text — audio and video aren't valid types at all. Real trigger: a 108-minute DJ set converted to mp3 (192kbps, ~148 MB) had no path to registration; there's no bitrate that gets a full-length set under the current cap without unacceptable quality loss.

A time-boxed spike (`context/spikes/2026-07-09-artifact-registration-over-25mb.md`) found this is much smaller than it looks:

- The copy path already streams (`io.Copy` under a bounded `io.LimitReader` in `copyArtifact`) — it never buffers the whole file in memory. The cap is a bare constant, not a technical ceiling.
- `path` is a **local filesystem path** the daemon reads directly; there is no network upload step to chunk. (Confirmed: `internal/mcp/server.go` has no `--remote`-mode branching — the MCP server is daemon-local only. Agents always talk to the local daemon's MCP server regardless of whether the TUI itself is in `--remote` mode, so the "does remote change anything" open question from the spike is resolved: no.)
- Serving already supports HTTP Range via `http.ServeContent` (`internal/api/artifacts.go`), and the API already accepts `?token=` query auth (`internal/api/auth.go`, added for `EventSource`) — so a native `<audio>`/`<video>` element pointed at the artifact URL gets seeking/scrubbing for free.
- The one real gap on the client: the web SPA's viewer (`openArtifactViewer` / `openArtifactFullscreen` in `internal/api/static/index.html`) always does `fetch().blob()` before rendering — that fully buffers the artifact in browser memory regardless of type. For a new audio/video type this should be bypassed in favor of pointing the media element straight at the authenticated URL.

## What Changes

- **Tiered size cap.** `model.MaxArtifactBytes` (25 MiB) stays the ceiling for html/markdown/pdf/image/text — those are inlined/rendered as a whole. A new `model.MaxMediaArtifactBytes` (1 GiB) applies to the new audio/video types, which are streamed and scrubbed via Range requests rather than loaded whole. `model.MaxBytesForType(t)` resolves which cap applies; `copyArtifact` takes the resolved `ArtifactType` and enforces the type-specific cap instead of a single constant.
- **New `ArtifactType` values: `audio`, `video`.** Extension inference (`.mp3/.wav/.m4a/.flac/.ogg/.aac` → audio; `.mp4/.mov/.webm/.mkv/.m4v` → video), explicit-type validation, and Content-Type resolution (a small deterministic extension→MIME map, not the OS mime database, to avoid platform-dependent behavior in tests/CI) are added alongside the existing types.
- **`artifact_register` tool doc + validation** updated to list audio/video and describe the tiered cap.
- **Web SPA:** new icons for audio/video; `makeArtifactNode` gets `<audio controls>` / `<video controls>` branches. Both the paneled viewer and the full-screen viewer bypass `fetch().blob()` for audio/video and instead build the artifact URL directly (same `?token=` pattern `openStream` already uses for `EventSource`) so playback starts immediately and the browser's own Range-request machinery drives scrubbing without ever holding the full file in JS memory. `downloadArtifact` does the same for audio/video (a plain anchor `download` click against the direct URL) so a large media download doesn't blob-buffer either.
- **No server-side streaming/chunking protocol.** Already unnecessary per the spike — this is purely a cap + type-plumbing + client-rendering change.
- **Non-Goal: macOS.** `macos/` has no artifact list/viewer at all today (verified — no code beyond an unrelated string match). That's a pre-existing gap, not introduced here, and out of scope: implementing *any* artifact viewing on macOS (not just audio/video) is a separate follow-up change, per this repo's frontend-parity rule (an explicit named follow-up, not silence).

## Capabilities

### Modified Capabilities

- `mcp-server`: the "Viewable artifact registration" requirement's type list and size-cap language become tiered (audio/video get a larger cap); existing scenarios (path-required, invalid-type-rejected, oversized-rejected) are preserved, oversized-rejected scenario notes the cap is type-dependent.
- `session-artifacts`: the "Serve registered artifact bytes" requirement's type list adds audio/video with their Content-Type behavior.

## Impact

- **Code:** `internal/model/artifact.go` (new types, tiered cap, content-type map), `internal/mcp/artifacts.go` (tool doc, validation, `copyArtifact` per-type cap), `internal/api/static/index.html` (icon, renderer, direct-URL view/download for media).
- **Tests:** `internal/model/artifact_test.go`, `internal/mcp/artifacts_test.go`, `internal/api/artifacts_test.go` (Content-Type coverage for the new types).
- **Docs:** `context/knowledge/gotchas/web-remote.md` (session-artifacts entry gets a note on the direct-URL media streaming pattern) and `context/knowledge/gotchas/misc.md` or the MCP-adjacent gotcha file for the tiered-cap invariant.
- **No schema change, no new endpoints, no new MCP tools.** Reuses `?token=` auth and `http.ServeContent`'s existing Range support.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI/Make/Go-build wiring added or changed. The quality gate stays `make pre-pr`.

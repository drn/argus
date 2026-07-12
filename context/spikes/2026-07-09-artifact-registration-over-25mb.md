# Spike: Support Artifact Files Over 25 MiB (Streaming/Chunking?)

**Date:** 2026-07-09
**Question:** Can `mcp__argus__artifact_register` support files larger than its current 25 MiB cap (motivating case: a 148 MB mp3), and does that require a streaming or chunked-upload mechanism?
**Status:** Complete

## Summary

The 25 MiB cap is a bare constant (`model.MaxArtifactBytes`), not a consequence of in-memory buffering — the copy path already streams via `io.Copy` with a bounded `io.LimitReader`, and there is no network hop at all (the MCP tool takes a local filesystem `path` and the daemon does a local disk-to-disk copy). **No streaming/chunked-upload protocol is needed.** Raising or tiering the constant is sufficient on the write side. Serving already supports HTTP Range via `http.ServeContent`, and the API already accepts `?token=` query auth (used today for EventSource), so a native `<audio>`/`<video>` element can stream and seek a large artifact directly without the client ever holding the full file in memory. The real gaps are: (1) no `audio`/`video` `ArtifactType` and viewer, and (2) the **web client's current viewer path always does a full `fetch().blob()` before rendering**, which would still fully buffer a 148 MB file in the browser for every non-text type today — that needs to be bypassed for the new media types, not fixed at the server.

## Background

`internal/mcp/artifacts.go`'s `artifact_register` tool resolves the caller's task, sanitizes the destination filename, and calls `copyArtifact`, which does an `os.Open` + `io.Copy(tmp, io.LimitReader(src, MaxArtifactBytes+1))` from the source path (anywhere on disk, e.g. `/tmp`) into `~/.argus/artifacts/<task-id>/<filename>` via atomic temp-file-then-rename. `MaxArtifactBytes = 25 * 1024 * 1024` lives in `internal/model/artifact.go:38`, documented as an arbitrary "comfortably covers a report/PDF/screenshot" ceiling, not a technical constraint. The originating case: a 148 MB mp3 (108-min DJ set, 192kbps) had no path to registration since audio isn't even a valid `ArtifactType` today (html/markdown/pdf/image/text only).

## Findings

### 1. The copy path is already streamed, not buffered — the cap is arbitrary

`copyArtifact` (`internal/mcp/artifacts.go:140-194`) never reads the whole file into a Go `[]byte`; it streams disk→disk with `io.Copy` under a bounded reader. Memory use is O(copy buffer), not O(file size), already. So "switch to a streamed copy" (one of the handoff's proposed remaining-work items) is **already true** — there's nothing to change here except the numeric ceiling. This also means there's no meaningful *implementation* cost difference between allowing 25 MiB vs. 250 MiB vs. 1 GiB at this layer — it's a one-line constant change (plus deciding the new number and whether to tier it by type, see Finding 4).

### 2. There is no network upload step to chunk

The handoff's framing ("streaming? chunking?") implicitly assumes something like an HTTP multipart upload. It isn't: `artifact_register` is an MCP tool call whose `path` argument is a **local filesystem path already reachable by the unsandboxed daemon** (agent worktree or `/tmp`); the daemon does a same-machine file copy. There is no wire transfer of file bytes through the MCP JSON-RPC channel at all (only the path string). A chunked-upload wire protocol would only be needed if the daemon *couldn't* reach the source bytes directly (e.g. a remote agent talking to a remote daemon over `--remote`) — worth flagging as an open question (see below) but not the case for local/default usage, which is what the motivating case was.

### 3. Serving already supports Range requests — no server work needed for seeking

`handleGetArtifact` (`internal/api/artifacts.go:102`) calls `http.ServeContent(w, r, art.Filename, info.ModTime(), f)`, which natively handles `Range:` headers, conditional requests, and `HEAD`. Scrubbing/seeking a large audio/video artifact works today at the HTTP layer with zero changes — this answers the handoff's open question about range-request support affirmatively.

### 4. Query-param auth (`?token=`) already exists and unlocks native `<audio>`/`<video>` streaming

`internal/api/auth.go:184-194` accepts either `Authorization: Bearer` or `?token=` (added for `EventSource`, which can't set headers). This means a native `<audio src=".../artifacts/<file>?token=...">` or `<video>` element can be pointed directly at the artifact endpoint — the browser's own media pipeline issues ranged byte-range requests as the user scrubs, with no custom JS streaming/MediaSource code required and no need to hold the file in memory. This is the cleanest path to real seeking, much simpler than anything chunked-upload-shaped.

### 5. The web viewer's current code path fully buffers non-text artifacts client-side — this is the part that actually needs to change

`openArtifactViewer` in `internal/api/static/index.html:4216-4241` does, for every non-markdown/text type: `const blob = await r.blob(); ... const url = URL.createObjectURL(blob);`. That's a full in-memory fetch of the entire artifact before any rendering, regardless of file size. For a 148 MB file this works but is wasteful; more importantly, if audio/video is added as a type, this path should be bypassed in favor of pointing the media element's `src` straight at the authenticated URL (Finding 4) so playback starts immediately and range requests do the work. `makeArtifactNode` (`index.html:4250-4290`) would need a new `art.type === 'audio' | 'video'` branch analogous to the existing `image`/`pdf` branches.

### 6. No `audio`/`video` `ArtifactType` exists anywhere in the stack

`model.ArtifactType` (`internal/model/artifact.go:17-23`) only has html/markdown/pdf/image/text; `InferArtifactType` has no audio/video extension cases; `ArtifactContentType` has no audio/video branches (would need to derive from `mime.TypeByExtension` similar to the image case, e.g. `audio/mpeg`, `video/mp4`); the MCP tool's doc string and validation (`internal/mcp/artifacts.go:46,97`) reject anything else explicitly. Full plumbing (model → MCP validation/docs → API content-type → web viewer icon/renderer) is needed — a decently mechanical but multi-file change.

### 7. macOS app has zero artifact viewer today (pre-existing gap, out of scope)

`macos/Sources/ArgusKit` has no artifact list/view/serve code at all (only an unrelated string match in `DiffParser.swift`). This is a pre-existing parity gap unrelated to the size cap — CLAUDE.md's frontend-parity rule would apply to *any* new artifact capability (including this one), so shipping audio/video artifacts would need either a macOS implementation in the same PR or an explicit named follow-up per the parity policy, same as the web SPA gets today.

### 8. No storage quota / cleanup concern beyond what already exists

Artifacts are metadata-only in SQLite (`internal/db/artifacts.go`, schema has no BLOB column — just id/name/filename/type/size/timestamps); bytes live on disk under `~/.argus/artifacts/<task-id>/`. Task deletion already `os.RemoveAll`s the whole per-task artifact dir (`internal/api/handlers.go:606`, `internal/tui/app.go:5584`), so raising the cap doesn't introduce a new unbounded-growth path beyond "a live task's artifacts dir can now hold bigger files" — same lifecycle as today, just larger.

## Recommendation

**Proceed.** This is materially smaller than the handoff's framing suggested — no chunked-upload wire protocol, no streaming rewrite of the copy path (it already streams). The real scope is:

1. Raise/tier `MaxArtifactBytes` (trivial; decide the number — see Open Questions).
2. Add `audio`/`video` as first-class `ArtifactType`s through the full stack (model → MCP tool validation/docs → API content-type → web SPA icon + a native-`<audio>`/`<video>`-element render branch that does NOT go through `fetch().blob()`).
3. Point the new media renderer at the artifact URL with `?token=` directly, relying on existing Range support — do not build custom streaming.
4. Decide macOS scope per the frontend-parity rule (implement or name the follow-up).
5. Route through `openspec/` per this repo's CLAUDE.md — this is a behavioral change (new artifact type, new size ceiling, new/changed tool doc).

**Confidence:** High — the load-bearing constraints (buffering, network transfer, range support, auth) were all verified by reading the actual implementation, not inferred.
**Effort estimate:** M — mechanical plumbing across ~5 files plus a modest amount of new SPA renderer code; no new subsystem.

## Open Questions

- What should the new cap be, and should it be tiered by type (e.g. keep 25 MiB for html/text/markdown meant to be inlined; a much larger ceiling — or none — for audio/video meant to be streamed)? The handoff's sizing intuition (148 MB real case, no acceptable-quality full-length encode fits under any cap in the tens-of-MB range) argues for at least ~250 MB–1 GB for audio/video, but the disk-usage tradeoff (per-task artifact dirs, no total-vault quota anywhere in the system) should inform the number.
- Does `--remote` mode (a client agent talking to a daemon it doesn't share a filesystem with) change anything here? Not investigated — `artifact_register`'s `path` argument assumes local daemon filesystem access; if remote-mode agents can call this tool against artifacts that only exist on a *different* machine, that's the one scenario where an actual upload/streaming mechanism would be needed. Worth a follow-up look at `internal/apiclient`/`internal/apistore` before implementing, to confirm this path is truly local-only in all deployment modes.
- Should video get a first-class type too, or is this spike's audio motivating case enough to ship just `audio` now and add `video` later? The handoff only asked about mp3; the plumbing is identical either way so there's little reason not to do both at once.

## Next Steps

- [ ] Write an `openspec/changes/<name>/` proposal covering: new `audio`/`video` `ArtifactType`, revised/tiered size cap, MCP tool doc update, web SPA media renderer, macOS scope decision.
- [ ] Confirm `--remote` mode's artifact-registration semantics before finalizing the design (Open Questions).
- [ ] Pick the concrete size ceiling(s) and get sign-off (single number vs. tiered by type).
- [ ] Implement per the approved openspec tasks; add gotcha notes to `context/knowledge/gotchas/web-remote.md` (auth query-param reuse for media `src`) and wherever artifact invariants are documented.

## 1. Session viewer registry + min() chokepoint (`internal/agent`)

- [x] 1.1 `session.go`: add a `viewers map[string]viewerSize` (value `{cols, rows int}`)
      guarded by the session mutex. Add `SetViewerSize(id string, cols, rows int)`
      and `RemoveViewer(id string)`.
- [x] 1.2 Add `applyViewerMin()` (called under lock from both mutators): compute the
      per-dimension `min` over registered viewers; if it differs from the current
      `ptyCols/ptyRows`, call the existing `pty.Setsize` path; if unchanged, do
      nothing (no resize, no SIGWINCH). With an empty registry, return without
      resizing (keep last applied size).
- [x] 1.3 Make the existing `Resize(rows, cols)` internal/derived: it stays as the
      apply-to-PTY primitive but is no longer the viewer entry point. Remove viewer
      callers of `Resize` (TUI, API) in later sections.
- [x] 1.4 Resize-after-exit stays a no-op success and preserves the last size
      (existing behavior); registry mutations after exit also no-op.
- [x] 1.5 `iface.go`: add `SetViewerSize`/`RemoveViewer` to `SessionHandle` (and the
      provider interface as needed); keep size getters.
- [x] 1.6 `uxlog.Log("[pty] viewer set id=%s %dx%d -> min %dx%d")` and
      `"[pty] viewer remove id=%s -> min %dx%d (n=%d)"`; log the no-op-unchanged case
      at most rate-limited.
- [x] 1.7 Tests (`session_test.go`): smallest wins; remove-smallest grows back;
      unchanged-min issues no resize (assert Setsize call count via injected
      sizer / spy); hidden/last-viewer-removed keeps last size; after-exit no-op.

## 2. Daemon / supervisor RPC plumbing

- [x] 2.1 `internal/daemon/sessioncore.go`: replace/augment the `Resize` RPC with
      `SetViewerSize(taskID, id, cols, rows)` and `RemoveViewer(taskID, id)` RPCs
      proxied to the session; keep the apply path on the supervisor side.
- [x] 2.2 `internal/daemon/client/`: client-side methods mirroring the RPCs.
- [x] 2.3 In-process runner path (`runner.go`): same methods delegate directly to the
      session so local (non-daemon) mode behaves identically.
- [x] 2.4 `--remote` apistore/apiclient: route viewer size/remove to the REST
      endpoints (section 4) so a remote TUI participates in the same registry.
- [x] 2.5 Tests: daemon-client round-trip for SetViewerSize/RemoveViewer (short
      socket-path names); in-process path parity.

## 3. TUI: register on enter, release on exit (`internal/tui`)

- [x] 3.1 `app.go`: mint a stable per-App viewer ID (UUID) once (`viewerID`,
      `"tui-" + uuid.NewString()`), wired onto the agent pane via `SetViewerID` in
      `buildUI`. Removed the `onTaskSelect` and auto-start `ForceResyncPTY()` calls;
      registration now happens through the Draw → `SyncPTYSize` (`SetViewerSize`)
      path, which is the only threading-safe place to issue the daemon-client RPC.
- [x] 3.2 `terminal/terminalpane.go`: Draw posts the new size via `SetViewerSize`
      (debounced through `pendingResize*` + `SyncPTYSize`, gated on `lastPosted*`)
      instead of `Resize`. `forceResync` flag and `ForceResyncPTY` method DELETED
      (full removal, no shim) — `SetSession` resetting `lastPosted` to 0 subsumes the
      forced-resync semantics. Added `SetViewerID`. The dead/replay path still renders
      at panel dims and posts no claim.
- [x] 3.3 `exitAgentView` + every `SetSession(nil)`/task-switch path releases the
      claim: `TerminalPane.SetSession` calls `RemoveViewer(viewerID)` on the OUTGOING
      session (off the main thread) when the session pointer changes, so it covers
      exit, task-to-task nav (onTaskSelect → SetSession(newSess)), and clean/dirty
      exit. Task-to-task re-registers under the new session (lastPosted reset → Draw
      re-claims).
- [x] 3.4 Hera-view migrated: distinct per-pane viewer IDs minted in `NewHeraPage`;
      `ForceResyncPTY()` removed from `bindPane` and `reconcileOne` (the SetSession
      reset subsumes it). `SyncPanes` unchanged (still off-thread, now posts
      `SetViewerSize`).
- [x] 3.5 Removed `isRedundantAttach`/`lastAttachCols`/`invalidateAttachCache` and the
      same-cols short-circuit in `maybeKickRerender`. The kick is gated solely by
      `agent.ShouldKickRerender` (Skips when `InitialPTYSize ≈ panelCols`), so no
      double-kick. Deletes the field, init, and the delete-cleanup call.
- [x] 3.6 Smoke tests in `smoke_test.go`: `TestSmoke_AgentReentrySameSizeNoResize`
      (re-entry at the same size posts no effective resize) and
      `TestSmoke_AgentEntryWithSmallerViewerNoResize` (a smaller pre-registered viewer
      keeps the PTY pinned, no resize on TUI entry). Spy session emulates the
      per-dimension min registry and counts ACTUAL applies.

## 4. REST API: resize → viewer, disconnect → remove (`internal/api`)

- [x] 4.1 `handlers.go` `handleResize`: derive a per-connection viewer ID, validate
      dims (keep zero/out-of-range 400, no-live-session 404), then call
      `SetViewerSize(connID, cols, rows)` instead of `Resize`.
- [x] 4.2 In `handleStreamOutput`, alongside `defer sess.RemoveWriter(cw)`, add
      `defer sess.RemoveViewer(connID)` so `r.Context().Done()` (tab close/navigate)
      releases the claim. Ensure the resize endpoint and the stream share the same
      `connID` derivation (e.g. a client-supplied device/stream token).
- [x] 4.3 Add an explicit release route (or extend resize with a `release` flag) the
      SPA calls on `visibilitychange→hidden` to drop the viewer without closing the
      stream.
- [x] 4.4 Simplify `isRedundantResize` to match the registry model (registry handles
      the unchanged-min no-op).
- [x] 4.5 Tests (`handlers_test.go`): valid resize registers a viewer; cancelling the
      stream context removes it; explicit release removes it; invalid dims still 400;
      no live session still 404.

## 5. SPA visibility reporting (`internal/api/static`)

- [x] 5.1 `index.html`: add a `visibilitychange` listener — on hidden, POST the
      release for the current connection's viewer; on visible, re-assert
      `SetViewerSize` at the terminal's current `(cols, rows)`. Add a best-effort
      `pagehide` release (use `navigator.sendBeacon` if available).
- [x] 5.2 Ensure the device/stream token used for resize matches the one used for the
      stream so server-side `connID` lines up.
- [x] 5.3 Bump `SW_VERSION` in `sw.js` (shell asset changed).

## 6. Docs, gotchas, quality gate

- [x] 6.1 `context/knowledge/gotchas/pty-terminal.md`: add the `min()`-over-active-
      viewers invariant; "active = focused/visible, NOT merely connected"; "zero
      active viewers keeps last applied size, never resize to zero"; "viewers
      influence size only via SetViewerSize/RemoveViewer — direct Resize removed".
- [x] 6.2 README Reference: if any documented resize/`/resize` behavior changed,
      update the REST endpoint table in place (no top-half edit).
- [x] 6.3 `make pre-pr` green (build, vet, fmt-check, lint-pr, vuln, test-cover-gate
      ≥88; target ≥95% on `internal/agent` and touched packages).
- [ ] 6.4 Archive within this PR before merge: `openspec archive
      pty-smallest-viewer-sizing` (or manual merge-and-move), committed on the branch.

## 1. Emulated-screen detection (agent package)

- [x] 1.1 Add `agent.ScreenRenderer` — a reusable vt `SafeEmulator` reset via RIS
  between renders (one drain goroutine for its lifetime, à la
  `terminal.PreviewVT`); `render(tail, cols, rows)` writes the tail
  (panic-guarded `safeEmuWrite`) and returns the visible screen as plain text.
  Non-positive dimensions fall back to the session default (80×24).
- [x] 1.2 Factor `DetectNeedsInput` / `DetectSelectionPrompt` / `ContentFingerprint`
  into shared text-body helpers (`detectNeedsInputText` / `detectSelectionPromptText`
  / `fingerprintText`) so the same signals run against rendered screen text.
- [x] 1.3 Add `DetectNeedsInputScreen(r, buf, cols, rows)` — raw fast path, then
  emulate-on-miss; nil renderer == `DetectNeedsInput`.
- [x] 1.4 Add `SelectionPromptFingerprint(r, buf, cols, rows) (fp, ok)` for the
  content-stability pass — raw match fingerprints the raw tail (linear, identical
  to before); raw miss emulates the screen and, if the widget appears there,
  fingerprints the rendered text.

## 2. Daemon detector (internal/api/push.go)

- [x] 2.1 `computeNeedsInput` gains a `*agent.ScreenRenderer` + per-session
  `sizeOf` and routes all three passes through `DetectNeedsInputScreen` /
  `SelectionPromptFingerprint`.
- [x] 2.2 `idleWatcherState.screen` holds the reused renderer; `sessionScreenSize`
  reads the persisted size sidecar (`agent.LoadSessionSize`, non-blocking),
  falling back to 80×24.

## 3. TUI detector (internal/tui/app.go)

- [x] 3.1 `detectNeedsInput` + `detectNeedsInputSticky` route through the
  emulated-screen detection; `App.needsInputScreen` holds the reused renderer,
  `needsInputScreenSize` reads the size sidecar (non-blocking — safe on the tview
  main goroutine, unlike `PTYSize()`).

## 4. Tests (both directions)

- [x] 4.1 `agent.TestDetectNeedsInputScreen`: an alt-screen fixture with the `❯`
  painted out of byte order is MISSED by raw `DetectNeedsInput`/`DetectSelectionPrompt`
  but caught by `DetectNeedsInputScreen`; linear prompts still fire via the raw
  fast path; plain alt-screen output does not fire; renderer reuse across
  sizes stays correct.
- [x] 4.2 `agent.TestSelectionPromptFingerprint`: linear fingerprint equals
  `ContentFingerprint`; a parked alt-screen prompt is detected and stable across
  animation-only ticks; streaming alt-screen content without a prompt is never
  flagged; a changing transcript shifts the fingerprint.
- [x] 4.3 `tui.TestDetectNeedsInputSticky_AltScreen`: a never-idle alt-screen
  worker is flagged on the second (stable) tick; a streaming alt-screen agent is
  never flagged.
- [x] 4.4 Existing needs-input tests (BUG-023 / BUG-028 / BUG-032) still pass.

## 5. Docs

- [x] 5.1 Add a gotcha bullet (selection/needs-input detection must run against
  the EMULATED screen, not `StripANSI(raw)`, because alt-screen prompts are
  cursor-addressed) and bump the index count.

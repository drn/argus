## Why

**BUG-033 — a FULLSCREEN (alt-screen) agent parked at a selection prompt is
never flagged needs-input until the user opens its pane.**

Needs-input detection runs the selection-prompt regex over
`sanitize.StripANSI(rawTail)`. `StripANSI` removes SGR/OSC/CSI escapes but does
NOT apply cursor positioning. A fullscreen Claude agent paints its prompt with
cursor-addressed in-place redraws, so in the raw byte stream the `❯` selection
cursor and the `1.` first option are separated by — or even reordered relative
to — CSI cursor-move sequences (e.g. the cursor is painted last, to the left of
already-printed option text). After ANSI strip the glyphs are not linearly
adjacent, so the `❯[ \t]*1\.` regex never matches and the session is silently
never flagged. It only lights up once the user opens the pane, because the
resulting `ForceResyncPTY` → SIGWINCH makes Claude repaint a linear frame the
regex can match.

BUG-032's content-stability pass — which is what should catch a never-idle
parked prompt — is itself gated on `DetectSelectionPrompt(StripANSI(rawTail))`,
which fails for the same reason, so its fingerprint is never even computed for an
alt-screen prompt. The ring/disk-log bytes ARE present for unviewed sessions;
they are just un-linearized.

## What Changes

- **Detection matches the EMULATED screen, not `StripANSI(raw)`.** A throwaway vt
  emulator sized to the session's current PTY dimensions reconstructs the visible
  screen from the tail bytes; the existing selection regexes (and the
  content-stability fingerprint) then run against the rendered text, where the
  cursor-addressed glyphs line up as they actually display.
- **Cheapest-correct: emulate on a raw miss.** The raw-byte regex stays the fast
  path (linear / main-screen agents behave EXACTLY as before and never touch the
  emulator); only when it misses is the tail rendered and re-checked. This
  preserves linear behavior byte-for-byte while catching the alt-screen case.
- **Both detectors, in lockstep:** the daemon-authoritative signal
  (`internal/api/push.go computeNeedsInput`, reads the live ring) and the TUI rail
  (`internal/tui/app.go detectNeedsInputSticky`, reads the on-disk log) both gain
  the emulated-screen fallback, mirroring each other as before.
- **The fingerprint is computed over the emulated screen** when the raw signature
  is absent (alt-screen): a parked prompt's visible screen is naturally stable
  tick-to-tick, while a streaming agent's rendered content shifts — so the BUG-032
  false-positive guard (2-tick-stable + selection-signature) holds unchanged.

## Capabilities

### Modified Capabilities

- `idle-detection`: needs-input detection (selection-prompt UI and the never-idle
  content-stability pass) now matches against the emulated terminal screen, so a
  fullscreen/alt-screen agent whose prompt is painted with cursor positioning is
  flagged without a view-triggered repaint. Linear (main-screen) agents are
  unaffected — the raw-byte match remains the fast path.

## Impact

- **Modified code:**
  - `internal/agent/needsinput.go` — `ScreenRenderer` (reuse-via-RIS vt emulator,
    one drain goroutine for its lifetime) + `DetectNeedsInputScreen` /
    `SelectionPromptFingerprint` (raw fast path, emulate-on-miss); existing
    `DetectNeedsInput` / `DetectSelectionPrompt` / `ContentFingerprint` factored
    into shared text-body helpers.
  - `internal/api/push.go` — `computeNeedsInput` threads a `ScreenRenderer` +
    per-session size lookup (`agent.LoadSessionSize`, falls back to 80×24);
    `idleWatcherState.screen` holds the reused renderer.
  - `internal/tui/app.go` — `detectNeedsInput` / `detectNeedsInputSticky` use the
    emulated-screen detection against the on-disk log tail; `App.needsInputScreen`
    holds the reused renderer, size from the session-size sidecar.
- **No new key, no new dependency** (x/vt is already used), **no schema change, no
  daemon RPC, no new notification behavior.** `Session.IsIdle()` and the idle-push
  / `session.idle` paths are untouched. The emulation runs only in the
  watcher/tick, off the hot paint path; no `screen.Sync()`.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.

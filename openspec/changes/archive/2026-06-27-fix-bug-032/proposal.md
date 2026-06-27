## Why

**BUG-032 — a worker parked at a permission prompt is never flagged needs-input
when its session emits continuous redraw/animation bytes.**

Needs-input detection is idle-gated: the detector scans only sessions that have
produced no output for `idleThreshold` (~3 s), because a still-streaming agent
that transiently flashes `❯ 1.` is not actually blocked. But a Claude session
parked at a prompt — especially a FULLSCREEN / alt-screen one — emits a steady
trickle of redraw bytes (spinner, cursor blink, alt-screen repaint). Each byte
bumps the session's `lastOutput`, so `Session.IsIdle()` keeps returning false,
the session never enters the idle set, the detector never scans it, and the rail
/ `(?)` glyph / `/api/tasks` `needs_input` never light up. The existing sticky
carry-forward cannot help — it only re-checks sessions that were detected at
least once, and a never-idle session is never detected even once.

This is the literal original complaint behind BUG-028; the shipped BUG-028 fix
only handled an adjacent gate (a live coordinator whose task is `complete`).

## What Changes

- **A new content-stability signal flags a blocked-but-never-idle session.** The
  detector additionally scans RUNNING (not just idle) sessions, but to avoid
  false-positiving on a streaming agent it only flags one when BOTH the prompt
  signature is present AND its content fingerprint is unchanged from the previous
  tick. The fingerprint (`agent.ContentFingerprint`) strips animation/redraw
  chrome (spinner-glyph timing lines, the `❯` input/cursor line, blank lines,
  ANSI) and de-duplicates repaint frames, so two output tails that differ only in
  that chrome fingerprint identically, while a tail with genuinely new transcript
  content fingerprints differently.
- **The streaming false-positive guard is the fingerprint instability itself:** a
  still-producing agent's meaningful content changes every tick, so its
  fingerprint never matches the prior tick and it is never flagged by this pass.
- **Both detection paths are fixed:** the TUI rail/attention-bar
  (`internal/tui/app.go detectNeedsInputSticky`, reads the on-disk session log)
  and the daemon-authoritative signal that feeds `/api/tasks` + the PWA
  (`internal/api/push.go computeNeedsInput`, reads the live session ring). They
  mirror each other, as before.
- **The idle-gated fast path and the sticky carry-forward are unchanged** — the
  content-stability pass is additive. A session that DOES go idle is still flagged
  in ~3 s; only the never-idle case relies on the (slightly slower, tick-cadence)
  stability pass.

## Capabilities

### Modified Capabilities

- `idle-detection`: needs-input detection now also flags a running session that
  never reaches the idle set when it shows the prompt signature and its
  animation-stripped content is stable across detector ticks; a session whose
  content is still changing is never flagged by this pass.

## Impact

- **Modified code:**
  - `internal/agent/needsinput.go` — new `ContentFingerprint` (animation-stripped,
    frame-deduplicated, trailing-distinct-line hash) + `fingerprintVolatileLine`.
  - `internal/api/push.go` — `computeNeedsInput` gains a content-stability pass and
    threads a per-session fingerprint map across ticks (`idleWatcherState.contentFP`).
  - `internal/tui/app.go` — `detectNeedsInputSticky` gains the same pass, fingerprints
    persisted on `App.needsInputFP`.
- **No new key, no new dependency, no schema change, no daemon RPC.** `Session.IsIdle()`
  and the idle-push / `session.idle` paths are untouched (no new notification
  behavior); the change is confined to the needs-input detectors.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.

## Why

The macOS companion app's needs-input notifications have no rate limiting
across different tasks. `NotificationManager.notifyNeedsInput` dedupes per
task (it won't restack a banner for the *same* task while one is still
pending) but nothing limits how many *different* tasks can each post their
own banner within the same second. A burst of real state transitions —
catching up on an SSE backlog after a reconnect, or several tasks legitimately
hitting needs-input around the same time — fires one native notification per
task in rapid succession. Reported live by the user as "no flood gate...
noisy... unhelpful," recurring in bursts (confirmed self-terminating each
time, but happens repeatedly).

## What Changes

- Add `NotificationFloodGate` (ArgusKit): a pure, sliding-window burst
  detector. While arrivals stay under a small threshold within a short
  window, each posts its own banner immediately (no added latency for the
  common, sparse case). Once the threshold is crossed, the rest of that burst
  coalesces into a single, updating summary notification instead of one
  banner per task.
- `AppState.addNeedsInput` (the sole call site into
  `NotificationManager.notifyNeedsInput`) owns a `NotificationFloodGate`
  instance and decides, per arrival, whether to post individually or fold
  into the coalesced summary.
- `NotificationManager` gains a summary-notification post path (fixed
  identifier, so a growing burst updates one banner in place rather than
  restacking), alongside its existing per-task dedupe, which is untouched.
- Foreground/background gating stays explicitly out of scope: needs-input
  notifications have no such gate today (unlike idle notifications'
  `!NSApp.isActive` check), and this change doesn't add one.

## Capabilities

### Modified Capabilities

- `macos-app`: needs-input notifications no longer fire one-per-task
  unboundedly during a burst — arrivals beyond a small threshold within a
  short window coalesce into one summary notification, layered on top of
  the existing per-task dedupe.

## Impact

- **New:** `macos/Sources/ArgusKit/NotificationFloodGate.swift` (pure logic)
  and `macos/Tests/ArgusKitTests/NotificationFloodGateTests.swift`.
- **Modified:** `macos/Sources/Argus/NotificationManager.swift` (summary post
  path; now imports ArgusKit for the pure gate type),
  `macos/Sources/Argus/AppState.swift` (owns the gate instance, drives the
  per-arrival decision).
- No daemon/REST API changes — purely mac-app client-side behavior.
- Specs are LOCAL DOCS only (`openspec/project.md`); the quality gate stays
  `make pre-pr` (Go) plus `make mac-build`/`make mac-test` (Swift).

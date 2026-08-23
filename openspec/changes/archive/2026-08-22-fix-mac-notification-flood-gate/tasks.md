## 1. ArgusKit flood gate (pure, testable logic)

- [x] 1.1 Add `NotificationFloodGate` to `macos/Sources/ArgusKit/`: a sliding-window
  burst detector (`decide(taskID:taskName:now:) -> Decision`) with a
  documented `window` and `burstThreshold` constant, plus a
  `NotificationBurstSummary` value type (title/body derived from the
  coalesced task names, truncated once the list gets long).
- [x] 1.2 Write `NotificationFloodGateTests`: sparse arrivals each decide
  `.postIndividual`; arrivals beyond the threshold within the window decide
  `.coalesce`; the window resets after a quiet gap so a later isolated
  arrival goes back to `.postIndividual`; the same task id doesn't appear
  twice in one coalesced summary.

## 2. Wire the gate into the app

- [x] 2.1 `AppState` owns a `NotificationFloodGate` instance and, in
  `addNeedsInput`, calls `decide(...)` to choose between the existing
  individual-post path and a new coalesced-summary path.
- [x] 2.2 `NotificationManager` gains a summary-notification post method
  (fixed identifier so a growing burst updates one banner instead of
  restacking); update its header doc comment to reflect the now-imported
  ArgusKit pure type (still no dependency on the ArgusKit `Task` domain
  model). Leave the existing per-task dedupe (`pendingNeedsInput`) and
  `notifyIdle`'s foreground gate untouched.

## 3. Verify and document

- [x] 3.1 `make mac-build` and `make mac-test` pass.
- [x] 3.2 Add a gotcha bullet to `context/knowledge/gotchas/macos-app.md`
  documenting the window/threshold choice and reasoning.
- [x] 3.3 `openspec archive fix-mac-notification-flood-gate` (or the manual
  merge-and-move fallback) on the change branch before opening the PR, per
  this repo's CLAUDE.md.

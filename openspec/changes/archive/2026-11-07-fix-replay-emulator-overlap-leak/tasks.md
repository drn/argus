## 1. Tests (red first)

- [x] 1.1 Stale-after-ResetVT not published; alt-screen flag not recorded
- [x] 1.2 Rebind A to B while A in flight: gated, B's content published, emulator reused
- [x] 1.3 25 rebinds: <= 2 emulators created, drop counter 0, goroutines bounded
- [x] 1.4 Slow stale rebuild cannot overwrite fresh (fails on origin/master)
- [x] 1.5 Panic parks emulator and clears in-flight; drop tripwire counted

## 2. Implementation

- [x] 2.1 replayGen bumped by ResetVT, invalidateReplayCache, SetTaskID
- [x] 2.2 replayBuilding cleared only by the goroutine's single deferred release
- [x] 2.3 Second spare slot; stash/pop helpers with drop tripwire
- [x] 2.4 uxlog create/reuse/stale/drop counts

## 3. Docs and ship

- [x] 3.1 Gotcha in pty-terminal.md and index row
- [x] 3.2 make pre-pr, hera-review + test-adversary, archive in the PR

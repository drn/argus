# Tasks — fix-bug-036 (content-aware idle/active)

## 1. Content-idle helper (internal/agent)

- [ ] 1.1 Add `ContentIdleFingerprint(r *ScreenRenderer, buf, cols, rows)` → `(fp uint64, working bool)`: fingerprint the EMULATED screen (`fingerprintText`), report whether the "working" affordance (`needsInputWorkingRe`) is present. RED test first.
- [ ] 1.2 Add `ContentIdle(running, rawIdle, tailOf, sizeOf, screen, prev, now)` → `(idle []string, next *ContentIdleState)`: a non-raw-idle session whose fingerprint is stable for ≥ `idleThreshold` AND not "working" is content-idle. RED test first.
- [ ] 1.3 Tests both directions: parked fullscreen (stable, no affordance) → idle on the threshold tick; working fullscreen (affordance present) → never idle; streaming (content changing) → never idle; raw-idle sessions skipped; nil renderer falls back to raw.

## 2. Daemon idle-push (internal/api/push.go)

- [ ] 2.1 Add `contentIdle *agent.ContentIdleState` to `idleWatcherState`.
- [ ] 2.2 In `idleWatcherTick`, compute content-idle BEFORE the push loop and OR it into `idleSet`. RED test: a fullscreen session fires idle-push exactly once across many stable ticks; a working/streaming one never does; non-fullscreen unchanged.

## 3. Hera spinner (internal/tui/hera + internal/tui/app.go)

- [ ] 3.1 Add `SessionIdle bool` to `RoleView`; `IsActive() = Live && in_progress && !SessionIdle`. RED test.
- [ ] 3.2 Thread a `sessionIdle map[string]bool` through `BuildModel` → `buildRoleView` (live binding only).
- [ ] 3.3 `HeraPage.SetSessionIdle(ids)` + `doRefresh` passes it to `BuildModel`.
- [ ] 3.4 App `refreshTasksWithIDs`: compute content-idle (disk-log tails, reuse `needsInputScreen`), union with `idleIDs`, feed `heraPage.SetSessionIdle`.

## 4. Docs + spec

- [ ] 4.1 Delta specs: `idle-detection` (content-aware idle + once-on-transition push) and `hera-view` (spinner respects content-idle).
- [ ] 4.2 Gotcha note in `pty-terminal.md`/`events.md`; bump `index.md` count.
- [ ] 4.3 `make pre-pr` clean.

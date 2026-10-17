## 1. Process-scoped anchors

- [x] 1.1 Add failing external-restart bind regressions, including genuine new-process drift and an actual Hera revive.
- [x] 1.2 Scope anchors to the live PID, invalidate stale attach state and reject superseded async decisions.
- [x] 1.3 Cover successful/failed start and deletion cleanup; retain same-process anchor tests.

## 2. Delivery

- [x] 2.1 Update gotchas, run focused race suites and make pre-pr, validate and archive specs.
- [x] 2.2 Push through Iris, verify remote and report to coord.

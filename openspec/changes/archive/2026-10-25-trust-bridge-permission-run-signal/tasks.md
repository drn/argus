## 1. Mod

- [x] 1.1 Tracker tests: a running call never qualifies, a qualified call clears on running, permission confidence is `pending`.
- [x] 1.2 Tracker `markRunning` and `pending` confidence; heartbeat confidence follows.
- [x] 1.3 `ui.render` hook on `ToolProgress` marks the call running and returns `next(e)` unchanged, with a mod test.

## 2. Daemon

- [x] 2.1 Verdict and authority tests: `pending` permission is displayed without scraper corroboration; `pending-or-running` and missing confidence still need it.
- [x] 2.2 `ConfidencePending` constant and `UncertainPermission` update.

## 3. Delivery

- [x] 3.1 Gotchas, spawn surface digest, mod checks, full gate.
- [x] 3.2 Archive the change, validate strictly, open the PR.

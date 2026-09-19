## 1. Submission protocol tests

- [x] 1.1 Extend the notifier session fake with monotonic output tracking and controllable CR consumption.
- [x] 1.2 Add a regression test where the first CR is swallowed and a later standalone CR is acknowledged.
- [x] 1.3 Add coverage for exhausted acknowledgment retries retaining pending state and for delayed text-consumption settling.

## 2. Reliable submit implementation

- [x] 2.1 Expose `TotalWritten` through the notifier's narrow session interface.
- [x] 2.2 Replace the fixed delay with bounded output-settle and post-CR acknowledgment waits.
- [x] 2.3 Retry standalone CR with backoff and record success only after acknowledgment.

## 3. Diagnostics and documentation

- [x] 3.1 Add daemon-visible structured logs for delivery gates, attempts, retries, success, cancellation, deadline, and write failures while retaining UX logging.
- [x] 3.2 Add tests that pin structured success and failure diagnostics.
- [x] 3.3 Update `context/knowledge/gotchas/messaging.md` with the output-acknowledgment, retry, pending-state, and daemon-log invariants.

## 4. Verification and archive

- [x] 4.1 Run targeted notify tests and `openspec validate --all --strict`.
- [ ] 4.2 Archive the change into base specs in the same branch.
- [ ] 4.3 Run `make pre-pr`, commit, push, and open the PR through Iris.

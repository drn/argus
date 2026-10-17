## 1. Correctness

- [x] 1.1 Remove redundant per-task width persistence and preserve in-memory anchors through the intermediate-Skip regression.
- [x] 1.2 Publish recent pane geometry off the UI thread and use it for fresh headless/Hera spawns with explicit-size and stale-report tests.
- [x] 1.3 Separate bounded width repair from ordinary kick callers, add production logs, and distinguish pending status.

## 2. Adversarial tests

- [x] 2.1 Strengthen stop timing, cancellation, descendant, ordinary stop, and runner restart tests.
- [x] 2.2 Assert bounded RPC routing on both hops, error propagation, exact border placement, Hera status, and exit cleanup.

## 3. Delivery

- [ ] 3.1 Resolve coordinator questions, rebase onto updated #1053, run focused suites and make pre-pr, and archive the specs.
- [ ] 3.2 Update #1054 and report review disposition and evidence to coord.

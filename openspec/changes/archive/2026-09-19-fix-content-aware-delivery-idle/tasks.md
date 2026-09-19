## 1. Specification

- [x] 1.1 Document the content-aware idle contract for recycle and notify.
- [x] 1.2 Document the prolonged recycle-wait diagnostic.
- [x] 1.3 Validate the change artifacts strictly.

## 2. Test-driven implementation

- [x] 2.1 Add failing tests for a reusable primary-screen content-idle tracker and working/empty guards.
- [x] 2.2 Add failing tests for recycle and notify content-aware gating.
- [x] 2.3 Add failing tests for prolonged recycle-wait logging and lifecycle cleanup.
- [x] 2.4 Implement the shared tracker and wire both consumers.
- [x] 2.5 Implement recycle-wait diagnostics.

## 3. Documentation and verification

- [x] 3.1 Add the root-cause and fix gotcha to `context/knowledge/gotchas/events.md`.
- [x] 3.2 Run focused tests and the full `make pre-pr` gate.
- [x] 3.3 Archive and strictly validate the OpenSpec change.
- [x] 3.4 Commit, push, and open the pull request.

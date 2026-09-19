## 1. Blocking inbox implementation

- [x] 1.1 Add database tests for fast-path, delayed-arrival, and cancellation behavior using Hera's NULL unread semantics.
- [x] 1.2 Implement the context-aware Hera inbox wait and expose it through the Hera service.
- [x] 1.3 Add MCP tests for timeout validation, timeout result, fast-path consumption, and delayed message arrival.
- [x] 1.4 Add the optional `timeout_seconds` schema field and bounded shutdown-aware wait to `hera_inbox`.

## 2. Agent guidance and documentation

- [x] 2.1 Update the built-in Hera skill to recommend blocking `hera_inbox` instead of timer-based polling.
- [x] 2.2 Update the gater-materialized worker orientation to use the blocking inbox wait.
- [x] 2.3 Update the README reference and record the wait invariants in the Hera message-bus gotchas.

## 3. Verification and delivery

- [x] 3.1 Run focused tests and strict OpenSpec validation.
- [x] 3.2 Archive the OpenSpec change into the base capability spec.
- [x] 3.3 Run `make pre-pr`, commit, push through Iris, and open a pull request.

## 1. Regression coverage

- [x] 1.1 Add failing tests for exact-ID/account availability, deferred kicks and fresh relaunch with the original prompt.
- [x] 1.2 Cover RPC identity propagation, TUI deferred retries and REST resize retry eligibility.

## 2. Implementation

- [x] 2.1 Add family availability capability and shared launch fallback.
- [x] 2.2 Guard all kick paths and propagate effective launch identity.
- [x] 2.3 Add gotcha documentation and update the knowledge index.

## 3. Validation and delivery

- [x] 3.1 Run focused tests, full race and coverage validation, and review changes and tests.
- [x] 3.2 Archive the change and validate all specs.

## Validation notes

- Build, vet, formatting, lint and vulnerability checks passed with Go 1.26.9.
- All packages passed race and coverage testing with a temporary 300-second test limit; filtered coverage is 88.5% against the unchanged 88% floor.
- Canonical `make pre-pr` reaches the documented cumulative TUI 120-second timeout. The coordinator approved delivery with that limitation recorded; the repository gate is unchanged. Complete TUI runs passed in 176.361 seconds standalone and 170.027 seconds in the final full suite.
- The six added or modified TUI tests complete in 0.11–0.20 seconds each. The RPC teardown fix passed five repeated race runs.

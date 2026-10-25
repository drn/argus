## 1. Contract and prerequisites

- [x] 1.1 Verify phase 2 authority seams and read approved proposal, delivery requirements and messaging gotchas.
- [x] 1.2 Resolve submit/ack ambiguity and obtain the full plugin-authoring skill.
- [x] 1.3 Complete capability deltas and validate the change.

## 2. Settings

- [x] 2.1 Write failing tests for default/overlay/runtime delivery gating and Bridge Settings persistence.
- [x] 2.2 Implement config overlay and extensible TUI Bridge toggles.

## 3. Delivery

- [x] 3.1 Write failing notifier/API tests for idle gates, claims, cancellation, deadlines, stale clients, retry and PTY fallback.
- [x] 3.2 Implement shared-queue bridge delivery routing and authenticated pull/authorization/ack endpoints.
- [x] 3.3 Write failing mod tests and implement idle-only polling/submission and acknowledgment.

## 4. Coordinator budget

- [x] 4.1 Write tests covering non-blocking budget delivery, increment cadence, legacy disabled/dead paths, context stamps and forced recycle.
- [x] 4.2 Implement queued idle budget nudges and authority-gated Stop-hook behavior.

## 5. Delivery and validation

- [x] 5.1 Update Hera skill/tool descriptions, gotchas, uxlog and README Reference.
- [x] 5.2 Pass plugin validation, TypeScript checking and plugin tests.
- [x] 5.3 Live-verify draft-preserved delivery, dead-mod fallback and delivery-disabled legacy behavior using isolated tmux/Runner/API harness.
- [x] 5.4 Run code review and test-adversary review, resolve findings, and validate the full gate plus the coordinator-approved isolated TUI rerun after the paste smoke-test fix.
- [x] 5.5 Validate all OpenSpec changes strictly, commit, push via iris and open one stacked PR against argus/argus-bridge/2a-truth; do not archive or merge. [PR #1071](https://github.com/drn/argus/pull/1071).

The final-tree full gate passed build, vet, formatting, lint, vulnerability scanning and every test package except the pre-existing paste smoke-test flake. Its dropped end-of-paste event was fixed without weakening the assertion or widening the timeout; 20 targeted race repetitions and the complete standalone TUI race suite (170s) passed. Per coordinator instruction, no further full-gate rerun was required; CI performs the final full-suite check. Native plugin validation, TypeScript checking, 50 plugin tests and strict OpenSpec validation passed. No changes were archived.

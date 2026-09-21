## 1. Delivery safety

- [x] 1.1 Normalize soft-wrap whitespace when verifying injected composer content.
- [x] 1.2 Use fallback acknowledgment for a known but unobservable composer and recognize annotated stale notices.
- [x] 1.3 Add and enforce a cross-tick total Enter-attempt ceiling with error-level abandonment logging.

## 2. Regression coverage and documentation

- [x] 2.1 Cover wrapped composer acknowledgments at 80 and 120 columns, known-unobservable fallback, bounded termination, and annotated-retry growth prevention.
- [x] 2.2 Record the acknowledgment and bounded-retry invariants in messaging gotchas.

## 3. Verification and archival

- [x] 3.1 Run notifier tests, OpenSpec validation, and `make pre-pr` (the local gate's only failure is the documented advisory Go 1.26.3 vulnerability scan; all remaining gates passed).
- [x] 3.2 Archive the validated OpenSpec change with the implementation.

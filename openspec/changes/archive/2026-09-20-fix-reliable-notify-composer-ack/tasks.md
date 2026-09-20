## 1. Composer verification

- [x] 1.1 Replace output-only submit acknowledgment with rendered-composer confirmation.
- [x] 1.2 Verify notice-only Ctrl+U clears before direct replacement and preserve on failed verification.

## 2. Regression coverage and documentation

- [x] 2.1 Add busy-output and failed-stale-clear notifier regression tests.
- [x] 2.2 Record the busy-PTY acknowledgment invariant in messaging gotchas.

## 3. Verification and archival

- [x] 3.1 Run notifier tests and `make pre-pr` (the advisory-only vulnerability scan reports existing Go 1.26.3 findings).
- [x] 3.2 Archive the validated OpenSpec change with the implementation.

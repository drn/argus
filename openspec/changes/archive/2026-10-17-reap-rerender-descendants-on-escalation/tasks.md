## 1. Escalation cleanup

- [x] 1.1 Add failing real-process tests for escalation reaping, detached descendants, outsider preservation and prompt PTY drain.
- [x] 1.2 Implement identity-scoped descendant capture and short-grace cleanup only after root SIGKILL succeeds.
- [x] 1.3 Cover tree selection, identity reuse, inspection/signal errors and cooperative exit preservation.

## 2. Delivery

- [ ] 2.1 Update supervisor stream identity and gotchas, run focused suites and make pre-pr, validate/archive specs.
- [ ] 2.2 Push through Iris, verify remote and report to coord.

## 1. Implementation and targeted verification

- [x] 1.1 Add failing delivery tests for Claude, Codex, and OpenCode, including fresh/resume and stale config.
- [x] 1.2 Share the inbox cap and derived client deadline; configure existing injection paths and logging.
- [x] 1.3 Run targeted agent, MCP, and OpenCode tests and document timeout gotchas and parity scope.

## 2. Integration and delivery

- [ ] 2.1 Wait for the concurrent spawn PR merge, fetch and rebase, verify ancestry, then bump and re-record the supervisor spawn surface.
- [ ] 2.2 Review the changes and tests, archive this change into base specs, and validate specs.
- [ ] 2.3 Obtain the full gate slot, run make pre-pr with the coordinator's toolchain settings, then push and open a PR through iris.

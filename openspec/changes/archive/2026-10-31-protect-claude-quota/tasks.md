## 1. Approval and regression tests

- [x] 1.1 Obtain coordinator approval before implementation.
- [x] 1.2 Add failing tests for both quota windows, independent expiry, and headless session parsing.
- [x] 1.3 Add failing tests for account tiers, pins, invalid accounts, backend constraints, and coordinator routing.

## 2. Usage and routing

- [x] 2.1 Implement independent window retention/selection for bridge and hidden-probe paths.
- [x] 2.2 Implement validated backend/account choices and creation precedence with emit-both logs.
- [x] 2.3 Route coordinator creation through Claude-capable tiers and preserve explicit overrides.

## 3. Persistence and surfaces

- [x] 3.1 Persist optional account in TOML/DB and edit/display it in TUI Settings with tests.
- [x] 3.2 Relay the fullest valid Claude reading to the status bar with tests.
- [x] 3.3 Inspect accessible spawn evidence and tighten builtin skills/MCP guidance.
- [x] 3.4 Rewrite routing gotchas and update README Reference configuration.

## 4. Validation and delivery

- [x] 4.1 Run targeted tests, review and test-adversary audits, and make pre-pr (including the full race suite and coverage gate).
- [x] 4.2 Archive change, validate all specs strictly, commit, push via iris, and open PR via iris.
- [x] 4.3 Report PR, exact checks, limitations, and personal-overflow config to coordinator.

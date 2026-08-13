## ADDED Requirements

### Requirement: Retention-swept resume failure surfaces an explanatory notice

When a session exit is classified as a retention-swept resume failure (see `agent-execution`), the TUI SHALL set a status-bar notice explaining that Claude Code's transcript for that session was likely deleted by its own `cleanupPeriodDays` retention sweep — not an Argus failure — and pointing at Settings → System / `argus doctor` for the fix, using the same transient notice mechanism (`SetError`/`SetInfo`, `StatusNoticeTTL`) as other status-bar notices. A generic (non-matching) exit SHALL NOT trigger this notice.

#### Scenario: Matching resume failure sets the notice

- **WHEN** `handleSessionExitUI` processes a non-clean exit whose last output matches the retention-swept signature
- **THEN** the status bar shows the explanatory notice for the standard notice TTL

#### Scenario: Generic crash does not trigger the notice

- **WHEN** `handleSessionExitUI` processes a non-clean exit whose last output does not match the retention-swept signature
- **THEN** no retention-specific notice is shown

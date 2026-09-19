## ADDED Requirements

### Requirement: Stateful content-aware idle classification is reusable by daemon consumers

The system SHALL expose a stateful content-aware idle classifier for daemon consumers that need to recognize a genuinely parked session despite ongoing cosmetic PTY redraw bytes. The classifier SHALL report idle immediately when the raw session idle predicate is true. Otherwise it SHALL reuse the existing animation-stripped emulated-screen stability signal, working-affordance exclusion, and parked-prompt escalation, carrying detector state across calls without changing `Session.IsIdle()` itself.

The classifier SHALL inspect a bounded substantive recent-output tail and render it using the session's current PTY dimensions. It SHALL work for both alternate-screen applications and ordinary primary-screen scrollback. Empty output SHALL not be treated as content-idle.

#### Scenario: Raw-idle session is immediately idle

- **WHEN** a session's raw idle predicate is true
- **THEN** the reusable classifier reports idle without waiting for content-stability convergence

#### Scenario: Primary-screen cosmetic redraw converges to idle

- **WHEN** an ordinary primary-screen session remains raw-busy because it redraws only cosmetic status chrome, its meaningful screen content remains unchanged for the stability threshold, and the working affordance is absent
- **THEN** the reusable classifier reports the session content-idle

#### Scenario: Working affordance prevents false idle

- **WHEN** a raw-busy session's screen is stable but still shows the agent's working affordance
- **THEN** the reusable classifier does not report the session content-idle

#### Scenario: Empty output is not content-idle

- **WHEN** a raw-busy session has no recent output to classify
- **THEN** the reusable classifier does not report the session content-idle

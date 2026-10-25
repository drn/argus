## ADDED Requirements

### Requirement: Uncertain permission input is corroborated per field

A live bridge SHALL remain the source throughout pending-or-running permission intervals. Consumers SHALL preserve bridge busy state and resolve needs-input as bridge input AND the raw scraper needs-input for uncertain permissions. Exact permission, question and plan input SHALL use bridge state directly. Missing permission confidence SHALL be treated as uncertain. Source logs SHALL fire only for real liveness or authority transitions, never for an uncertain field alone. REST, daemon-connected TUI, web, macOS, push and shadow consumers SHALL apply the same rule.

#### Scenario: Hook-approved long tool execution

- **WHEN** a busy live bridge reports a pending-or-running permission and the scraper reports no input request
- **THEN** it remains busy without needs-input and without a source transition

#### Scenario: Real permission dialog

- **WHEN** both a live uncertain permission and the scraper report needs-input
- **THEN** needs-input is true and the session is quiescent while bridge busy remains unchanged

#### Scenario: Exact permission

- **WHEN** a live bridge reports an exact permission request and the scraper disagrees
- **THEN** needs-input is true immediately

#### Scenario: Prior exact input must not corroborate a later uncertain permission

- **WHEN** an exact dialog was published and a later uncertain permission arrives without raw scraper input
- **THEN** the previous published verdict does not corroborate the uncertain permission

#### Scenario: Telemetry hook unavailable

- **WHEN** the engine refuses first-party telemetry observation for user mods
- **THEN** the mod retains its existing grace-period pending-or-running fallback

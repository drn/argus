# Argus bridge specification

## Purpose

Define how live Claude bridge reports and terminal inference combine into consistent session verdicts across daemon clients.
## Requirements
### Requirement: Uncertain permission input is corroborated per field

A live bridge SHALL remain the source throughout pending-or-running permission intervals. Display consumers SHALL preserve bridge busy state and resolve needs-input as bridge input AND the raw scraper needs-input for uncertain permissions. Exact permission, question and plan input SHALL use bridge state directly. Missing permission confidence SHALL be treated as uncertain. Source logs SHALL fire only for real liveness or authority transitions, never for an uncertain field alone. REST status, daemon-connected TUI, web, macOS and push SHALL apply the same display rule. The separate shadow permission diagnostic bucket SHALL compare raw bridge needs-input against scraper input. Delivery routing and claim-settlement evidence SHALL retain raw bridge needs-input as a conservative safety gate.

#### Scenario: Hook-approved long tool execution

- **WHEN** a busy live bridge reports a pending-or-running permission and the scraper reports no input request
- **THEN** it remains busy without displayed needs-input and without a source transition

#### Scenario: Real permission dialog

- **WHEN** both a live uncertain permission and the scraper report needs-input
- **THEN** displayed needs-input is true and the session is quiescent while bridge busy remains unchanged

#### Scenario: Exact permission

- **WHEN** a live bridge reports an exact permission request and the scraper disagrees
- **THEN** displayed needs-input is true immediately

#### Scenario: Prior exact input must not corroborate a later uncertain permission

- **WHEN** an exact dialog was published and a later uncertain permission arrives without raw scraper input
- **THEN** the previous published verdict does not corroborate the uncertain permission

#### Scenario: Telemetry hook unavailable

- **WHEN** the engine refuses first-party telemetry observation for user mods
- **THEN** the mod retains its existing grace-period pending-or-running fallback

#### Scenario: Raw permission disagreement remains observable

- **WHEN** raw bridge permission input is true and scraper input is false beyond the shadow settling period
- **THEN** disagreement counts under needs_input_permission while the headline needs_input counters remain unaffected

#### Scenario: Delivery blocks despite display corroboration clearing input

- **WHEN** the bridge is idle with raw uncertain permission input true and scraper input false
- **THEN** delivery is blocked and a fresh heartbeat does not establish settled absence for a claim

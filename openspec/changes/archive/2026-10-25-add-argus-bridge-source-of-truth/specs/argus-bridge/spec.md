## ADDED Requirements

### Requirement: Bridge authority belongs to the current running session

The daemon SHALL treat bridge state as authoritative only for a running Claude session whose current identity and process generation match the report and whose last event is less than 45 seconds old. Disabled, absent, stale, or superseded reports SHALL fall back to scraping with a logged source transition. The read API SHALL expose the same liveness decision used by consumers.

#### Scenario: Restart with the same transcript

- **WHEN** a task restarts or recycles, including a restart reusing its transcript id
- **THEN** its previous reports are not live until the new mod reports

#### Scenario: Missing heartbeat

- **WHEN** no event has arrived for 45 seconds
- **THEN** authority returns to the scraper automatically

#### Scenario: Daemon restarts during a busy turn

- **WHEN** a heartbeat repopulates an empty daemon hub
- **THEN** its busy, dialog kind and confidence snapshot reconstructs current state rather than assuming idle

#### Scenario: Concurrent hooks and late posts

- **WHEN** hook event creation overlaps or a retired mod boot posts late
- **THEN** event creation preserves invocation order without awaiting network delivery and retired boots cannot regain authority

### Requirement: Measurement freshness is independent of heartbeat freshness

The hub SHALL record measurement time separately and SHALL clear session-scoped context and cost on a session change.

#### Scenario: Heartbeats without measurements

- **WHEN** a bridge continues heartbeating without a fresh measurement
- **THEN** an old rate-limit reading does not become fresh


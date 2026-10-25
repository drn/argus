## ADDED Requirements

### Requirement: Uncertain permission input does not discard bridge busy state

For live bridges the system SHALL derive quiescence as not busy OR resolved needs-input, where only uncertain permission needs-input requires scraper corroboration. Busy state SHALL remain bridge-owned regardless of scraper idle. Stale, missing and disabled bridges SHALL retain scraper behavior.

#### Scenario: Scraper appears idle during approved execution

- **WHEN** a live bridge is busy with an uncertain permission, the scraper appears idle, and the scraper reports no input request
- **THEN** task responses, TUI spinner gates and notifications treat the task as working

#### Scenario: Bridge becomes stale

- **WHEN** the bridge loses liveness during a pending permission interval
- **THEN** all fields return to scraper behavior and the source transition is logged

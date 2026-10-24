## ADDED Requirements

### Requirement: Scraper verdicts are observed against bridge state
The daemon's idle watcher SHALL, once per tick and after computing its final screen-reading idle set and needs-input set, hand both to the bridge shadow comparator together with the running set. This observation SHALL be side-effect free with respect to the verdicts themselves.

#### Scenario: Observation does not alter verdicts
- **WHEN** the comparator observes a tick
- **THEN** the idle set, needs-input set, emitted events and pushes are identical to a daemon without the comparator

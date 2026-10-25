## ADDED Requirements

### Requirement: Bridge measurement supplies binding context

A usable live session.measure SHALL stamp context tokens and percent on the binding without transcript rereading. The Stop hook SHALL retain its other duties and use transcript fallback when no usable live bridge measurement exists.

#### Scenario: Live context measurement

- **WHEN** the current bridge reports context tokens and percent
- **THEN** the binding receives those values and the Stop hook does not scan the transcript for them


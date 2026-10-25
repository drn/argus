## ADDED Requirements

### Requirement: Fresh bridge rate limits precede hidden probes

Usage routing SHALL prefer the freshest usable weekly session.measure reading among live Claude bridges on the requested account. The headless probe SHALL run only when no fresh applicable reading exists. Default and named accounts SHALL remain isolated.

The headless probe SHALL remain default-account-only. A named account with no fresh bridge reading SHALL return unknown and fail open; per-account headless probes are a named follow-up.

#### Scenario: Live account reading

- **WHEN** a live bridge on the requested account has a fresh weekly limit
- **THEN** usage routing uses that percentage without launching a hidden Claude session

#### Scenario: Different account or stale reading

- **WHEN** only another account or stale measurements exist
- **THEN** they are not used and the existing fallback remains available


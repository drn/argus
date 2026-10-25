## ADDED Requirements

### Requirement: Idle-only mod delivery

For a live Claude bridge with bridge.delivery enabled, the daemon SHALL offer shared-notifier deliveries through authenticated claim, authorization and acknowledgment endpoints. The mod SHALL poll after turn.complete and on a gentle idle timer, SHALL submit at most one authorized delivery through $.prompt.submit while locally idle with no pending input dialog, and SHALL leave the user's unsent draft untouched. Every submission SHALL carry a trailing delivery-ID marker. A new mod session SHALL reconcile outstanding claims before requesting new deliveries.

#### Scenario: Draft survives delivery

- **WHEN** the mod delivers an authorized notice while an unsent draft exists
- **THEN** a plugin-framed turn starts and the draft remains unchanged

#### Scenario: Resume reconciles an outstanding claim

- **WHEN** a new authoritative launch resumes a session with an expired authorized claim
- **THEN** the transcript marker completes a submitted delivery without retry, or positive absence after the original launch ended permits re-offer before new work

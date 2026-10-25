## ADDED Requirements

### Requirement: Bridge budget nudge is non-blocking

When bridge.delivery is enabled and phase 2 authority identifies the coordinator's live bridge, the Stop hook SHALL enqueue the budget nudge into the shared mod delivery path, and SHALL emit no blocking decision. The mod SHALL submit the nudge as a plugin-framed turn only while idle and outside input dialogs, with the same deduplication, acknowledgment and recovery contract as message delivery. Cadence SHALL match the existing Stop hook: fire at the budget on the first over-budget episode, then only after context_size grows by CoordinatorNudgeIncrement beyond the last nudge; pending recycle SHALL suppress ordinary nudges. Context-size stamping, request_recycle and forced recycle at 1.5x budget SHALL remain effective. With either bridge setting off or bridge authority absent, the ordinary Stop-hook nudge SHALL remain byte-identical. The mod SHALL NOT register prompt.compose for budget guidance because managed organization policy skips that hook.

#### Scenario: Increment throttle applies to mod nudges

- **WHEN** a bridge coordinator remains over budget without a full increment of context growth since the prior nudge
- **THEN** no additional budget delivery is enqueued

#### Scenario: Budget enqueue response is lost

- **WHEN** the budget enqueue POST may have succeeded but its transport fails or returns an ambiguous server response
- **THEN** the hook lets Stop complete and stamps the nudge cadence without also emitting a blocking nudge; explicit disabled or unavailable responses retain legacy blocking

#### Scenario: Legacy budget nudge with delivery off

- **WHEN** bridge.delivery is false while a coordinator's bridge is live and its nudge gate permits an over-budget nudge
- **THEN** the Stop hook returns the existing blocking nudge

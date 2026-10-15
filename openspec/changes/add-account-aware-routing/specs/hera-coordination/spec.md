## ADDED Requirements

### Requirement: Coordinator and sub-coordinator spawn select a Claude account from an ordered list

When `[backend_routing].coordinator_accounts` is configured, a newly spawned hera coordinator (`SpawnHeraCoordinator`) or sub-coordinator (`MaterializeHeraSubCoordinator`) with no explicit account SHALL walk that list in order and run on the first account whose cached weekly Claude reading is below `coordinator_threshold_pct`. An unknown or stale reading SHALL count as available. An account that is not configured, cannot run Claude, or whose folder is not signed in SHALL be skipped and logged. Accounts that resolve to the same signed-in identity SHALL be treated as one, with `default` preferred (see `backend-tier-routing`). If every listed account is at or above the limit, the first listed usable account SHALL be used, so a coordinator spawn never fails for lack of headroom. An explicit account SHALL always win. This selection SHALL NOT change the coordinator's backend, which remains governed by "Coordinator and sub-coordinator spawn always resolve to a Claude-capable backend". When `coordinator_accounts` is unset, coordinator account resolution SHALL be unchanged.

#### Scenario: Work under the coordinator limit is used

- **WHEN** `coordinator_accounts = ["default", "personal"]`, `coordinator_threshold_pct = 95`, and `default` (work) is at 80%
- **THEN** a new coordinator runs on `default`

#### Scenario: Work over the coordinator limit falls back to personal

- **WHEN** `default` (work) is at 97% and `personal` is at 36%, with the same configuration
- **THEN** a new coordinator runs on `personal`

#### Scenario: Workers stop before coordinators do

- **WHEN** `default` is at 80%, its worker tier threshold is 70, and `coordinator_threshold_pct` is 95
- **THEN** routed workers do not select `default`, while a new coordinator still runs on `default`

#### Scenario: All listed accounts over the limit

- **WHEN** every account in `coordinator_accounts` is at or above `coordinator_threshold_pct`
- **THEN** the coordinator runs on the first listed usable account and the spawn succeeds

#### Scenario: Unknown reading counts as available

- **WHEN** `default` has no usage reading yet
- **THEN** a new coordinator runs on `default`

#### Scenario: Same login under both accounts

- **WHEN** `default` and `personal` are signed into the same login
- **THEN** a new coordinator runs on `default`

#### Scenario: Sub-coordinator materialization uses the same selection

- **WHEN** the gater materializes a `kind=subcoord` plan node with no explicit account while `default` is over the coordinator limit
- **THEN** the sub-coordinator runs on `personal`, via the same selection a root coordinator spawn uses

#### Scenario: Explicit account wins

- **WHEN** a coordinator is spawned with `account: "personal"` while `default` is under the limit
- **THEN** the coordinator runs on `personal`

#### Scenario: Unset list leaves behavior unchanged

- **WHEN** `coordinator_accounts` is not configured
- **THEN** coordinator account resolution is exactly as before this change

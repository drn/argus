## ADDED Requirements

### Requirement: A tier may name an account and resolution returns backend plus account

Each `[[backend_routing.tier]]` entry SHALL accept an optional `account` naming the account the tier's tasks run under; an omitted `account` SHALL mean the `default` account. The tier resolver SHALL return both the chosen tier's backend and its account, under both the `ordered` and `headroom` strategies, so that two tiers sharing a backend (e.g. `claude` on two accounts) are distinct candidates. A tier naming an account that is not configured, or an account that cannot run the tier's backend (`Config.AccountSupports`), SHALL be skipped and logged, and resolution SHALL continue with the remaining tiers, never erroring the caller. Diligence profiles SHALL NOT carry an account: a task's model continues to resolve from its archetype by backend family, independent of which account routing chose.

#### Scenario: Most headroom across accounts and Codex wins

- **WHEN** strategy is `headroom` and the tiers are `claude` on `default` (90% used, threshold 70), `claude` on `personal` (36% used, threshold 90) and `codex` (25% used, threshold 80), each signed into a distinct login
- **THEN** the `default` Claude tier is excluded (over threshold), and the resolver returns backend `codex` with account `default`, because Codex's headroom (55) exceeds personal's (54)

#### Scenario: Two Claude accounts are separate candidates

- **WHEN** strategy is `headroom`, `claude` on `default` has headroom 10 and `claude` on `personal` has headroom 40, and Codex is over its threshold
- **THEN** the resolver returns backend `claude` with account `personal`

#### Scenario: Omitted account means default

- **WHEN** a tier has `backend = "claude"` and no `account`, and it is selected
- **THEN** the resolver returns account `default`

#### Scenario: Unknown account is skipped

- **WHEN** a tier names `account = "nope"`, which is not configured
- **THEN** that tier is skipped, the skip is logged, and resolution continues with the remaining tiers

#### Scenario: Account that cannot run the backend is skipped

- **WHEN** a `codex` tier names an account that defines only `claude_config_dir`
- **THEN** that tier is skipped and logged, and resolution continues

#### Scenario: Archetype model is unaffected by the routed account

- **WHEN** a `code_slice` worker is routed to `claude` on `personal`
- **THEN** its model is the profile's `code_slice` Claude model, the same as it would be on `default`

### Requirement: Routing candidates are keyed by signed-in identity

Before each usage-probe tick, and at daemon startup, the system SHALL resolve each Claude tier's account folder to its signed-in identity (email) using the existing sign-in lookup, reading the identity only and never credentials. Tiers whose folders report the same identity SHALL collapse into a single candidate with a single usage reading; the collapsed candidate's account SHALL be `default` when `default` is among the collapsed tiers, otherwise the first-listed of them, and its threshold SHALL be that tier's threshold. A Claude tier whose folder reports no sign-in SHALL be skipped. A Claude tier whose sign-in lookup errors SHALL be treated as having an unknown reading (fail open), not skipped. Because identity is re-resolved every tick, re-signing a folder into a different login SHALL change routing within one probe interval with no configuration change.

#### Scenario: Same login under two accounts collapses to one candidate

- **WHEN** `~/.claude` (`default`) and the `personal` account folder are both signed into the same email
- **THEN** routing has one Claude candidate for that email, using account `default`, with one usage reading

#### Scenario: Work burned out and the machine is switched to personal

- **WHEN** Aaron re-signs `~/.claude` from his work login into his personal login while tiers exist for `default` and `personal`
- **THEN** within one probe interval the two Claude tiers collapse into one candidate on account `default`, and routing balances that candidate against Codex only

#### Scenario: Switching back restores the third candidate

- **WHEN** `~/.claude` is later re-signed into the work login
- **THEN** within one probe interval routing again has distinct work, personal and Codex candidates

#### Scenario: Not-signed-in account folder is skipped

- **WHEN** the `personal` account folder has never been logged in
- **THEN** the `personal` tier is skipped and never selected

#### Scenario: Sign-in lookup failure fails open

- **WHEN** the sign-in lookup for a Claude tier's folder returns an error
- **THEN** that tier is kept as a candidate with an unknown reading

### Requirement: Routing decisions are logged to the daemon log

Every tier resolution SHALL emit one structured log line to the daemon's own log (not the TUI-only debug logger) naming each candidate's backend, account, identity, reading (or unknown), threshold and headroom, the selected candidate, and the reason (`headroom`, `list-order`, `all-over-threshold`, or `skipped`).

#### Scenario: A resolution is visible in daemon.log

- **WHEN** a worker is created with no explicit backend and the tier list is consulted
- **THEN** `daemon.log` contains a line listing every candidate's reading and the chosen backend and account

### Requirement: No agent is created on an account at or above the routing ceiling

`[backend_routing]` SHALL accept `ceiling_pct`, defaulting to 98 when unset. No agent spawn — worker, freelancer, coordinator or sub-coordinator, whether routed by the tier list or resolved from a project backend — SHALL be placed on a backend and account whose cached weekly used percentage is at or above the ceiling; a held depleted reading always counts as at the ceiling. Under the `headroom` strategy, candidates at or above the ceiling SHALL be excluded before headroom is compared, so when every tier is over its own threshold the resolver picks the highest-headroom candidate that is still below the ceiling. Under the `ordered` strategy, a tier at or above the ceiling SHALL be skipped even when it would otherwise be the fail-open choice. An unknown or stale reading SHALL count as below the ceiling. When no candidate is below the ceiling, the spawn SHALL be refused before any worktree, branch or task row is created, with an error naming each candidate and its reading. A backend or account given explicitly on the spawn SHALL bypass the ceiling, as an operator override, and SHALL log a warning when the chosen reading is at or above it.

#### Scenario: Worker skips an account at the ceiling

- **WHEN** strategy is `headroom`, every tier is over its own threshold, `claude` on `default` is at 99%, and `claude` on `personal` is at 95% with threshold 90
- **THEN** a routed worker runs on `personal`, never on `default`

#### Scenario: Every candidate at the ceiling refuses a worker

- **WHEN** every tier's reading is at or above 98% and a worker is spawned with no explicit backend or account
- **THEN** the spawn fails with an error naming each candidate and its reading, and no worktree, branch or task row is created

#### Scenario: Project backend at the ceiling refuses the spawn

- **WHEN** a project's backend is `codex`, Codex is at 99%, and a worker is spawned in that project with no explicit backend
- **THEN** the spawn is refused with the Codex reading named in the error

#### Scenario: Unknown reading is below the ceiling

- **WHEN** the only candidate tier has no usage reading yet
- **THEN** the spawn proceeds on that tier

#### Scenario: Explicit choice bypasses the ceiling with a warning

- **WHEN** a worker is spawned with `account: "default"` and backend `claude` while `default` is at 99%
- **THEN** the worker runs on `default` and a warning naming the reading and the ceiling is logged

#### Scenario: Ceiling is configurable

- **WHEN** `ceiling_pct = 100` and the only candidate is at 99%
- **THEN** the spawn proceeds on that candidate

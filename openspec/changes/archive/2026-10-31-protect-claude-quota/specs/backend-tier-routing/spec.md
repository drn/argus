## ADDED Requirements

### Requirement: Tiers select a backend and account together

Each tier SHALL support an optional account in config.toml and backend_tiers storage. The resolver SHALL return backend and account together, using the tier's account for its usage measurement unless an explicit or project account overrides it. Omitted tier accounts SHALL retain existing default-account resolution. Ordered/headroom strategies, threshold semantics, tie order, unknown-reading behavior, and exhausted-tier fallback SHALL remain unchanged. A tier account failing ValidateAccount or AccountSupports SHALL be skipped with a warning emitted through both uxlog and slog; routing SHALL never borrow a different account's Claude usage.

#### Scenario: Personal tier has more headroom

- **WHEN** headroom routing sees work Claude at 96% and personal Claude at 0%, both with threshold 90
- **THEN** it returns the personal Claude backend/account pair

#### Scenario: Invalid account is skipped

- **WHEN** a tier names an unknown, invalid, or backend-incompatible account
- **THEN** it is skipped with both warnings, and remaining tiers are considered

#### Scenario: Named account has no measurements

- **WHEN** personal has no fresh bridge usage
- **THEN** it is unknown under the existing fail-open strategy and never scored using work's reading

#### Scenario: Explicit backend still permits account selection

- **WHEN** the caller pins a backend but supplies no account and the project has no account pin
- **THEN** routing selects among that backend's valid tiers using the configured strategy and returns its account without changing the backend

#### Scenario: Named Codex identity lacks an account-scoped probe

- **WHEN** a Codex tier selects a named account
- **THEN** its reading is unknown rather than borrowed from the default account; account-scoped Codex rollout reading remains the named follow-up named-codex-usage-probes

## MODIFIED Requirements

### Requirement: Hera coordinator and sub-coordinator spawn are excluded from tier-list routing

Coordinator and sub-coordinator routing SHALL restrict automatic selection to Claude-capable backend tiers, including their accounts. Non-Claude tiers SHALL never determine an automatic coordinator backend. If no eligible tier is available, the system SHALL preserve the existing Claude-capable project/default resolution and literal claude fallback. Explicit backend overrides SHALL remain honored with the existing warning for non-Claude choices.

#### Scenario: Idle personal Claude account protects coordinator quota

- **WHEN** headroom routing sees an exhausted work Claude tier, an available personal Claude tier, and a Codex tier
- **THEN** an unpinned coordinator uses personal Claude, regardless of Codex headroom

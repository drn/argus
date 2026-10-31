## ADDED Requirements

### Requirement: Tier account is viewable and editable

TUI Settings SHALL display a tier's optional account and allow selecting compatible configured accounts or clearing the selection. The account SHALL persist with its tier across edits and reorder operations. Config.toml-owned tiers SHALL remain read-only. Web/macOS tier-editing parity SHALL remain the named follow-up web-macos-routing-settings-parity.

#### Scenario: Edit personal overflow tier

- **WHEN** a DB-owned Claude tier is assigned account personal
- **THEN** its persisted and displayed account is personal

#### Scenario: Config-owned tiers

- **WHEN** tiers come from config.toml
- **THEN** the account is displayed but cannot be edited in Settings

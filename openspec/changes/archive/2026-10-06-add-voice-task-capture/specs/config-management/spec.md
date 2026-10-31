## ADDED Requirements

### Requirement: Voice default project

The configuration SHALL provide `[defaults] voice_project` (also DB key `defaults.voice_project`), the project used for voice-captured tasks when neither the transcript nor the classifier names one. It SHALL default to empty.

#### Scenario: Configured default

- **WHEN** `config.toml` sets `[defaults] voice_project = "forge"`
- **THEN** the effective config reports `Defaults.VoiceProject == "forge"`

### Requirement: Voice capture configuration

The configuration SHALL provide a config.toml-only `[voice]` table with `enabled` (default false), `hotkey` (default `right_option`), `wispr_db` (default empty, meaning Wispr Flow's standard `flow.sqlite` path), `transcript_timeout_seconds` (default 30), and `[voice.jev]` with `api_key` (a secret source; empty disables Jev), `endpoint` (default `https://api.typesafe.ai/v1/systemone`), `model` (optional), and `min_confidence` (default 0.7). It SHALL have no DB keys and no REST or settings-UI surface, and changes SHALL take effect on daemon restart.

#### Scenario: Defaults applied

- **WHEN** `config.toml` sets only `[voice] enabled = true`
- **THEN** the effective config has hotkey `right_option`, transcript timeout 30, Jev endpoint `https://api.typesafe.ai/v1/systemone`, and Jev min_confidence 0.7

#### Scenario: Key from a secret reference

- **WHEN** `[voice.jev] api_key = "op://Private/TypeSafe/credential"`
- **THEN** the key is resolved through the secrets resolver at use and never stored in the DB

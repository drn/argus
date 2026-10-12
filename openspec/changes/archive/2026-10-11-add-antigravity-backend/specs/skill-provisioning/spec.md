## ADDED Requirements

### Requirement: Antigravity builtin skills via global manifest

Argus SHALL ensure agy's global skills manifest `~/.gemini/config/skills.json` contains an `entries` item whose `path` is Argus's materialized builtin skills directory, adding it idempotently and preserving every other entry and key. An unparseable manifest SHALL be left untouched with an error.

#### Scenario: Entry added once
- **WHEN** injection runs twice against a manifest without the Argus entry
- **THEN** exactly one entry with the Argus skills path exists and other entries are preserved

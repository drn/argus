## ADDED Requirements

### Requirement: Cursor sandbox grant

Sandboxed cursor launches SHALL receive a write grant for `~/.cursor`, emitted only into cursor launches' profiles.

#### Scenario: Grant present for cursor only
- **WHEN** a sandbox profile is generated for a cursor launch
- **THEN** it allows writes under `$HOME/.cursor`, and profiles for other backends do not

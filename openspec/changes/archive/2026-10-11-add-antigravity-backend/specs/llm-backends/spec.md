## ADDED Requirements

### Requirement: Backend kind classification

The system SHALL expose one classifier mapping a backend command to its family kind by first-word basename, shared by configuration (account support), the agent layer, and the REST API, so no component re-implements backend detection.

#### Scenario: Account support uses the shared classifier
- **WHEN** a named account with only `claude_config_dir` is checked against an antigravity backend
- **THEN** it is reported unsupported

## ADDED Requirements

### Requirement: Built-in cursor backend

The default configuration SHALL include a `cursor` backend with command `cursor-agent --approve-mcps` and no prompt flag; existing databases receive it on next start.

#### Scenario: Default backend present
- **WHEN** the default config is built
- **THEN** it contains a backend named `cursor` classified as the cursor family

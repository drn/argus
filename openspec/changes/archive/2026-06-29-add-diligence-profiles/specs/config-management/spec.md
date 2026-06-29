# Configuration Management

## ADDED Requirements

### Requirement: Project profile binding

Project configuration SHALL support an optional `profile` field naming the diligence profile bound to
that project, storing the profile **name only** (never the profile body). When a project's `profile` is
empty or absent, the project SHALL be treated as bound to the `default` profile for resolution purposes.

#### Scenario: Project carries a profile name

- **WHEN** a project entry declares `profile = "customer_grade"`
- **THEN** the loaded project exposes `customer_grade` as its bound profile name

#### Scenario: Absent profile resolves default

- **WHEN** a project entry omits `profile`
- **THEN** the project is treated as bound to the `default` profile

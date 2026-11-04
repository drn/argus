## MODIFIED Requirements

### Requirement: Builtin skill body bundle

The embedded builtin set SHALL include `argus-handoff` alongside the existing builtins.

#### Scenario: Embedded set includes every shipped builtin

- **WHEN** the embedded builtin skill set is enumerated
- **THEN** it includes `argus-archive`, `argus-complete`, `argus-handoff`, `argus-recycle`, `argus-resolve-model`, `argus-schedule`, `hera`, `hera-plan`, `hera-review`, `hera-review-test-adversary`, and `hera-spawn-review`

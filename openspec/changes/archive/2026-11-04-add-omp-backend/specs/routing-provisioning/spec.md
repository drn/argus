## ADDED Requirements

### Requirement: Oh My Pi routing delivery

The system SHALL deliver the materialized builtin routing file to omp launches as `--append-system-prompt <path>`.

#### Scenario: Routing flag on omp launch
- **WHEN** an omp command is built and routing content materializes
- **THEN** it contains `--append-system-prompt` followed by the quoted routing file path

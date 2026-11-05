## ADDED Requirements

### Requirement: Oh My Pi sandbox grant

Sandboxed omp launches SHALL receive a write grant for `~/.omp`, emitted only into omp launches' profiles.

#### Scenario: Grant present for omp only
- **WHEN** a sandbox profile is generated for an omp launch
- **THEN** it allows writes under `$HOME/.omp`, and profiles for other backends do not

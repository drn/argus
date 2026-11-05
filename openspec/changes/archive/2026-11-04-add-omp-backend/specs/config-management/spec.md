## ADDED Requirements

### Requirement: Built-in omp backend

The default configuration SHALL include an `omp` backend with command `omp` and no prompt flag; existing databases receive it on next start.

#### Scenario: Default backend present
- **WHEN** the default config is built
- **THEN** it contains a backend named `omp` classified as the omp family

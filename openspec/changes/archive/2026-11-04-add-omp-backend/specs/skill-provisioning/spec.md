## ADDED Requirements

### Requirement: Session-scoped Oh My Pi builtin skills

The system SHALL deliver Argus's builtin skills to omp launches through `--config <overlay>`, where `<overlay>` is a content-addressed YAML file under `~/.argus/cache/omp-config/` setting `skills.customDirectories` to the materialized builtin skills directory. omp's own `config.yml` SHALL NOT be written. A materialization failure SHALL be logged and the launch SHALL proceed without the flag.

#### Scenario: Overlay delivered per launch
- **WHEN** an omp command is built and builtin skills materialize
- **THEN** it contains `--config` pointing at a file whose `skills.customDirectories` lists the builtin skills directory

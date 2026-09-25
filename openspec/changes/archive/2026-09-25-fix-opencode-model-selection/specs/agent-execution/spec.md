## ADDED Requirements

### Requirement: OpenCode model selection uses child inline configuration

The system SHALL deliver a resolved model for a backend recognized as OpenCode through the child process's `OPENCODE_CONFIG_CONTENT` `model` key rather than a top-level `--model` argument, because OpenCode v2's full interactive TUI rejects that argument. The system SHALL preserve valid inherited inline configuration, unrelated keys, and existing skill paths while merging Argus's model and skills overrides, and SHALL accept the trailing-comma JSONC superset that OpenCode's own config parser accepts. A backend command that already contains an explicit model flag SHALL take precedence and SHALL NOT receive an additional inline model override. Model resolution precedence SHALL remain unchanged: a valid per-task override, then a valid profile model, then the backend default, then no model override. Model delivery for Claude, Codex, and Pi SHALL continue to use their command-line `--model` flag. A resolved model that is not a `provider/model` identifier SHALL be logged as ignored by OpenCode rather than delivered silently. A malformed inherited inline configuration SHALL remain untouched and SHALL NOT block the OpenCode launch.

#### Scenario: Fresh OpenCode task receives a per-task model

- **WHEN** a task for an OpenCode backend has a valid per-task model and its backend command has no model flag
- **THEN** the built command contains no top-level `--model` argument
- **AND** the child environment's `OPENCODE_CONFIG_CONTENT` contains a `model` key with that value

#### Scenario: Backend default model reaches OpenCode

- **WHEN** an OpenCode task has no per-task model but its resolved backend default is non-empty
- **THEN** the child environment's `OPENCODE_CONFIG_CONTENT` contains that resolved model

#### Scenario: Profile model reaches OpenCode

- **WHEN** a profile resolves a model for an OpenCode-backed task
- **THEN** the child environment's `OPENCODE_CONFIG_CONTENT` contains the profile model
- **AND** the existing `ARGUS_PROFILE`, `ARGUS_ARCHETYPE`, and `ARGUS_MODEL` exports remain present

#### Scenario: Resumed OpenCode task keeps the model delivery contract

- **WHEN** an OpenCode task is resumed with a known session ID and a resolved model
- **THEN** the command uses `--session` and carries the model in `OPENCODE_CONFIG_CONTENT`
- **AND** it does not append a top-level `--model` argument

#### Scenario: Inline configuration is preserved

- **WHEN** the inherited `OPENCODE_CONFIG_CONTENT` is valid JSON containing unrelated settings and existing skill paths
- **THEN** the child receives the merged model and Argus skills path while every existing key and skill path remains present

#### Scenario: OpenCode-accepted JSONC is not treated as malformed

- **WHEN** the inherited `OPENCODE_CONFIG_CONTENT` uses a trailing comma that OpenCode's own JSONC parser accepts
- **THEN** Argus still merges its model and skills overrides into that document
- **AND** a comma inside a quoted value SHALL NOT be rewritten

#### Scenario: A model OpenCode cannot parse is logged, never silent

- **WHEN** the resolved OpenCode model is not a `provider/model` identifier
- **THEN** Argus logs a warning naming the task and the value
- **AND** the launch continues

#### Scenario: Explicit command model wins

- **WHEN** the configured OpenCode backend command already contains a model flag
- **THEN** Argus leaves the command's explicit model selection in place
- **AND** Argus does not add a conflicting `model` key to the child inline configuration

#### Scenario: Malformed inline configuration fails open

- **WHEN** the inherited `OPENCODE_CONFIG_CONTENT` is not a valid JSON object
- **THEN** Argus leaves the inherited value untouched, logs the configuration failure, and still builds the OpenCode launch
- **AND** the launch receives no Argus model override from that malformed value

#### Scenario: Incompatible skills value does not suppress the model

- **WHEN** inherited inline configuration is valid JSON but its `skills` value is not an array
- **THEN** Argus preserves that user-owned value, logs that Argus skills were skipped, and still delivers the resolved model
- **AND** the launch continues without replacing the incompatible value

#### Scenario: Other backends keep command-line model injection

- **WHEN** a Claude, Codex, or Pi task has a resolved model and its command has no model flag
- **THEN** the built command still contains its backend-specific `--model` argument

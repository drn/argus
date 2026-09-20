## MODIFIED Requirements

### Requirement: hera_spawn_worker creates a born-bound worker transactionally

Hera worker spawning SHALL validate an explicit model override against the worker's resolved backend before agent command construction. An unsupported override SHALL not be passed to the backend CLI and SHALL fall through to the backend default model behavior.

#### Scenario: Claude override on Codex worker

- **WHEN** `hera_spawn_worker` requests backend `codex` with model `opus`
- **THEN** the worker is not launched with `codex --model opus`

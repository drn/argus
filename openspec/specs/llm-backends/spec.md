# LLM Backends

## Purpose

Argus runs coding tasks through command-template LLM backends (Claude Code, Codex, custom CLIs). Some backends depend on a local inference daemon being warm before the agent's first request, and Argus also uses a cheap LLM call to auto-name tasks. This capability covers the local-ollama prelaunch path (ensure the daemon is up and the target model is loaded) and the non-interactive task-name generator that shells out to the local `claude` CLI. Both paths are best-effort utilities that must fail predictably so the surrounding task workflow can proceed or fall back cleanly.
## Requirements
### Requirement: Ollama daemon liveness probe

The system SHALL determine whether the local ollama daemon is reachable by issuing a single short-timeout liveness probe to its tags endpoint. The probe SHALL report "running" only when the daemon answers with an HTTP 200 within the timeout, and SHALL report "not running" for any network failure, non-200 status, or cancelled/expired context.

#### Scenario: Daemon answers 200

- **WHEN** the ollama endpoint responds 200 to the tags probe within the timeout
- **THEN** the probe reports the daemon as running

#### Scenario: Daemon unreachable

- **WHEN** the ollama endpoint cannot be connected to
- **THEN** the probe reports the daemon as not running

#### Scenario: Daemon returns an error status

- **WHEN** the ollama endpoint responds with a non-200 status (e.g. 500)
- **THEN** the probe reports the daemon as not running

#### Scenario: Probe context already cancelled

- **WHEN** the caller's context is cancelled or the probe exceeds its timeout
- **THEN** the probe reports the daemon as not running rather than raising an error

### Requirement: Starting the ollama daemon

The system SHALL bring up a downed ollama daemon by running a configured start command and then polling the liveness probe until the daemon answers or a bounded start timeout elapses. A successful return of the start command alone SHALL NOT be treated as the daemon being ready; readiness is confirmed only by a successful liveness probe. If the start command itself fails, the system SHALL return an error that includes the command and its captured output.

#### Scenario: Daemon becomes ready after start

- **WHEN** the start command succeeds and the liveness probe begins answering 200 within the start timeout
- **THEN** start succeeds with no error

#### Scenario: Start command fails

- **WHEN** the configured start command exits non-zero
- **THEN** start returns an error containing the command name and its captured output

#### Scenario: Daemon never becomes ready

- **WHEN** the start command succeeds but the liveness probe never returns 200 before the start timeout elapses
- **THEN** start returns a timeout error indicating the daemon was not ready

### Requirement: Preloading a model into the daemon

The system SHALL preload a named model into the running daemon by requesting a generation with a keep-alive hint, so the model is resident and the agent's first real inference is fast. When the requested model is not installed, the system SHALL return a clear error that names the model and tells the user how to install it. Any other non-success response SHALL be returned as an error including the status.

#### Scenario: Model loads successfully

- **WHEN** a preload is requested for an installed model and the daemon responds 200
- **THEN** preload succeeds and the request carries a keep-alive hint so the model stays resident

#### Scenario: Model not installed

- **WHEN** the daemon responds 404 because the model is not installed
- **THEN** preload returns an error naming the model and instructing the user to pull it

#### Scenario: Daemon error during preload

- **WHEN** the daemon responds with a server error status
- **THEN** preload returns an error including the HTTP status

### Requirement: Ensuring a backend's model is warm

The system SHALL provide a single entry point that probes the daemon, starts it only if it is down, and then preloads the requested model. This orchestration SHALL be serialized so that concurrent task launches requiring the same daemon do not each shell out to start it; later callers SHALL observe the daemon already up and complete via a fast preload. If starting the daemon fails, the orchestration SHALL return an error indicating the start failure.

#### Scenario: Daemon already running

- **WHEN** the ensure entry point runs while the daemon is already up
- **THEN** the start command is not invoked and the model is preloaded

#### Scenario: Daemon down then ensured

- **WHEN** the ensure entry point runs while the daemon is down
- **THEN** the daemon is started and the model is preloaded

#### Scenario: Concurrent ensure callers

- **WHEN** multiple callers invoke the ensure entry point concurrently for a downed daemon
- **THEN** the start command runs exactly once and every caller completes without error

#### Scenario: Daemon fails to start

- **WHEN** the ensure entry point runs while the daemon is down and the start command fails
- **THEN** the ensure entry point returns an error indicating the daemon failed to start

### Requirement: Generating a task name from a description

The system SHALL generate a short kebab-case task name by summarizing a task description through the local `claude` CLI pinned to a fast, low-cost model with all context sources disabled. The generated name SHALL consist of lowercase alphanumeric segments joined by single hyphens with no leading or trailing hyphen, and SHALL NOT exceed the maximum name length.

#### Scenario: Valid name produced

- **WHEN** a non-empty task description is summarized and the model returns a valid kebab-case candidate
- **THEN** the system returns that name

#### Scenario: Description framed as data not a question

- **WHEN** a task description is sent for naming
- **THEN** it is conveyed as a task description to summarize, with framing instructing the model not to answer or engage with its content

### Requirement: Name generation skip and failure semantics

The system SHALL fail open so that callers can keep their fallback name. It SHALL return a distinct "empty prompt" signal when the description is empty or whitespace, a distinct "unavailable" signal when the `claude` CLI is not installed, and a generic error when the CLI ran but produced output that is not a valid name. A name candidate that is not valid kebab-case SHALL be rejected rather than returned.

#### Scenario: Empty description

- **WHEN** the description is empty or only whitespace
- **THEN** the system returns the empty-prompt signal without invoking the CLI

#### Scenario: CLI not installed

- **WHEN** the `claude` CLI is not found on PATH
- **THEN** the system returns the unavailable signal so the caller treats it as a clean skip

#### Scenario: Model returns unusable output

- **WHEN** the CLI runs but returns prose or other output that is not valid kebab-case
- **THEN** the system returns a generic error and no name

### Requirement: Sanitizing model output

The system SHALL sanitize a raw model response before validating it: trim surrounding whitespace, strip wrapping quotes, single quotes, and backtick fences, lowercase the result, and drop trailing sentence punctuation. After sanitizing, a candidate SHALL be accepted only if it is non-empty, within the maximum length, and matches strict kebab-case; otherwise it SHALL be rejected.

#### Scenario: Wrapped or decorated candidate

- **WHEN** the model returns a candidate wrapped in quotes, backticks, a code fence, or with trailing punctuation or mixed case
- **THEN** the wrappers and punctuation are stripped and the lowercased kebab-case core is accepted

#### Scenario: Non-kebab candidate rejected

- **WHEN** the sanitized candidate contains underscores, spaces, slashes, a leading or trailing hyphen, doubled hyphens, or exceeds the maximum length
- **THEN** the candidate is rejected and no name is returned

### Requirement: Backend credential environment mapping definition

A backend definition SHALL be able to carry an optional credential environment
mapping: a set of entries mapping a target environment-variable name to a
source descriptor. The mapping SHALL hold only descriptors and SHALL NOT store
a secret value. The mapping SHALL be persisted with the backend definition and
read back with it. The default `codex` backend SHALL be seeded with the mapping
`OPENAI_API_KEY -> HERA_OPENAI` so a Codex (OpenAI) agent can receive its key
under the expected variable name. No `gemini` backend SHALL be added by this
change.

#### Scenario: Codex default carries the OpenAI mapping

- **WHEN** the default backend set is seeded into a fresh database
- **THEN** the `codex` backend carries a credential mapping
  `OPENAI_API_KEY -> HERA_OPENAI` and no secret value is stored

#### Scenario: Existing database picks up the codex mapping

- **WHEN** a database that predates this change is opened and the existing
  `codex` row has no credential mapping
- **THEN** the `codex` row is updated to carry `OPENAI_API_KEY -> HERA_OPENAI`
  without overwriting a mapping a user has already customized

#### Scenario: Mapping round-trips without a value

- **WHEN** a backend with a credential mapping is written and read back
- **THEN** the mapping is preserved as target-to-source descriptors with no
  secret value


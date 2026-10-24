## ADDED Requirements

### Requirement: Explicit sandbox launch signal

Argus SHALL export `ARGUS_SANDBOXED=1` into an agent's environment if and only if that launch command was successfully wrapped in `sandbox-exec`. It SHALL never export another value. It SHALL remove inherited and configured values of this reserved variable before applying the actual launch result. This contract SHALL apply to every backend and to fresh, resumed, and recycled sessions, whether launched in process or through the session supervisor.

#### Scenario: Wrapped agent launch

- **WHEN** an agent command is successfully wrapped in `sandbox-exec`
- **THEN** its environment contains exactly one `ARGUS_SANDBOXED=1` entry

#### Scenario: Unwrapped launch despite inherited or configured signal

- **WHEN** an agent command is not wrapped, including disabled sandboxing, unavailable sandbox support, or profile-generation failure
- **THEN** `ARGUS_SANDBOXED` is absent even if the parent environment, backend environment mapping, or cache mapping supplied it

#### Scenario: Resume and recycle recompute the signal

- **WHEN** a session is resumed or recycled
- **THEN** the new command's signal reflects that new launch's successful wrapping, independent of the previous session's signal and persisted sandbox status

#### Scenario: Supervisor compatibility

- **WHEN** a daemon checks a supervisor built before this launch contract
- **THEN** the updated spawn-surface metadata identifies its spawn behavior as stale without changing the live stream surface

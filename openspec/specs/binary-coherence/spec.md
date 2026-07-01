# binary-coherence Specification

## Purpose
TBD - created by archiving change detect-binary-skew. Update Purpose after archive.
## Requirements
### Requirement: Cross-process binary identity reporting

The daemon SHALL report its own boot-time binary identity AND, when a session-supervisor is connected, the supervisor's binary identity to the TUI in a single `BootInfo` response. Identity comprises the resolved binary path, the SHA-256 content hash, and the VCS revision plus dirty flag read from the binary's build info. The supervisor's `Hello` handshake SHALL carry its content hash and VCS identity, and the R/S `ProtocolVersion` SHALL be bumped additively so a newer daemon can feature-detect an older supervisor.

#### Scenario: Supervisor identity relayed

- **WHEN** the TUI requests `BootInfo` from a daemon that is driving a connected supervisor
- **THEN** the response SHALL include the supervisor's resolved path, content hash, and VCS identity

#### Scenario: Older supervisor reports unknown, never stale

- **WHEN** the connected supervisor speaks a protocol version that predates the binary-hash field
- **THEN** the daemon SHALL report the supervisor identity as unknown and SHALL NOT report the supervisor as stale

#### Scenario: No supervisor present

- **WHEN** the daemon runs the in-process runner with no supervisor connected
- **THEN** the `BootInfo` response SHALL indicate that no supervisor is present

### Requirement: Binary identity display and staleness signal

The system SHALL display a binary's identity as its commit SHA and a dirty flag when VCS build info is present, and SHALL fall back to the short content hash when VCS build info is absent. The stale-versus-current decision SHALL be based on the SHA-256 content hash, never on VCS information.

#### Scenario: Rich identity shown when VCS info present

- **WHEN** a binary was built from inside a git tree and carries VCS build info
- **THEN** its identity is displayed as the commit SHA plus a dirty/clean flag plus its resolved path

#### Scenario: Hash fallback when VCS info absent

- **WHEN** a binary lacks VCS build info
- **THEN** its identity is displayed using the short content hash

#### Scenario: Decision is hash-based

- **WHEN** two binaries have differing VCS info but identical content hashes
- **THEN** they SHALL be judged current (not stale)

### Requirement: Binary-coherence diagnostic command

The CLI SHALL provide an `argus doctor` command that runs read-only, enumerates the argus binaries resolvable on disk (the `PATH` entry, the `~/.argus/argusd` symlink target, and the `go install` target) and the identity each live process is running, and prints a verdict with an exact remediation command. The verdict SHALL distinguish a coherent install, a restart-needed install (same resolved path, differing hash), and a path-divergence install (the daemon symlink target and the `PATH` `argus` resolve to different files). A row that cannot be resolved SHALL degrade to "unknown" rather than aborting the command.

#### Scenario: Healthy install

- **WHEN** the TUI, daemon, and supervisor resolve to the same binary file with matching hashes
- **THEN** `argus doctor` SHALL print a healthy verdict and exit zero

#### Scenario: Non-healthy verdict exits non-zero

- **WHEN** `argus doctor` renders any non-healthy verdict (restart-needed or path-divergence)
- **THEN** it SHALL exit with a non-zero status after printing the table and remediation

#### Scenario: Restart needed

- **WHEN** a process is running a different hash from a binary at the same resolved path
- **THEN** `argus doctor` SHALL report "restart needed" and print the restart command

#### Scenario: Path divergence

- **WHEN** the daemon symlink target and the `PATH` `argus` resolve to different files
- **THEN** `argus doctor` SHALL report "path divergence" and print the re-point/reinstall fix rather than a plain restart

#### Scenario: Read-only

- **WHEN** `argus doctor` runs
- **THEN** it SHALL NOT modify any symlink, binary, `PATH`, or process

#### Scenario: Unresolvable row degrades

- **WHEN** one binary location cannot be resolved or hashed
- **THEN** that row SHALL be reported as "unknown" and the command SHALL still complete

### Requirement: Startup binary-skew detection and prompt

At startup the TUI SHALL detect binary skew against the daemon and the supervisor and present a blocking modal when skew is found. Daemon-staleness detection SHALL be performed only when the TUI connected to a pre-existing daemon (a daemon the TUI itself just auto-started cannot be stale). Supervisor-staleness detection SHALL be performed whenever a supervisor is present, regardless of how the daemon was started. The modal SHALL display the rich identity of whichever process is stale and offer the relevant restart action. Restarting the supervisor SHALL require a second explicit confirmation that names the number of running agents the restart will interrupt; declining the second confirmation SHALL leave the supervisor running.

#### Scenario: Supervisor checked on the auto-start path

- **WHEN** the TUI auto-started the daemon and the connected supervisor is stale
- **THEN** the skew modal SHALL be presented for the supervisor

#### Scenario: Daemon check gated on pre-existing connection

- **WHEN** the TUI auto-started the daemon
- **THEN** daemon-staleness SHALL NOT be evaluated for that daemon

#### Scenario: Rich identity in the modal

- **WHEN** the skew modal is presented
- **THEN** it SHALL display the commit SHA, dirty flag, and path (or hash fallback) of the stale process

#### Scenario: Supervisor restart double-confirm

- **WHEN** the user selects "restart supervisor" in the skew modal
- **THEN** a second confirmation SHALL be shown naming the count of agents that will be interrupted, and the supervisor SHALL be restarted only on an explicit second yes

#### Scenario: Declining the supervisor restart

- **WHEN** the user declines the second supervisor-restart confirmation
- **THEN** the supervisor SHALL be left running and no agents interrupted


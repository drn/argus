## ADDED Requirements

### Requirement: Supervisor coherence is judged on an executed-surface version, not the whole-binary hash

Supervisor staleness SHALL be judged on a declared surface version describing the observable
behavior of the code the supervisor actually executes, reported alongside the binary hash over
the existing handshake and boot-info surfaces. When the running supervisor's surface version
matches the current build's, the supervisor SHALL be reported coherent even though the two
binary hashes differ — a build that changes nothing the supervisor runs is not skew. The
whole-binary hash SHALL continue to be reported and displayed, and SHALL be used as the
fallback signal only when the supervisor reports no surface version at all (an older
supervisor), which SHALL be treated as unknown rather than stale, as it is today.

The surface version SHALL be reported as two independently-compared components, so a mismatch
can be attributed to its consequence: a **spawn** component covering the code read only when a
session starts, and a **stream** component covering the code that serves live sessions. A
spawn-component mismatch SHALL be reported as affecting only sessions started from that point
on; a stream-component mismatch SHALL be reported as affecting live sessions.

A change to any source path declared as supervisor-resident SHALL fail continuous integration
unless the corresponding surface component is also changed, so that omission is caught
mechanically rather than relying on the author remembering.

Nothing in this requirement SHALL cause a live supervisor to be restarted automatically.

#### Scenario: A build that does not change the supervisor's surface is coherent

- **GIVEN** a running supervisor whose binary hash differs from the current build's
- **WHEN** its reported surface version matches the current build's
- **THEN** the supervisor is reported coherent and no restart is suggested

#### Scenario: A supervisor that reports no surface version is unknown, never stale

- **WHEN** the running supervisor reports no surface version
- **THEN** it is reported present-but-unknown and is never treated as stale on that basis alone

#### Scenario: A spawn-only mismatch is reported as affecting new sessions only

- **WHEN** only the spawn component of the surface version differs
- **THEN** the verdict states that already-running agents are unaffected and only newly started
  sessions will use the previous build's spawn configuration

#### Scenario: A stream mismatch is reported as affecting live sessions

- **WHEN** the stream component of the surface version differs
- **THEN** the verdict states that live sessions are affected

#### Scenario: Touching supervisor-resident code without bumping the surface version fails CI

- **WHEN** a change modifies a source path declared as supervisor-resident and leaves both
  surface components unchanged
- **THEN** continuous integration fails

### Requirement: Binary skew is re-evaluated while the TUI is running

Binary skew SHALL be re-evaluated periodically while the TUI is running and on reconnection to
the daemon, not only during startup, so that a build installed after launch is discovered
without requiring a relaunch. A skew discovered after startup SHALL be surfaced through the
transient status-bar notice rather than a blocking modal, so it never interrupts work in
flight; the blocking startup modal SHALL remain the surface for skew present at launch.

#### Scenario: Skew introduced after launch is discovered

- **GIVEN** a TUI that was coherent at startup
- **WHEN** a new build is installed and the supervisor's surface version no longer matches
- **THEN** the skew is discovered without relaunching the TUI

#### Scenario: Post-startup skew does not present a blocking modal

- **WHEN** skew is discovered after startup
- **THEN** it is surfaced as a status-bar notice and no blocking modal is presented

## MODIFIED Requirements

### Requirement: Binary identity display and staleness signal

The system SHALL display a binary's identity as its commit SHA and a dirty flag when VCS build
info is present, and SHALL fall back to the short content hash when VCS build info is absent. The
stale-versus-current decision SHALL be based on the SHA-256 content hash for the daemon, and on the
declared executed-surface version for the session-supervisor; it SHALL NEVER be based on VCS
information for any process. Where the supervisor's surface version is judged, its content hash
SHALL still be reported and displayed.

#### Scenario: Rich identity shown when VCS info present

- **WHEN** a binary was built from inside a git tree and carries VCS build info
- **THEN** its identity is displayed as the commit SHA plus a dirty/clean flag plus its resolved path

#### Scenario: Hash fallback when VCS info absent

- **WHEN** a binary lacks VCS build info
- **THEN** its identity is displayed using the short content hash

#### Scenario: Decision is hash-based

- **WHEN** two binaries have differing VCS info but identical content hashes
- **THEN** they SHALL be judged current (not stale)

#### Scenario: The supervisor's decision is surface-based, not hash-based

- **WHEN** the running supervisor's content hash differs from the current build's but its reported
  surface version matches
- **THEN** it SHALL be judged current (not stale), and both its content hash and its surface
  version SHALL still be displayed

### Requirement: Startup binary-skew detection and prompt

At startup the TUI SHALL detect binary skew against the daemon and the supervisor and present a
blocking modal when the detected skew affects work in flight — a stale daemon, or a supervisor
mismatch that reaches live sessions. A supervisor mismatch that can affect only sessions started
from that point on SHALL be surfaced through the transient status-bar notice instead of a blocking
modal, since it cannot affect any already-running agent. Daemon-staleness detection SHALL be
performed only when the TUI connected to a pre-existing daemon (a daemon the TUI itself just
auto-started cannot be stale). Supervisor-staleness detection SHALL be performed whenever a
supervisor is present, regardless of how the daemon was started. The modal SHALL display the rich
identity of whichever process is stale and offer the relevant restart action(s); where the
supervisor is among them, the modal SHALL also state what its mismatch costs, so an agent-
interrupting restart is never offered without saying what it buys. Restarting the supervisor SHALL
require a second explicit confirmation that names the number of running agents the restart will
interrupt; declining the second confirmation SHALL leave the supervisor running. The modal SHALL
auto-dismiss only when a single restart action (or none) was offered; when both the daemon and the
supervisor are stale, resolving one restart action SHALL leave the modal open with the remaining
action(s) until the user addresses or skips it.

#### Scenario: Supervisor checked on the auto-start path

- **WHEN** the TUI auto-started the daemon and the connected supervisor is stale in a way that
  affects live sessions
- **THEN** the skew modal SHALL be presented for the supervisor

#### Scenario: A spawn-only supervisor mismatch at startup does not block

- **WHEN** the only skew present at launch is a supervisor spawn-surface mismatch
- **THEN** no blocking modal SHALL be presented, and the mismatch SHALL be surfaced through the
  status-bar notice

#### Scenario: Daemon check gated on pre-existing connection

- **WHEN** the TUI auto-started the daemon
- **THEN** daemon-staleness SHALL NOT be evaluated for that daemon

#### Scenario: Rich identity in the modal

- **WHEN** the skew modal is presented
- **THEN** it SHALL display the commit SHA, dirty flag, and path (or hash fallback) of the stale process

#### Scenario: The modal states what a supervisor restart buys

- **WHEN** the skew modal offers a supervisor restart
- **THEN** it SHALL also state the consequence of the supervisor's mismatch — whether running
  agents are affected or only newly started sessions

#### Scenario: Supervisor restart double-confirm

- **WHEN** the user selects "restart supervisor" in the skew modal
- **THEN** a second confirmation SHALL be shown naming the count of agents that will be interrupted, and the supervisor SHALL be restarted only on an explicit second yes

#### Scenario: Declining the supervisor restart

- **WHEN** the user declines the second supervisor-restart confirmation
- **THEN** the supervisor SHALL be left running and no agents interrupted

#### Scenario: Restarting the daemon while the supervisor is also stale leaves the modal open

- **WHEN** both the daemon and the supervisor are stale and the user chooses "Restart daemon"
- **THEN** the daemon restart SHALL fire and the skew modal SHALL remain open, now offering only "Restart supervisor" and "Skip"

#### Scenario: Restarting the supervisor resolves a pending daemon restart too

- **WHEN** both the daemon and the supervisor are stale and the user chooses "Restart supervisor"
- **THEN** the skew modal SHALL dismiss in one step (the supervisor restart also bounces the daemon), proceeding straight to the supervisor's double-confirm

#### Scenario: Single restart action still auto-dismisses

- **WHEN** only the daemon or only the supervisor is stale and the user chooses the offered restart action
- **THEN** the skew modal SHALL dismiss immediately, unchanged from prior behavior

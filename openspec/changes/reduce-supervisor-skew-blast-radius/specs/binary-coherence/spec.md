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

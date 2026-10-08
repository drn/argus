## ADDED Requirements

### Requirement: Session-tagged descendant reaping on exit

Every session the runner spawns SHALL carry a unique per-spawn tag in its process environment, inherited by all of its descendants. When a session's process exits for any reason (explicit stop, natural exit, kick/rerender restart, recycle), the runner SHALL, in the background and without delaying its exit handling, wait a short grace period. It SHALL then send SIGTERM to every live process whose environment carries that exact tag, and send SIGKILL to any such process still alive after a second short period. This SHALL reach descendants that left the session's process group or session (e.g. Playwright browsers launched detached) or were reparented to the system init process.

The reaper SHALL never signal its own process. A replacement session spawned for the same task SHALL carry a different tag and SHALL NOT be signalled by the previous session's reaper. Daemon and supervisor auto-start SHALL NOT propagate the tag to the processes they start. Every enumeration or signalling failure SHALL be logged and swallowed. On platforms without a process-environment enumerator, the reaper SHALL do nothing.

#### Scenario: Detached browser left behind is reaped
- **WHEN** a session exits while a descendant that called setsid (e.g. a Playwright-launched browser) is still running
- **THEN** that descendant is sent SIGTERM after the grace period, and SIGKILL if it is still alive afterwards

#### Scenario: Process orphaned mid-session is reaped
- **WHEN** a session's descendant was reparented to the init process before the session exited
- **THEN** it is still found by its tag and reaped when the session exits

#### Scenario: Kick-restart replacement is untouched
- **WHEN** a session exits and the runner immediately restarts the same task
- **THEN** the old session's reaper does not signal the replacement session or its descendants

#### Scenario: Nothing tagged survives
- **WHEN** a session exits and no process carries its tag after the grace period
- **THEN** no signal is sent and the outcome is logged

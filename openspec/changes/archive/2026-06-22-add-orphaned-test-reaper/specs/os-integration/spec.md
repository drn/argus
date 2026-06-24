# OS Integration

## ADDED Requirements

### Requirement: Orphaned-test reaper

The repo SHALL ship a maintenance script (`script/reap-orphaned-tests.sh`) that
kills orphaned Go `*.test` binaries, and an installer
(`script/install-reaper.sh`) that registers the script as a periodic macOS
LaunchAgent. This is a second, maintenance-only LaunchAgent distinct from the
daemon auto-start agent; it is installed from a repo script (not from argus Go
code) and exists to backstop the class of failure where a leaked test binary is
reparented to PID 1 and spins a CPU indefinitely because its `-test.timeout`
watchdog never fired (the runtime-monotonic clock is suspended while the machine
sleeps).

The reaper SHALL only kill a process when ALL of the following hold, so that a
live `go test` run is never affected (its test binaries are children of
`go test`, never of PID 1):

- the process's parent PID is 1 (orphaned), AND
- the process command line names a `*.test` binary AND carries a `-test.` flag
  (the Go test-binary signature), AND
- the process has been alive longer than a configurable threshold
  (`REAP_MIN_AGE_MINUTES`, default 10), which clears the brief reparent window
  during a normal `go test` shutdown.

The reaper SHALL send `SIGTERM` first and escalate to `SIGKILL` only if the
process is still alive after a short grace period. It SHALL support `--dry-run`
(report candidates, kill nothing) and SHALL log every kill, escalation, and
no-op run to a log file (`REAP_LOG_FILE`, default
`~/.argus/logs/reap-orphaned-tests.log`).

#### Scenario: Orphaned spinning test binary is reaped

- **WHEN** a `*.test` process whose command line includes a `-test.` flag is
  parented to PID 1 and has been alive longer than the age threshold
- **THEN** the reaper SHALL terminate it (SIGTERM, then SIGKILL if it survives)
  and log the kill

#### Scenario: A live test run is never touched

- **WHEN** a `*.test` process is a child of a running `go test` (parent PID is
  not 1)
- **THEN** the reaper SHALL NOT consider it a candidate, regardless of age

#### Scenario: Non-test orphans and young orphans are ignored

- **WHEN** an orphaned process (PID 1 parent) either does not match the Go
  test-binary signature, or is younger than the age threshold
- **THEN** the reaper SHALL NOT kill it

#### Scenario: Dry-run kills nothing

- **WHEN** the reaper is run with `--dry-run`
- **THEN** it SHALL report each candidate and kill no process

### Requirement: Reaper LaunchAgent installation

On macOS, the installer SHALL copy the reaper to a stable location outside the
repo (so the LaunchAgent survives the repo/worktree moving or being deleted),
render a LaunchAgent plist that runs the reaper on a configurable interval
(`REAP_INTERVAL_SECONDS`, default 300), and bootstrap it into the user's launchd
domain — booting out any previously-loaded job first so re-running picks up an
updated script. The installer SHALL also support uninstalling the LaunchAgent.
On non-macOS platforms the installer SHALL degrade to a no-op with an
explanatory message rather than attempting launchd operations.

#### Scenario: Install bootstraps the LaunchAgent on macOS

- **WHEN** the installer is run with `install` on macOS
- **THEN** it SHALL write the reaper to the stable location, write the plist
  with the configured interval, and bootstrap the job into the user's launchd
  domain (booting out any prior instance first)

#### Scenario: Uninstall removes the LaunchAgent

- **WHEN** the installer is run with `uninstall`
- **THEN** it SHALL boot out the job (if loaded) and remove the plist

#### Scenario: Installer is a no-op off macOS

- **WHEN** the installer is run on a non-darwin platform
- **THEN** it SHALL print an explanatory message and perform no launchd
  operations

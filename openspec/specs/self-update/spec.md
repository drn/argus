# Self-Update

## Purpose

Self-update lets the operator replace the running Argus binary in place with a freshly built one, sourced from a configured local Argus source clone. The capability rebuilds the binary by syncing the clone to the latest published source and running `go install`, then triggers a daemon respawn so the new binary takes over. It is an operator-only control surface, gated behind the master credential.

## Requirements

### Requirement: Configured source path

The capability SHALL operate against a source directory configured as the master-only `argus.source_path` setting. The configured path SHALL be readable and writable through the control surface, and SHALL be persisted across calls.

#### Scenario: Reading the configured source path

- **WHEN** the master credential requests the configured source path
- **THEN** the currently persisted `argus.source_path` value is returned

#### Scenario: Persisting a new source path

- **WHEN** the master credential submits a new source path
- **THEN** the value is persisted as `argus.source_path` and the persisted value is returned

#### Scenario: Whitespace is trimmed

- **WHEN** a source path is submitted with leading or trailing whitespace
- **THEN** the persisted value has the surrounding whitespace removed

### Requirement: Master-only access

All self-update operations (reading the source path, setting the source path, and triggering an update) SHALL require the master credential. Device-scoped credentials SHALL be rejected.

#### Scenario: Device token cannot read the source path

- **WHEN** a request authenticated with a device token asks for the source path
- **THEN** the request is rejected with a forbidden status

#### Scenario: Device token cannot trigger an update

- **WHEN** a request authenticated with a device token triggers an update
- **THEN** the request is rejected with a forbidden status

### Requirement: Source path validation

An update run SHALL validate the configured source path before building. An empty path, a path that is not an existing directory, or a directory that is not a Go module (no `go.mod`) SHALL cause the run to fail without attempting a build.

#### Scenario: Empty source path

- **WHEN** an update is triggered with no source path configured
- **THEN** the run fails with an error indicating the source path is not set and no build is attempted

#### Scenario: Nonexistent source directory

- **WHEN** the configured source path does not point at an existing directory
- **THEN** the run fails with an error indicating the path is not a directory

#### Scenario: Source path is not a Go module

- **WHEN** the configured source directory contains no `go.mod`
- **THEN** the run fails with an error indicating the path is not a Go module

### Requirement: Sync source clone to origin/master

When the source directory is a git clone (contains a `.git` directory), an update run SHALL fetch `origin master` and hard-reset the working tree to `origin/master` before building, so that whatever is on `origin/master` is exactly what gets installed. The sync SHALL be best-effort: a fetch or reset failure SHALL be recorded in the output log but SHALL NOT abort the run, and the build SHALL proceed against whatever the clone then contains.

#### Scenario: Clone is reset to origin/master before building

- **WHEN** an update runs against a git clone whose working tree has diverged from `origin/master`
- **THEN** the working tree is hard-reset to match `origin/master` and the build runs against that content

#### Scenario: Fetch failure does not abort the run

- **WHEN** the fetch of `origin master` fails (for example, the operator is offline or the remote has no `master` branch)
- **THEN** the failure is recorded in the output log and the build proceeds against the existing clone contents

#### Scenario: Non-git source directory skips sync

- **WHEN** the source directory is a Go module without a `.git` directory
- **THEN** no fetch or reset is attempted and the build runs directly

### Requirement: Build via go install

An update run SHALL build and install the binary by running `go install ./...` from the source directory. The combined stdout and stderr of every step SHALL be accumulated into an output log that is returned to the caller regardless of success or failure. A `go install` failure SHALL fail the run.

#### Scenario: Successful build

- **WHEN** `go install ./...` succeeds against a valid source module
- **THEN** the run succeeds and the output log includes a success marker indicating the daemon should be restarted to pick up the new binary

#### Scenario: Build failure surfaces the log

- **WHEN** `go install ./...` fails (for example, the source does not compile)
- **THEN** the run fails and the returned output log includes the build step output so the operator can see the cause

### Requirement: Respawn on successful update

When an update build succeeds, the control surface SHALL report success to the caller before triggering a daemon respawn, and SHALL signal that a restart will occur. The success response SHALL be flushed to the client before the successor daemon is spawned, because the successor terminates the current daemon as it starts. On a failed update, the control surface SHALL report failure and SHALL signal that no restart will occur.

#### Scenario: Success response precedes respawn

- **WHEN** an update build succeeds
- **THEN** a success response carrying the build output and a restart-will-occur signal is flushed to the caller, and only afterward is a successor daemon spawned

#### Scenario: Failure reports no restart

- **WHEN** an update build fails
- **THEN** the caller receives a failure response carrying the build output and a signal that no restart will occur

### Requirement: Respawn refused under test binary

The successor-daemon spawn SHALL be refused when the process is running as a Go test binary, to avoid re-invoking the test binary as a daemon (which would re-run the entire test suite as a fork bomb).

#### Scenario: Spawn refused from a test binary

- **WHEN** a successor daemon spawn is attempted from a process identified as a Go test binary
- **THEN** the spawn is refused with an error and the current process is left running

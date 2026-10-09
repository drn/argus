## ADDED Requirements

### Requirement: The Claude probe reads the screen incrementally and stops on success

The system SHALL run the Claude `/usage` probe in a PTY and render its output incrementally, attempting to parse the weekly reading after each chunk of output. On the first successful parse the system SHALL store the reading and terminate the probe process without waiting for it to exit on its own. Terminating a still-running probe SHALL also kill every process in its process group, including children that ignore the termination signal. The probe SHALL be bounded by a timeout; on timeout the cache SHALL be left unchanged and the timeout logged. The parser SHALL accept the weekly percentage whether it appears on the same line as the "Current week (all models)" header or on a following line before the next section header.

#### Scenario: Reading stored before process exit

- **WHEN** the probe's rendered screen shows the weekly section while the `claude` process is still running
- **THEN** the reading is stored and the process is terminated

#### Scenario: Children ignoring termination are killed

- **WHEN** the probe is terminated while a child process it started ignores SIGTERM
- **THEN** that child is killed along with the probe process

#### Scenario: Percentage on the line after the header

- **WHEN** the rendered output has "Current week (all models)" on one line and "41% used" on the next
- **THEN** the probe records 41%

### Requirement: The Claude probe runs in a dedicated directory and answers only its own trust dialog

The system SHALL run the Claude probe with its working directory set to a dedicated, argus-owned, empty directory under the argus data directory. If the rendered screen shows Claude Code's folder-trust dialog naming that exact directory, the system SHALL select the trust option once — locating the "Yes, I trust this folder" option by its label relative to the selection cursor, never by position — and continue. If that option is not on screen, the system SHALL treat the dialog as blocking. If the screen shows any other blocking dialog, the system SHALL abort the probe, leave the cache unchanged, and log the dialog's first line.

#### Scenario: Trust dialog for the probe directory is accepted

- **WHEN** the probe's screen shows the folder-trust dialog naming the probe directory
- **THEN** the probe selects the trust option and goes on to read the usage screen

#### Scenario: Trust option is selected by its label

- **WHEN** the folder-trust dialog for the probe directory lists its options in a different order
- **THEN** the probe moves the cursor to "Yes, I trust this folder" and confirms that option

#### Scenario: Trust dialog without the trust option aborts

- **WHEN** a folder-trust dialog naming the probe directory has no "Yes, I trust this folder" option
- **THEN** the probe aborts without sending any keystroke and logs that it was blocked

#### Scenario: Trust dialog for another path is not accepted

- **WHEN** the probe's screen shows a folder-trust dialog naming a different directory
- **THEN** the probe aborts without selecting any option and logs that it was blocked

#### Scenario: Other dialog aborts the probe

- **WHEN** the probe's screen shows a blocking dialog that is not the folder-trust dialog
- **THEN** the probe aborts, the cache is unchanged, and the dialog is logged

### Requirement: Usage probes run at daemon startup

The system SHALL run one Claude probe and one Codex probe when the daemon starts, in the background, and then on the regular probe cadence.

#### Scenario: First reading does not wait a full interval

- **WHEN** the daemon starts
- **THEN** a probe is attempted immediately rather than only after the first 30-minute tick

## MODIFIED Requirements

### Requirement: The usage probe fails open on any error

The system SHALL treat any probe failure — process error, timeout, blocking dialog, or unparseable output — as "unknown," leaving the previous cached value in place (or empty, if none exists yet) rather than raising an error or blocking daemon startup. The system SHALL log every probe outcome (success with percentage and reset, or the failure reason) through a logger that reaches the daemon log, without surfacing failures as user-facing errors.

#### Scenario: Probe subprocess fails

- **WHEN** the probe's subprocess exits non-zero or times out
- **THEN** the cached value is left unchanged and the failure is logged to the daemon log, with no error propagated to any caller

#### Scenario: Probe output does not parse

- **WHEN** the probe's rendered output does not match the expected weekly-usage format
- **THEN** the cache is left unchanged and a parse-failure warning is logged to the daemon log

#### Scenario: Successful probe is logged

- **WHEN** the probe stores a reading
- **THEN** a line with the percentage and reset time appears in the daemon log

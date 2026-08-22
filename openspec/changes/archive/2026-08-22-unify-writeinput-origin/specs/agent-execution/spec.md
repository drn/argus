# Agent Execution

## MODIFIED Requirements

### Requirement: Input forwarding records activity only on success

The system SHALL forward raw input bytes to the agent's PTY and SHALL record the wall-clock time of input only when the write succeeds, so a failed write (e.g. after the PTY is closed) does not advance the last-input timestamp. The last-input time SHALL read as zero before any successful input.

Every input write SHALL carry an explicit, mandatory origin — a genuine human keystroke, or input the system itself injected (reliable-notify delivery, a coordinator bounce instruction, a live emulator's auto-answered terminal capability query). There SHALL be no default origin: the write operation's signature requires the caller to state one, so a caller cannot silently fall back to the wrong classification. A human-origin write SHALL advance both the last-input timestamp and a separate last-user-input timestamp; a system-origin write SHALL advance only the last-input timestamp, never the last-user-input timestamp, so system-injected input can never be mistaken for the user answering a prompt.

#### Scenario: Successful input advances last-input time
- **WHEN** input is successfully written to a live session
- **THEN** the last-input time becomes non-zero

#### Scenario: No input means zero last-input time
- **WHEN** no input has been written to a session
- **THEN** the last-input time reads as zero

#### Scenario: Human-origin input advances both timestamps
- **WHEN** input is successfully written with human origin
- **THEN** both the last-input time and the last-user-input time become non-zero

#### Scenario: System-origin input advances only the last-input time
- **WHEN** input is successfully written with system origin
- **THEN** the last-input time becomes non-zero and the last-user-input time remains unchanged from before the write

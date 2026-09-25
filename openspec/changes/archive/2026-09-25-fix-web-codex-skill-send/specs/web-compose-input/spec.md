## ADDED Requirements

### Requirement: Compose submissions preserve explicit skill names

The web compose bar SHALL deliver the message body to the agent PTY as a
bracketed paste, with the opening and closing paste markers in the same ordered
write as the body. It SHALL send the submit carriage return in a subsequent
write only after the paste write succeeds and the existing submit delay has
elapsed. It SHALL preserve the user's message body verbatim between the paste
markers. A failed paste write SHALL leave the submit carriage return unsent.
The web compose bar SHALL enforce the input endpoint's per-write byte limit
including the paste markers, so the framed write cannot be truncated.
It SHALL reject a message containing either bracketed-paste delimiter sequence
before writing any bytes, so message text cannot escape the paste frame.

#### Scenario: Codex skill mention and another matching skill

- **WHEN** the user sends `$pr` from the web compose bar to Codex while a
  different skill whose name begins with `p` is also installed
- **THEN** Codex receives `$pr` as the composed message and invokes the `pr`
  skill, without selecting the other skill from its interactive picker

#### Scenario: Ordinary message

- **WHEN** the user sends a message from the web compose bar
- **THEN** its PTY writes contain a complete bracketed paste with the exact
  message body, followed by a separate carriage-return write

#### Scenario: Paste write fails

- **WHEN** the paste write fails or receives a non-success response
- **THEN** the web compose bar does not send the carriage return

#### Scenario: Message reaches the input size limit

- **WHEN** the message plus paste markers would exceed the input endpoint's
  per-write byte limit
- **THEN** the web compose bar rejects the send before writing any bytes

#### Scenario: Message contains a paste delimiter

- **WHEN** the message contains a bracketed-paste start or end sequence
- **THEN** the web compose bar rejects the send before writing any bytes

#### Scenario: Claude slash skill

- **WHEN** the user sends `/review` to a Claude session from the web compose bar
- **THEN** the agent receives and submits the intended slash skill request

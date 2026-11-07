## MODIFIED Requirements

### Requirement: OSC sequence filtering

Before bytes reach the live emulator, the terminal pane SHALL strip OSC sequences (`ESC ] … terminator`) from the PTY stream, treating only BEL or 7-bit ST as terminators and explicitly NOT treating a `0x9C` byte as a terminator (working around an upstream parser bug that leaks UTF-8 window titles onto the screen). The filter SHALL be stateful so a sequence split across incremental feeds is fully removed, SHALL preserve non-OSC bytes, and SHALL stop dropping after a bounded maximum so a never-terminated OSC cannot consume the stream unboundedly.

OSC-8 hyperlink sequences (`ESC ] 8 ; params ; URI terminator`) are the single exception: the filter SHALL pass them through to the emulator rather than strip them. A passed-through hyperlink SHALL be re-emitted with every byte outside printable ASCII (`0x20`–`0x7E`) in its params and URI percent-encoded (`%XX`, uppercase hex) and with a 7-bit ST (`ESC \`) terminator regardless of the original terminator, so no byte the upstream parser could mistake for a C1 String Terminator ever reaches it inside an OSC. A hyperlink payload longer than a bounded maximum SHALL be stripped like any other OSC rather than passed through, leaving its visible text intact.

#### Scenario: OSC sequence removed

- **WHEN** a non-hyperlink OSC sequence (e.g. a window title `ESC ] 2 ; … BEL`) appears in the byte stream
- **THEN** the OSC bytes SHALL be removed and surrounding non-OSC bytes SHALL pass through unchanged

#### Scenario: OSC split across feeds

- **WHEN** an OSC sequence begins in one incremental feed and ends in a later one
- **THEN** the entire sequence SHALL still be stripped across the feed boundary (or, for an OSC-8 hyperlink, re-emitted whole once its terminator arrives)

#### Scenario: 0x9C is not a terminator

- **WHEN** a `0x9C` byte (e.g. a UTF-8 continuation byte) appears inside an OSC payload
- **THEN** it SHALL NOT end the OSC and the payload SHALL continue to be dropped until a real terminator

#### Scenario: Runaway OSC is bounded

- **WHEN** an OSC payload exceeds the maximum drop budget without a recognized terminator
- **THEN** the filter SHALL stop dropping and resume passing bytes through so it can never hang

#### Scenario: Hyperlink passed through with normalized terminator

- **WHEN** the stream contains `ESC ] 8 ; id=ub9bei ; https://example.com/p BEL` followed by link text and `ESC ] 8 ; ; BEL`
- **THEN** the filter SHALL emit `ESC ] 8 ; id=ub9bei ; https://example.com/p ESC \`, the link text, and `ESC ] 8 ; ; ESC \`

#### Scenario: Non-ASCII hyperlink bytes are percent-encoded

- **WHEN** an OSC-8 hyperlink URI contains a UTF-8 character whose encoding includes `0x9C` (e.g. `✳` = `E2 9C B3`)
- **THEN** the emitted URI SHALL contain `%E2%9C%B3` in its place and no raw byte outside printable ASCII

#### Scenario: Oversized hyperlink is stripped

- **WHEN** an OSC-8 hyperlink payload exceeds the hyperlink length bound
- **THEN** the sequence SHALL be stripped and the link text that follows SHALL still pass through

## ADDED Requirements

### Requirement: Hyperlinks reach the host terminal

The agent pane SHALL carry an OSC-8 hyperlink emitted by the agent through to the host terminal. A cell the emulator records as part of a hyperlink SHALL be painted with a tcell style carrying that hyperlink's URL, and, when the hyperlink's params include an `id=` key, that id, so the host terminal treats every cell of one link (including a link wrapped across rows) as a single clickable link. This SHALL hold for hyperlinks with and without params. Argus SHALL NOT add its own link-activation gesture; activation is the host terminal's own (e.g. Cmd+click in iTerm2).

#### Scenario: Claude Code hyperlink is clickable end to end

- **WHEN** the agent emits `ESC ] 8 ; id=ub9bei ; https://app.notion.com/p/abc BEL`, link text, and a closing `ESC ] 8 ; ; BEL`, and the bytes are fed through the pane's OSC filter into its emulator
- **THEN** each link-text cell's painted style SHALL carry URL `https://app.notion.com/p/abc` and URL id `id=ub9bei`

#### Scenario: Hyperlink without params

- **WHEN** the agent emits `ESC ] 8 ; ; https://example.com ESC \`, link text, and a closing sequence
- **THEN** each link-text cell's painted style SHALL carry URL `https://example.com` and no URL id

#### Scenario: Text outside a hyperlink carries no URL

- **WHEN** a cell is painted that the emulator did not record as part of a hyperlink
- **THEN** its style SHALL carry no URL

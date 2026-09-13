# Terminal Rendering

## ADDED Requirements

### Requirement: OSC-8 hyperlink rendering

The terminal pane SHALL render an OSC-8 hyperlink cell as a genuinely
clickable link using the link's actual URI as the click target, independent
of any additional params segment (e.g. an `id=` grouping param) present in
the same OSC-8 sequence.

#### Scenario: Hyperlink with an id param resolves to the real URL

- **WHEN** the PTY stream contains an OSC-8 sequence of the form
  `ESC ] 8 ; id=<value> ; <url> ST` (or BEL-terminated) followed by link text
  and a closing `ESC ] 8 ; ; ST`
- **THEN** the resulting cell's link target SHALL be `<url>` and the mapped
  screen style's clickable target SHALL be `<url>`, not the `id=<value>`
  segment

#### Scenario: Hyperlink with no params still resolves correctly

- **WHEN** the PTY stream contains an OSC-8 sequence with an empty params
  segment (`ESC ] 8 ; ; <url> ST`)
- **THEN** the resulting cell's link target SHALL be `<url>`

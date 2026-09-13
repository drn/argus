# Plugin Views

## ADDED Requirements

### Requirement: OSC-8 hyperlink rendering parity

The plugin-view terminal pane SHALL render OSC 8 hyperlinks emitted by a
plugin's PTY output as real terminal hyperlinks, at parity with the default
agent terminal pane. When the VT emulator parses a cell carrying a non-empty
link URL, the pane's cell-style mapper MUST attach that URL to the
corresponding tcell cell style so the outer terminal receives the hyperlink
escape sequence as part of the normal draw cycle. A cell with no link URL
MUST NOT have a URL attached.

#### Scenario: A plugin-printed link renders as a real hyperlink

- **WHEN** a plugin's PTY output contains an OSC 8 hyperlink (e.g. a
  clickable PR reference or "view artifact" link) and the plugin view is
  drawn
- **THEN** the cell carrying that link's text is painted with a tcell style
  whose URL matches the link, so the user's real terminal renders it as a
  clickable hyperlink

#### Scenario: Plain text is unaffected

- **WHEN** a cell has no OSC 8 link associated with it
- **THEN** the mapped tcell style carries no URL

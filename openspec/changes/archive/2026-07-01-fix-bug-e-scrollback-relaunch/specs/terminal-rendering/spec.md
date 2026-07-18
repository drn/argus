# Terminal Rendering

## MODIFIED Requirements

### Requirement: Scrollback browsing

The terminal pane SHALL support scrolling up into session history and back down to the live tail. While scrolled, it SHALL keep the viewed content pinned as new output arrives (anchor-lock), and SHALL clamp the scroll offset to the available scrollback. Returning to the bottom SHALL resume live follow-tail.

The available scrollback SHALL be reconstructed from the on-disk session log, which retains the full session history across TUI restarts (the log is truncated only when a session starts, not when a viewer reattaches to a still-running session). Reconstruction SHALL load a bounded window of the log rather than eagerly replaying the entire (possibly multi-MB) log. When the user scrolls PAST the currently-loaded window and older bytes remain in the log, the pane SHALL read further back from the on-disk log to extend the loaded scrollback, reaching strictly older history on each such extension until the whole log is loaded or a bounded maximum single read is reached. The extension SHALL NOT depend on a bytes-per-visible-line estimate (which underestimates how far back a given number of lines reaches for escape-dense output), so scrollback is not stuck at the initially-loaded window after a reattach.

#### Scenario: Scroll up enters history

- **WHEN** the user scrolls up from the live tail
- **THEN** the pane SHALL display earlier content from the scrollback buffer and show a scroll indicator

#### Scenario: Anchor-lock on new output

- **WHEN** the user is scrolled up and new output arrives that grows the total line count
- **THEN** the scroll offset SHALL be increased by the growth so the viewed content stays pinned in place

#### Scenario: Scroll back to bottom resumes live

- **WHEN** the user scrolls down such that the offset reaches zero
- **THEN** the pane SHALL resume live follow-tail rendering

#### Scenario: Scroll offset clamped to available history

- **WHEN** the requested scroll offset exceeds the available scrollback
- **THEN** the offset SHALL be clamped to the maximum available scroll

#### Scenario: Scroll past the loaded window extends from the on-disk log

- **WHEN** the user scrolls up past the currently-loaded scrollback window of a live session whose on-disk log holds older history than is currently loaded
- **THEN** the pane SHALL read further back from the on-disk log so the loaded scrollback reaches strictly older content (advancing the first loaded byte earlier), rather than remaining pinned at the initially-loaded window — until the whole log is loaded or the bounded maximum single read is reached

#### Scenario: Extension does not eagerly replay the whole log

- **WHEN** a scroll-past-the-window extension occurs on a very large on-disk log
- **THEN** the read SHALL remain bounded by the maximum single-read size rather than loading the entire log at once

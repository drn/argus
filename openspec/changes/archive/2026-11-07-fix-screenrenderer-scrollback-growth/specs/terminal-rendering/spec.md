## ADDED Requirements

### Requirement: Screen-scraping emulators retain bounded scrollback

Emulators that exist only to reconstruct the visible screen as text (the needs-input/content-idle `ScreenRenderer` and the usage/Codex probe renderers) SHALL NOT accumulate scrollback across renders. Each reuse of such an emulator SHALL leave its scrollback empty or at most one line, independent of how many lines the rendered input scrolled off screen.

#### Scenario: Repeated renders of a tall tail

- **WHEN** a shared `ScreenRenderer` renders a tail taller than the screen 50 times
- **THEN** the emulator's scrollback length after the 50th render equals its length after the first render and does not exceed one line

#### Scenario: Visible output unchanged

- **WHEN** a tail is rendered after the scrollback bound is applied
- **THEN** the returned visible screen text is identical to the text produced before the change

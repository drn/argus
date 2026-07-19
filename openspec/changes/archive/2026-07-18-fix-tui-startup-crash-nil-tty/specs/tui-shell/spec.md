## ADDED Requirements

### Requirement: Startup requires a controlling terminal

The shell SHALL verify that a real controlling terminal is available before constructing its tcell screen. If no controlling terminal is available, `Run()` SHALL return a clear, descriptive error and SHALL NOT proceed to construct or configure a tcell screen.

#### Scenario: No controlling terminal available

- **WHEN** the application is launched with no controlling terminal (e.g. a detached or headless process)
- **THEN** `Run()` returns an error describing the missing terminal, and the application never constructs a tcell screen or calls `EnableMouse`/`EnablePaste`

#### Scenario: A controlling terminal is available

- **WHEN** the application is launched from a normal interactive terminal
- **THEN** the terminal check succeeds and the application proceeds to construct its tcell screen exactly as before

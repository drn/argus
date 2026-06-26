## ADDED Requirements

### Requirement: Visibility-driven viewer claim

While viewing a task's terminal, the SPA SHALL hold an active-viewer claim on the
session sized to the terminal's current `(cols, rows)`. When the page becomes
hidden (the `visibilitychange` event reports hidden, e.g. the tab is backgrounded
or the device is locked), the SPA SHALL release its viewer claim so a backgrounded
web app no longer constrains the shared PTY size for other viewers. When the page
becomes visible again, the SPA SHALL re-assert its claim at the terminal's current
dimensions. On `pagehide`/unload the SPA SHALL make a best-effort release on top of
the stream-disconnect path.

#### Scenario: Backgrounding the tab releases the claim
- **WHEN** the SPA is showing a terminal and the page becomes hidden
- **THEN** the SPA releases its viewer claim so the session size is recomputed without it

#### Scenario: Returning to the tab re-asserts the size
- **WHEN** the page becomes visible again while a task terminal is open
- **THEN** the SPA re-registers its viewer at the terminal's current dimensions

#### Scenario: Closing the app releases the claim
- **WHEN** the SPA page is hidden via unload/`pagehide`
- **THEN** the SPA best-effort releases its viewer claim, and the stream-disconnect path removes it regardless

## ADDED Requirements

### Requirement: Task detail occupies the visible mobile viewport

The mobile PWA SHALL keep an open task detail panel aligned with the visible viewport and its controls accessible when the soft keyboard is closed or open. Viewport updates SHALL preserve terminal touch and momentum scrolling.

#### Scenario: Open after browsing a long task list

- **WHEN** the user opens a task after scrolling the task list
- **THEN** the detail header begins at the top of the visible viewport and the composer remains within it

#### Scenario: Keyboard is dismissed

- **WHEN** the user taps Send and the soft keyboard closes after the detail view was sized and translated for it
- **THEN** the detail panel expands to the visible viewport and does not leave the task list exposed above it

#### Scenario: App returns to foreground

- **WHEN** the mobile app returns to the foreground with a task detail open
- **THEN** the panel reconciles its viewport size and position even if the browser did not deliver a usable earlier viewport event

#### Scenario: Terminal momentum scroll is active

- **WHEN** the visual viewport changes during a terminal touch or momentum scroll
- **THEN** geometry writes wait until scrolling settles, without interrupting the gesture

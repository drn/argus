## ADDED Requirements

### Requirement: Zoom an image artifact in Argus Web

When an image artifact is rendered in Argus Web (paneled viewer or full-screen overlay), clicking or tapping the image SHALL toggle it between fit-to-pane display and actual pixel size. While at actual size, the image's container SHALL be scrollable so the full image can be panned into view. A second click/tap, or leaving the artifact view, SHALL return the image to fit-to-pane. This behavior SHALL be identical in both presentations since they share one rendering path.

#### Scenario: Zoom in on a large screenshot

- **WHEN** a user clicks or taps an image artifact that is currently fit to its pane
- **THEN** the image switches to actual pixel size and its container becomes scrollable

#### Scenario: Zoom back out

- **WHEN** a user clicks or taps an image artifact that is currently at actual size
- **THEN** the image returns to fit-to-pane display

#### Scenario: Same behavior in both presentations

- **WHEN** the same image artifact is opened via the paneled viewer versus the full-screen "Open" overlay
- **THEN** the zoom toggle behaves identically in both

#### Scenario: Leaving the artifact resets zoom

- **WHEN** a user navigates away from a zoomed image artifact and later reopens it
- **THEN** it is shown fit-to-pane, not still zoomed

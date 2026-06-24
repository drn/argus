## ADDED Requirements

### Requirement: Live system-metrics panel in Settings

The Settings view SHALL include a System panel that displays live host-load
metrics: CPU utilization percent and the 1/5/15-minute load average, memory and
swap usage (used/total with percent bars), disk usage of the Argus data-directory
filesystem (used/total/free with percent and the path), the Argus process resident
memory, host uptime, and the active/idle agent-session counts. The panel SHALL
fetch `GET /api/system-metrics` and refresh on a short interval (~2 seconds) only
while the Settings tab is visible, and SHALL stop refreshing when the user navigates
away so no background polling continues for a hidden tab. A metric reported as
unavailable SHALL be rendered as a placeholder (e.g. an em dash) rather than a
misleading zero.

#### Scenario: Panel populates on Settings open
- **WHEN** the user opens the Settings tab
- **THEN** the System panel fetches metrics immediately and renders CPU, load, memory, swap, disk, process memory, uptime, and session counts

#### Scenario: Panel refreshes live while visible
- **WHEN** the Settings tab remains open
- **THEN** the panel re-fetches and re-renders the metrics roughly every 2 seconds

#### Scenario: Polling stops when the tab is hidden
- **WHEN** the user navigates away from the Settings tab
- **THEN** the refresh interval is cleared and no further metric fetches occur until the tab is reopened

#### Scenario: Unavailable metric shows a placeholder
- **WHEN** a metric is reported unavailable by the API
- **THEN** that row renders a placeholder instead of a numeric value

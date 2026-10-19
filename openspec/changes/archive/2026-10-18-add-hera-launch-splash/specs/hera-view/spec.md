## ADDED Requirements

### Requirement: Hera panes show the launch splash while a role's task launches

The Hera coordinator and agent panes SHALL render the Tasks view's LAUNCH banner, with a status line derived from task and session state, from the moment the bound task is FIRST launching until its session writes its first output. The splash is for a task's first launch only: it SHALL NOT be shown for a task the pane has observed producing output (a per-task latch that survives rebinds and respawns) or whose session log is non-empty, so size-drift restarts, zero-byte re-dialed handles for established agents, and failed revives never show it. A bound task with status `pending` and no session SHALL show `PREPARING WORKTREE`; a live session that has never written output SHALL show `STARTING AGENT`. The splash SHALL NOT be shown over a session that has output, over the closed-out banner, or while the pane is "Re-rendering…", SHALL give up after 45 seconds per bound session, and SHALL paint only within the pane rect in both split and fullscreen layouts.

#### Scenario: Daemon-spawned worker appears pending
- **WHEN** a worker is spawned by an MCP tool and its task is `pending` with no session
- **THEN** its agent pane shows the LAUNCH banner with `PREPARING WORKTREE`

#### Scenario: Session started but silent
- **WHEN** the worker's session is alive and has written no bytes
- **THEN** the pane shows `STARTING AGENT`, and the splash disappears on the first output

#### Scenario: Dead role is not a launch
- **WHEN** a role's task is `in_progress` with no session
- **THEN** the pane shows the normal "Session not running" placeholder, not the splash

#### Scenario: Established task never splashes
- **WHEN** a task with recorded output gets a fresh zero-byte session handle, a kill+resume restart, or reverts to `pending` after a failed revive
- **THEN** the pane keeps its replay, "Re-rendering…", or placeholder rendering and shows no splash

#### Scenario: Pane too small for the wordmark
- **WHEN** the pane is shorter than the banner or narrower than the wordmark
- **THEN** only the status line is drawn

#### Scenario: Higher-priority states win
- **WHEN** the closed-out banner is armed or the pane is "Re-rendering…"
- **THEN** the splash is not drawn

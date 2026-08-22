## ADDED Requirements

### Requirement: Forced build/test cache environment redirect

The system SHALL force `GOCACHE` and `PLAYWRIGHT_BROWSERS_PATH` onto every spawned agent's environment, pointed outside `~/Library/{Application Support,Containers,Caches}`, so that Go builds and Playwright browser downloads triggered by an agent (or any process it forks) do not write under the macOS TCC-gated tree and trigger the "access data from other apps" prompt. This applies unconditionally, with no per-project or per-backend opt-out, mirroring the existing forced `TERM`/`COLORTERM` environment.

#### Scenario: Go build cache redirected for every spawned agent
- **WHEN** a command is built for any task, regardless of backend or worktree
- **THEN** the command's environment sets `GOCACHE` to a path under `~/.argus/cache/` rather than the tool's own default under `~/Library/Caches`

#### Scenario: Playwright browser cache redirected for every spawned agent
- **WHEN** a command is built for any task, regardless of backend or worktree
- **THEN** the command's environment sets `PLAYWRIGHT_BROWSERS_PATH` to a path under `~/.argus/cache/` rather than the tool's own default under `~/Library/Caches`

#### Scenario: Redirect applies even when the parent environment already sets these variables
- **WHEN** the parent process environment already defines `GOCACHE` or `PLAYWRIGHT_BROWSERS_PATH`
- **THEN** the spawned agent's environment uses argus's forced value, not the inherited one

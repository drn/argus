## ADDED Requirements

### Requirement: Dev-stack teardown before removal

`RemoveWorktree` SHALL stop any devbox-managed dev-stack process (`process-compose`, `mysqld`, `redis-server`, `postgres`, or `caddy`) whose command line embeds the worktree's exact path, before removing the worktree's git registration or files. This step SHALL apply uniformly to every worktree-removal call site, since all of them funnel through `RemoveWorktree`.

The teardown SHALL send `SIGTERM` to every matched process, wait a short bounded grace period, then send `SIGKILL` to any matched process still running — it SHALL NOT assume that signaling a `process-compose` supervisor alone is sufficient to stop its child services. Matching SHALL require the worktree path to appear as a whole path segment in a process's command line, not merely as a substring, so a worktree whose name is a string-prefix of a sibling worktree's name is never mistaken for it.

This step SHALL be best-effort: a failure to scan for or signal a process SHALL be logged and SHALL NOT block or fail worktree removal.

#### Scenario: Dev-stack process for this worktree is stopped

- **WHEN** a worktree being removed has a running `process-compose`, `mysqld`, `redis-server`, `postgres`, or `caddy` process whose command line references that worktree's exact path
- **THEN** `RemoveWorktree` sends `SIGTERM` to that process before removing the worktree, and `SIGKILL`s it if it is still running after the grace period

#### Scenario: Supervisor-only signal is not assumed sufficient

- **WHEN** a `process-compose` supervisor and one or more of its service processes (e.g. `redis-server`, `caddy`) are running for the worktree being removed
- **THEN** every one of those processes is signaled directly, not only the supervisor

#### Scenario: Sibling worktree with a prefix name is not affected

- **WHEN** worktree `Sherlock/3b` is being removed and a dev-stack process is running for a separate, still-live worktree `Sherlock/3b-more`
- **THEN** the teardown step does not signal the process belonging to `Sherlock/3b-more`

#### Scenario: No dev-stack process running is a no-op

- **WHEN** a worktree being removed has no running process referencing its path
- **THEN** the teardown step does nothing further and removal proceeds immediately

#### Scenario: Scan failure never blocks removal

- **WHEN** the process scan used to find dev-stack processes cannot run (e.g. the scanning mechanism is unavailable on the current platform)
- **THEN** the teardown step logs the failure and worktree removal proceeds as if no dev-stack process was found

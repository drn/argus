## ADDED Requirements

### Requirement: Dev-stack orphan diagnostic

`argus doctor` SHALL additionally report any currently-running devbox dev-stack process (`process-compose`, `mysqld`, `redis-server`, `postgres`, or `caddy`) whose command line embeds a worktree path that no longer exists on disk. This check SHALL be independent of the binary-coherence table and verdict: it is printed as its own section and SHALL NOT alter `argus doctor`'s exit-code contract, and it SHALL NOT terminate, signal, or otherwise mutate any process it reports — it is read-only, matching every other gather step `argus doctor` performs.

The check SHALL report exactly one of three states:

- **Found** — one or more dev-stack processes reference a worktree path that no longer exists; each is listed with its PID, process name, and the missing worktree path.
- **None found** — the scan ran successfully and found no such process.
- **Unknown** — the process scan itself could not run (e.g. the scanning mechanism is unavailable on the current platform); this SHALL be reported distinctly from "none found" rather than assumed clean.

#### Scenario: Orphaned dev-stack process found

- **WHEN** a `mysqld`, `redis-server`, `postgres`, `caddy`, or `process-compose` process is running with a worktree path in its command line that no longer exists on disk
- **THEN** `argus doctor` reports it as found, listing its PID, process name, and the missing path

#### Scenario: No orphans found

- **WHEN** every dev-stack process currently running references a worktree path that still exists on disk
- **THEN** `argus doctor` reports none found

#### Scenario: Scan unavailable degrades to unknown, not a false negative

- **WHEN** the process-scanning mechanism cannot run on the current platform
- **THEN** `argus doctor` reports the check as unknown rather than "none found"

#### Scenario: Check does not change the exit-code contract

- **WHEN** orphaned dev-stack processes are found but the binary-coherence verdict is healthy
- **THEN** `argus doctor` still exits zero

#### Scenario: Check never signals a process

- **WHEN** `argus doctor` reports one or more orphaned dev-stack processes
- **THEN** none of those processes are terminated, signaled, or otherwise modified by the check itself

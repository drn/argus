## ADDED Requirements

### Requirement: Durable hera-nuke reclaim via reconciliation sweep

A hera nuke's worktree removal, branch deletion, and eventual task-row prune are dispatched from a backgrounded goroutine with no durability guarantee of their own — a goroutine that loses the race against process exit, a daemon restart, a panic, or the host sleeping leaves that work permanently unfinished with no record that it never completed. The system SHALL provide a reconciliation sweep that re-derives, from durable state alone, every hera-nuked task whose cleanup has not yet finished, and completes it. This is a separate mechanism from the existing orphan-worktree sweep (which finds directories untracked by ANY task row); this sweep targets directories and branches that a task row still references but that a hera nuke already promised — and may have failed — to remove.

The sweep's candidate set is every task with a `hera_bindings` row whose `end_reason` is the nuke-path's end reason (`user_deleted`) and that holds no live (`ended_at IS NULL`) Hera role binding. For each candidate: if its worktree directory still exists on disk, the worktree + local/remote branch removal is retried; if any additional stacked branch was confirmed safe at nuke time and is not recorded as operator-excluded and still exists on origin, its removal is retried; once both are confirmed complete and the task holds no live session, the row is pruned via the existing `PruneTasks` explicit-ID-list contract — unchanged, and re-verifying the live-binding guard exactly as it already does for every other caller.

The sweep SHALL be idempotent and safe to run repeatedly: a candidate whose cleanup is already fully complete (worktree gone, branches gone) is pruned on the next run with no further filesystem or network work attempted, and a candidate with nothing left to do performs no action at all.

#### Scenario: A leaked worktree from an interrupted nuke is swept up

- **WHEN** the sweep runs and finds a nuke-ended task whose worktree directory still exists on disk
- **THEN** the worktree and its local and remote branch are removed, and the task row is then pruned once no live session remains

#### Scenario: A fully-reclaimed task that was never pruned is caught

- **WHEN** the sweep runs and finds a nuke-ended task whose worktree no longer exists and has no confirmed-safe branch still on origin, but whose row was never pruned
- **THEN** the row is pruned directly with no filesystem or network work attempted

#### Scenario: A task still holding a live binding is never touched by the sweep

- **WHEN** the sweep runs and a candidate task has since gained a live Hera role binding
- **THEN** that task is skipped entirely, exactly as the underlying `PruneTasks` guard already requires

#### Scenario: A task with a live session is left for a later sweep

- **WHEN** the sweep runs and a candidate task still has a live session (e.g. its reclaim-triggered stop has not yet exited)
- **THEN** the sweep retries only the worktree/branch removal, not the prune, and leaves the row for a later run once the session has exited

#### Scenario: The sweep runs at daemon startup and self-heals pre-existing leaks

- **WHEN** the daemon starts
- **THEN** the sweep runs once, alongside the existing hera-binding startup reconciliation, and finishes cleanup for any task left over from before this mechanism existed — with no separate manual migration step

#### Scenario: The sweep also runs periodically without requiring a restart

- **WHEN** the daemon has been running for one sweep interval
- **THEN** the sweep runs again on its own ticker, catching a reclaim goroutine lost mid-session without requiring a daemon restart

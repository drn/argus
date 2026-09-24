## ADDED Requirements

### Requirement: task_recycle resets a task's context window without losing its place

The system SHALL provide a daemon-side primitive that terminates a task's running agent session and starts a fresh one on the identical argus task row — same worktree, same branch, same task ID — seeded with a caller-supplied handoff note plus the task's original prompt as historical background. Unlike `recycle_coord`, this primitive SHALL require no Hera role or binding: it operates directly on the task.

The primitive SHALL defer the actual kill-and-restart until the task's live session reaches genuine idleness (no forced interruption mid-turn), since the call is normally made BY the session it will kill from inside its own tool-call turn. A missing or already-exited session SHALL count as idle immediately. If the session never goes idle within a bounded timeout, the primitive SHALL give up without restarting the task and SHALL leave the task's `SessionID`/`Prompt` unchanged.

Before restarting, the primitive SHALL terminate any stray background job tied to the outgoing session, the same guard `recycle_coord` applies.

#### Scenario: Restart clears the stale session and seeds the fresh prompt

- **WHEN** the primitive restarts a task with an outgoing `SessionID` and a caller-supplied handoff note
- **THEN** the persisted task row has an empty `SessionID` and a `Prompt` containing both the handoff note and the original prompt, with the handoff note appearing before the original prompt

#### Scenario: No live session restarts immediately

- **WHEN** the primitive is invoked for a task with no live session (already exited, or never started)
- **THEN** it starts a fresh session directly rather than waiting or erroring

#### Scenario: A session that never goes idle does not restart

- **WHEN** the task's live session never reaches idleness before the bounded timeout elapses
- **THEN** the primitive gives up without killing the session or mutating the task row

### Requirement: Seed prompt orders the handoff note before the original prompt

The system SHALL compose the fresh session's opening prompt with the handoff note FIRST, followed by the task's original prompt explicitly marked as historical background not to be treated as a current instruction — mirroring `hera.BuildRecycleSeedPrompt`'s rationale that a fresh session should anchor on current state rather than re-deriving "start from scratch" from a stale mission that may already be substantially done.

#### Scenario: Handoff note precedes the original prompt

- **WHEN** the seed prompt is built from an original prompt and a handoff note
- **THEN** the handoff note's text appears earlier in the composed prompt than the original prompt's text

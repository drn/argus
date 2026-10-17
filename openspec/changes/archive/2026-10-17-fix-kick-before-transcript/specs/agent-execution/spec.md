## ADDED Requirements

### Requirement: Rerender requires a resumable conversation

The system SHALL defer kill-and-resume rerenders when a backend family's transcript-availability callback reports that the recorded session has no transcript or cannot be inspected. The guard SHALL apply to classic TUI, Hera panes, REST resize, and runner kick entry points. Deferred TUI kicks SHALL preserve retry eligibility through the existing deferred callback and cache invalidation. Families without a transcript callback SHALL retain their existing behavior. Transcript lookup SHALL use the task's account and exact recorded session ID. Deferrals SHALL be logged with a `[rerender]` prefix.

#### Scenario: Quiet fresh Claude has no transcript

- **WHEN** a quiet Claude session crosses the rerender width threshold before its first transcript exists
- **THEN** the session remains alive and the kick remains eligible for retry

#### Scenario: Transcript becomes available

- **WHEN** a previously deferred session writes its recorded transcript and a retry otherwise qualifies
- **THEN** the kick resumes the saved conversation at the requested dimensions

#### Scenario: Unrelated transcript is insufficient

- **WHEN** the worktree has another transcript but none for the live session's recorded ID
- **THEN** the live session is not stopped for a rerender

### Requirement: Missing-transcript relaunch recovers fresh

Every relaunch through the shared runner SHALL validate a requested resume using the backend family's transcript callback. If that exact transcript is absent, it SHALL generate a new session ID, launch through the fresh-session path with the original task prompt, return the effective ID through daemon start RPCs, and log the recovery with a `[resume]` prefix. Existing transcripts and families without the callback SHALL retain resume behavior. Inspection errors SHALL fail the launch without replacing the recorded identity. Reattaching an already-live session SHALL not replace its identity.

REST resume and restart responses SHALL include the effective `session_id`. The remote TUI SHALL retain that ID before persisting task updates, and SHALL query transcript availability on the daemon host through the existing session-list endpoint. Web and macOS clients MAY ignore the additive response field because the daemon persists the effective identity.

#### Scenario: Dead never-saved Claude session

- **WHEN** TUI Enter, REST resume or restart, or daemon revival relaunches a Claude task whose transcript does not exist
- **THEN** the launched command pins a new session ID, includes the original prompt, and omits the old resume flag

#### Scenario: Saved Claude conversation

- **WHEN** the requested session's transcript exists in the task's configured account
- **THEN** the relaunch resumes that ID and omits the original prompt

#### Scenario: Live session reattachment

- **WHEN** a client asks to resume a session that is already alive before its first transcript exists
- **THEN** it reattaches without minting a replacement identity

#### Scenario: Transcript location cannot be resolved

- **WHEN** a resume has no recorded worktree path or its stored account is no longer configured
- **THEN** launch fails without replacing the recorded session ID

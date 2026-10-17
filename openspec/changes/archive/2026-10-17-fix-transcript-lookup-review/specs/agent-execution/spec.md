## ADDED Requirements

### Requirement: Transcript lookup matches launch identity

Claude transcript availability SHALL use the effective launch configuration directory, including backend environment overrides, and SHALL accept the exact session transcript under either logical or physical worktree encoding. Inspection errors MUST preserve the recorded identity and defer kicks.

#### Scenario: Physical worktree transcript

- **WHEN** a task uses a symlinked worktree and its exact transcript exists under the physical path
- **THEN** resume preserves the saved conversation

#### Scenario: Backend configuration override

- **WHEN** a backend maps CLAUDE_CONFIG_DIR to another directory
- **THEN** lookup uses the same directory as launch

#### Scenario: Unreadable transcript directory

- **WHEN** lookup encounters a filesystem permission error
- **THEN** recovery preserves the stored identity and kick defers without stopping the session

### Requirement: Remote transcript probes use exact availability

The Claude sessions REST endpoint SHALL accept `check_session_id` and return `{available}` from the backend's exact transcript check without parsing session metadata. Remote TUI kick checks SHALL use this query only for candidate kicks. Existing session listing behavior SHALL remain unchanged; web and macOS clients require no changes to their existing requests.

#### Scenario: Remote physical transcript

- **WHEN** remote TUI checks a saved session under the physical worktree path
- **THEN** the daemon returns available using the same lookup as local resume

### Requirement: Explicit revive recovers an unsaved live session

An explicit Hera revive SHALL restart an idle live session with a fresh identity and its original prompt when its transcript is missing. Automatic width kicks SHALL continue deferring. Relaunch inspection errors SHALL preserve the existing identity and attempt resume.

#### Scenario: Live unsaved revive

- **WHEN** the coordinator explicitly revives an idle live session without its first transcript
- **THEN** the session restarts fresh and the recovered identity is persisted through RPC

#### Scenario: Uncertain relaunch

- **WHEN** transcript inspection errors during relaunch
- **THEN** the existing session ID is retained for resume

### Requirement: Deferred Hera kicks back off

Consecutive deferred pane kicks SHALL retry at 10, 20, 40, then 60 second intervals, capped at 60 seconds with no hard give-up. Rebinding SHALL reset the backoff.

#### Scenario: Repeated deferrals

- **WHEN** a pane remains bound and its kicks repeatedly defer
- **THEN** retries slow to the cap and remain eligible indefinitely

## MODIFIED Requirements

### Requirement: Missing-transcript relaunch recovers fresh

Every relaunch through the shared runner SHALL validate a requested resume using the backend family's transcript callback. If that exact transcript is absent, it SHALL generate a new session ID, launch through the fresh-session path with the original task prompt, return the effective ID through daemon start RPCs, and log the recovery with a `[resume]` prefix. Existing transcripts and families without the callback SHALL retain resume behavior. Inspection errors SHALL preserve the recorded identity and attempt resume, with normal launch validation still applying. Reattaching an already-live session SHALL not replace its identity.

REST resume and restart responses SHALL include the effective `session_id`. The remote TUI SHALL retain that ID before persisting task updates, and SHALL query exact transcript availability on the daemon host through the existing session-list endpoint. Web and macOS clients MAY ignore the additive response field because the daemon persists the effective identity.

#### Scenario: Unsaved Claude conversation

- **WHEN** a dead task requests resume but its exact Claude transcript is absent
- **THEN** it launches fresh with a new ID, includes the original prompt, and omits the old resume flag

#### Scenario: Saved Claude conversation

- **WHEN** the requested session's transcript exists in the task's configured account
- **THEN** the relaunch resumes that ID and omits the original prompt

#### Scenario: Live session reattachment

- **WHEN** a client asks to resume a session that is already alive before its first transcript exists
- **THEN** it reattaches without minting a replacement identity

#### Scenario: Transcript location cannot be resolved

- **WHEN** a resume cannot inspect its transcript location
- **THEN** it attempts resume without replacing the recorded session ID, and normal launch validation may still reject invalid task configuration

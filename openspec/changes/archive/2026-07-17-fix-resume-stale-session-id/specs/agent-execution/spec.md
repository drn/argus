## ADDED Requirements

### Requirement: Resume-time session-ID refresh for Claude backends

Before resuming a Claude-style backend, the system SHALL re-derive the most-recently-updated transcript UUID for the task's worktree and persist it as the task's session ID, so the resume targets the newest in-place session rather than a stale earlier one. This is the resume-time analog of post-exit capture, required because sessions that idle or lose their stream (hera workers) never reach the post-exit capture and so accrue newer transcripts while their recorded session ID stays pinned to the create-time value.

The refresh SHALL apply only to Claude-style backends; for codex, pi, opencode, and unrecognized backends it SHALL be a no-op so their resume semantics are unchanged. The refresh SHALL be a no-op — leaving the recorded session ID intact and never blanking it — when the task has no worktree, has no prior session ID, the worktree has no transcript, or the newest transcript equals the recorded ID. When it does change the ID, it SHALL both update the in-memory task (so the immediate resume uses the new ID) and persist the change to the task row.

#### Scenario: Claude resume targets the newest transcript

- **WHEN** a Claude-backed task with a recorded session ID is about to resume and its worktree holds a newer transcript than the recorded ID
- **THEN** the task's session ID is refreshed to the newest transcript UUID and the resume uses that ID

#### Scenario: Non-Claude backends are untouched

- **WHEN** the resume-time refresh runs for a codex, pi, opencode, or unrecognized backend
- **THEN** the recorded session ID is left unchanged and no transcript scan influences the resume

#### Scenario: Zero-transcript worktree falls back to the existing ID

- **WHEN** the resume-time refresh runs for a Claude-backed task whose worktree holds no transcript
- **THEN** the recorded session ID is left intact (never blanked) and the resume proceeds with it

#### Scenario: No prior session ID is not fabricated

- **WHEN** the resume-time refresh runs for a task that has no recorded session ID
- **THEN** no session ID is derived or written and the launch behaves as a fresh start

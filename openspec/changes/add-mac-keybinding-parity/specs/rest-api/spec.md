## ADDED Requirements

### Requirement: Claude session listing and switching endpoints

The system SHALL expose a `GET /api/tasks/{id}/claude-sessions` endpoint that lists the named task's available Claude sessions (via `internal/claudesession.List`, keyed on the task's worktree) and a `POST /api/tasks/{id}/claude-session` endpoint that switches the task to a different session, mirroring the TUI's `ctrl+r` session-switcher flow (`internal/tui/app.go`'s `openSessionPickerModal`/`switchSession`). Both endpoints SHALL be callable by any authenticated token (master, device, or plugin-scoped) and SHALL be restricted to Claude-backed tasks (matching the TUI's own `IsCodexBackend`/`IsPiBackend`/`IsOpencodeBackend` guard) — a non-Claude-backed task returns 400 rather than an empty or garbage session list.

#### Scenario: List sessions for a Claude-backed task

- **WHEN** a client calls `GET /api/tasks/{id}/claude-sessions` for a Claude-backed task
- **THEN** the response is 200 with `{"sessions": [{"id", "title", "branch", "pr_ref", "mod_time", "size_bytes"}, ...], "current_session_id": "..."}`, newest session first

#### Scenario: List sessions for a non-Claude-backed task

- **WHEN** a client calls `GET /api/tasks/{id}/claude-sessions` for a task whose resolved backend is Codex, Pi, or Opencode
- **THEN** the response is 400 identifying that session listing is Claude-only

#### Scenario: Switch to a different session

- **WHEN** a client posts `{"session_id": "<id>"}` to `POST /api/tasks/{id}/claude-session` for a Claude-backed task, where `<id>` differs from the task's current session
- **THEN** the task's stored session ID is updated to `<id>`, any live session for the task is stopped and restarted (or started fresh if none was running) resuming with `<id>`, and the response is 200 with `{"status": "switched", "pid": <int>}`

#### Scenario: Switch to the already-active session is a no-op

- **WHEN** a client posts the task's current session ID to `POST /api/tasks/{id}/claude-session`
- **THEN** no session is stopped or restarted and the response is 200 with `{"status": "unchanged"}`

#### Scenario: Switch for a non-Claude-backed task or unknown session ID

- **WHEN** a client posts to `POST /api/tasks/{id}/claude-session` for a non-Claude-backed task, or with a missing/empty `session_id`
- **THEN** the response is 400

#### Scenario: Task not found

- **WHEN** a client calls either endpoint for a task ID that does not exist
- **THEN** the response is 404

#### Scenario: Any authenticated token accepted

- **WHEN** a request authenticated with a device token or a plugin-scoped token calls either endpoint
- **THEN** the request is accepted (not rejected as master-only)

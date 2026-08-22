import Foundation

extension ArgusClient {
    /// `GET /api/tasks/{id}/claude-sessions` — lists a task's available Claude
    /// Code conversation sessions, newest first, mirroring the TUI's `ctrl+r`
    /// session-switcher flow (`internal/tui/app.go`'s `openSessionPickerModal`).
    ///
    /// Throws ``ArgusError/http(status:body:)`` with status 400
    /// (`error.isBadRequest`) when the task's backend isn't Claude — session
    /// listing is Claude-only — and status 404 (`error.isNotFound`) when the
    /// task doesn't exist.
    public func claudeSessions(taskID: String) async throws -> (sessions: [ClaudeSession], currentSessionID: String) {
        let resp: ClaudeSessionsResponse = try await getDecoding("/api/tasks/\(pc(taskID))/claude-sessions")
        return (resp.sessions, resp.currentSessionID)
    }

    /// `POST /api/tasks/{id}/claude-session` — switches the task to a
    /// different Claude session. `status` is `"switched"` (any live session
    /// was stopped and restarted, or started fresh, resuming with
    /// `sessionID`) or `"unchanged"` (`sessionID` was already current — a
    /// legitimate no-op, `pid` is nil in that case).
    ///
    /// Throws ``ArgusError/http(status:body:)`` with status 400 for a
    /// non-Claude-backed task or an empty `sessionID`, and status 404 when
    /// the task doesn't exist.
    public func switchClaudeSession(taskID: String, sessionID: String) async throws -> (status: String, pid: Int?) {
        let resp: ClaudeSessionSwitchResponse = try await sendDecoding(
            "POST", "/api/tasks/\(pc(taskID))/claude-session",
            body: ["session_id": sessionID])
        return (resp.status, resp.pid)
    }
}

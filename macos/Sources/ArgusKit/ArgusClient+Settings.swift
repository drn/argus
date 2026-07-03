import Foundation

extension ArgusClient {
    /// `GET /api/settings`.
    public func settings() async throws -> Settings {
        try await getDecoding("/api/settings")
    }

    /// `PUT /api/settings` — partial update. The `sandbox` section requires the
    /// master token; other sections accept any authenticated token. Returns the
    /// refreshed snapshot.
    public func updateSettings(_ req: SettingsUpdate) async throws -> Settings {
        try await sendDecoding("PUT", "/api/settings", body: req)
    }

    private struct SkillsEnvelope: Decodable { let skills: [Skill] }

    /// `GET /api/skills` — optionally narrowed by project (adds that project's
    /// `.claude/skills/`) and a substring filter.
    public func skills(project: String? = nil, filter: String? = nil) async throws -> [Skill] {
        var q: [URLQueryItem] = []
        if let project, !project.isEmpty { q.append(.init(name: "project", value: project)) }
        if let filter, !filter.isEmpty { q.append(.init(name: "filter", value: filter)) }
        let env: SkillsEnvelope = try await getDecoding("/api/skills", query: q)
        return env.skills
    }

    // MARK: - Inbox / messaging

    /// `GET /api/tasks/{id}/inbox`. `unreadOnly` defaults to true (matching the
    /// MCP tool); pass a `sender` / `since` (RFC3339) / `limit` to narrow.
    public func inbox(taskID: String, unreadOnly: Bool = true, sender: String? = nil,
                     since: String? = nil, limit: Int? = nil) async throws -> Inbox {
        var q: [URLQueryItem] = [.init(name: "unread_only", value: unreadOnly ? "true" : "false")]
        if let sender, !sender.isEmpty { q.append(.init(name: "sender", value: sender)) }
        if let since, !since.isEmpty { q.append(.init(name: "since", value: since)) }
        if let limit, limit > 0 { q.append(.init(name: "limit", value: String(limit))) }
        return try await getDecoding("/api/tasks/\(pc(taskID))/inbox", query: q)
    }

    /// `POST /api/tasks/{id}/messages` — stages a message from the given task.
    public func sendMessage(fromTaskID: String, _ req: SendMessageRequest) async throws -> SendMessageResponse {
        try await sendDecoding("POST", "/api/tasks/\(pc(fromTaskID))/messages", body: req)
    }

    private struct AckResponse: Decodable { let acked: Int }

    /// `POST /api/tasks/{id}/inbox/ack` — marks the given message IDs read.
    /// Returns the count actually flipped.
    @discardableResult
    public func ackInbox(taskID: String, ids: [String]) async throws -> Int {
        let resp: AckResponse = try await sendDecoding("POST", "/api/tasks/\(pc(taskID))/inbox/ack",
                                                       body: ["ids": ids])
        return resp.acked
    }

    // MARK: - Task meta

    private struct MetaEnvelope: Decodable { let entries: [MetaEntry] }
    private struct MetaWriteResponse: Decodable { let written: Int }

    /// `GET /api/tasks/{id}/meta` — the sidecar metadata, optionally scoped to a
    /// namespace.
    public func taskMeta(taskID: String, namespace: String? = nil) async throws -> [MetaEntry] {
        var q: [URLQueryItem] = []
        if let namespace, !namespace.isEmpty { q.append(.init(name: "namespace", value: namespace)) }
        let env: MetaEnvelope = try await getDecoding("/api/tasks/\(pc(taskID))/meta", query: q)
        return env.entries
    }

    /// `PUT /api/tasks/{id}/meta` — upserts one row or a batch. Returns the
    /// number of rows written.
    @discardableResult
    public func putTaskMeta(taskID: String, _ req: MetaPutRequest) async throws -> Int {
        let resp: MetaWriteResponse = try await sendDecoding("PUT", "/api/tasks/\(pc(taskID))/meta", body: req)
        return resp.written
    }
}

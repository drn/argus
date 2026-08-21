import Foundation

extension ArgusClient {
    private struct TasksEnvelope: Decodable { let tasks: [Task] }

    /// `GET /api/tasks` with optional filters. `archived` accepts "0" (exclude,
    /// default), "1" (only archived), or "all".
    public func tasks(status: String? = nil, project: String? = nil,
                      archived: String? = nil) async throws -> [Task] {
        var q: [URLQueryItem] = []
        if let status, !status.isEmpty { q.append(.init(name: "status", value: status)) }
        if let project, !project.isEmpty { q.append(.init(name: "project", value: project)) }
        if let archived, !archived.isEmpty { q.append(.init(name: "archived", value: archived)) }
        let env: TasksEnvelope = try await getDecoding("/api/tasks", query: q)
        return env.tasks
    }

    /// `GET /api/tasks/{id}`.
    public func task(id: String) async throws -> Task {
        try await getDecoding("/api/tasks/\(pc(id))")
    }

    /// `POST /api/tasks` — creates a task and starts its agent session.
    /// Multipart uploads are not supported here.
    public func createTask(_ req: CreateTaskRequest) async throws -> CreateTaskResponse {
        try await sendDecoding("POST", "/api/tasks", body: req)
    }

    /// `DELETE /api/tasks/{id}` — stops the session, removes the worktree +
    /// branch, and deletes the row.
    public func deleteTask(id: String) async throws {
        try await sendVoid("DELETE", "/api/tasks/\(pc(id))")
    }

    /// `POST /api/tasks/{id}/stop` — stops the session and flips the task to
    /// in_review.
    public func stopTask(id: String) async throws {
        try await sendVoid("POST", "/api/tasks/\(pc(id))/stop")
    }

    /// `POST /api/tasks/{id}/restart` — re-spawns a finished session, resuming
    /// the prior conversation when the backend supports `--resume`.
    public func restartTask(id: String) async throws -> SessionActionResult {
        try await sendDecoding("POST", "/api/tasks/\(pc(id))/restart")
    }

    /// `POST /api/tasks/{id}/resume` — resumes (or starts fresh) the session.
    public func resumeTask(id: String) async throws -> SessionActionResult {
        try await sendDecoding("POST", "/api/tasks/\(pc(id))/resume")
    }

    /// `POST /api/tasks/{id}/archive`.
    public func archiveTask(id: String) async throws {
        try await sendVoid("POST", "/api/tasks/\(pc(id))/archive")
    }

    /// `POST /api/tasks/{id}/unarchive`.
    public func unarchiveTask(id: String) async throws {
        try await sendVoid("POST", "/api/tasks/\(pc(id))/unarchive")
    }

    /// `POST /api/tasks/{id}/rename`.
    public func renameTask(id: String, name: String) async throws {
        try await sendVoid("POST", "/api/tasks/\(pc(id))/rename", body: ["name": name])
    }

    /// `POST /api/tasks/{id}/status` — moves a task to one of
    /// pending/in_progress/in_review/complete.
    public func setStatus(id: String, status: String) async throws {
        try await sendVoid("POST", "/api/tasks/\(pc(id))/status", body: ["status": status])
    }

    /// `POST /api/tasks/{id}/fork` — forks a task; empty request fields inherit
    /// from the source.
    public func forkTask(id: String, _ req: ForkRequest = ForkRequest()) async throws -> CreateTaskResponse {
        try await sendDecoding("POST", "/api/tasks/\(pc(id))/fork", body: req)
    }

    /// `GET /api/tasks/{id}/raw` — the full `model.Task` shape (`internal/api/raw.go`),
    /// exposed as a type-erased ``JSONValue`` rather than the lossy ``Task``
    /// model. Includes fields (`session_id`, `result`, …) the lossy model
    /// never decodes; ``setPinned(id:pinned:)`` relies on this to round-trip
    /// them untouched.
    public func rawTask(id: String) async throws -> JSONValue {
        try await getDecoding("/api/tasks/\(pc(id))/raw")
    }

    /// `PUT /api/tasks/{id}/raw` — flips the `pinned` flag by GETting the raw
    /// task, mutating only that key, and PUTting the same object back.
    ///
    /// This goes through the raw ``JSONValue`` object rather than decoding
    /// into the lossy ``Task`` model (which has no property for
    /// `session_id`/`result`/etc.) so those fields survive the round trip
    /// unchanged. Per `internal/api/raw.go`'s `handleUpdateTaskRaw`, the
    /// server re-pins `worktree`/`branch`/`base_branch` to the existing row
    /// regardless of what's sent, so those three don't need special handling
    /// here.
    ///
    /// `handleUpdateTaskRaw` writes through `db.Update`, NOT the dedicated
    /// `db.SetPinned`/`db.SetArchived` methods that enforce "at most one of
    /// {Pinned, Archived} is true" (see `internal/model/task.go`'s
    /// `Task.SetPinned`/`SetArchived`) — `db.Update` persists whatever this
    /// object says verbatim. So when pinning (`pinned == true`), this also
    /// clears an already-`true` `archived` key client-side, mirroring
    /// `Task.SetPinned`'s own invariant, rather than risking a
    /// `(pinned: true, archived: true)` row the task list would render in
    /// both sections. It only *flips* an existing key — never inserts one
    /// that wasn't already present — since `archived` is `omitempty` on the
    /// wire and absence means false. Unpinning leaves `archived` untouched,
    /// matching `SetPinned`'s own asymmetry.
    public func setPinned(id: String, pinned: Bool) async throws {
        let raw = try await rawTask(id: id)
        guard case .object(var obj) = raw else {
            throw ArgusError.decoding("raw task \(id) is not a JSON object")
        }
        obj["pinned"] = .bool(pinned)
        if pinned, case .bool(true)? = obj["archived"] {
            obj["archived"] = .bool(false)
        }
        try await sendVoid("PUT", "/api/tasks/\(pc(id))/raw", body: JSONValue.object(obj))
    }

    private struct LinksEnvelope: Decodable { let links: [Link] }

    /// `GET /api/tasks/{id}/links` — http/https URLs extracted from the task's
    /// terminal output.
    public func links(taskID: String) async throws -> [Link] {
        let env: LinksEnvelope = try await getDecoding("/api/tasks/\(pc(taskID))/links")
        return env.links
    }
}

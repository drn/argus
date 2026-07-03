import Foundation

extension ArgusClient {
    /// `GET /api/tasks/{id}/output` — the most recent terminal output.
    /// `tailBytes` caps the body (pass nil for the server default, 32 KiB);
    /// `clean` strips ANSI control sequences. The returned ``OutputTail/total``
    /// (from `X-Output-Total`) is the `since` cursor for a gapless
    /// ``terminalStream(taskID:since:)``.
    public func output(taskID: String, tailBytes: Int? = nil,
                       clean: Bool = false) async throws -> OutputTail {
        var q: [URLQueryItem] = []
        if let tailBytes, tailBytes > 0 { q.append(.init(name: "bytes", value: String(tailBytes))) }
        if clean { q.append(.init(name: "clean", value: "1")) }
        let req = try makeRequest(method: "GET", path: "/api/tasks/\(pc(taskID))/output", query: q)
        let (data, http) = try await send(req)
        let total = UInt64(http.value(forHTTPHeaderField: "X-Output-Total") ?? "") ?? 0
        let source = http.value(forHTTPHeaderField: "X-Source") ?? ""
        return OutputTail(data: data, total: total, source: source)
    }

    /// `POST /api/tasks/{id}/input` — writes raw bytes to the agent's PTY.
    /// The server caps a single write at 64 KiB; the caller must keep `data`
    /// within that bound.
    public func sendInput(taskID: String, _ data: Data) async throws {
        let req = try makeRequest(method: "POST", path: "/api/tasks/\(pc(taskID))/input",
                                  body: data, contentType: "application/octet-stream")
        _ = try await send(req)
    }

    /// `GET /api/tasks/{id}/size` — the active PTY dimensions.
    public func terminalSize(taskID: String) async throws -> PTYSize {
        try await getDecoding("/api/tasks/\(pc(taskID))/size")
    }

    /// `POST /api/tasks/{id}/resize` — sends a SIGWINCH-equivalent resize.
    public func resize(taskID: String, rows: UInt16, cols: UInt16) async throws -> ResizeResult {
        // Field order matches resizeReq{Cols,Rows}; JSON is order-independent.
        try await sendDecoding("POST", "/api/tasks/\(pc(taskID))/resize",
                               body: ["rows": rows, "cols": cols])
    }
}

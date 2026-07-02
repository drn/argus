import Foundation

extension ArgusClient {
    /// `GET /api/tasks/{id}/git/status` — the worktree's git status.
    public func gitStatus(taskID: String) async throws -> GitStatus {
        try await getDecoding("/api/tasks/\(taskID)/git/status")
    }

    /// `GET /api/tasks/{id}/git/diff?path=…` — the unified diff for a
    /// worktree-relative path. Absolute paths and ".." are rejected server-side.
    public func gitDiff(taskID: String, path: String) async throws -> GitDiff {
        try await getDecoding("/api/tasks/\(taskID)/git/diff",
                              query: [.init(name: "path", value: path)])
    }

    /// `GET /api/tasks/{id}/files?dir=…` — the directory listing for a
    /// worktree-relative directory. Empty `dir` lists the worktree root.
    public func fileTree(taskID: String, dir: String = "") async throws -> FileTree {
        var q: [URLQueryItem] = []
        if !dir.isEmpty { q.append(.init(name: "dir", value: dir)) }
        return try await getDecoding("/api/tasks/\(taskID)/files", query: q)
    }
}

import Foundation

/// One changed path in a worktree, with a status normalized to a single letter
/// for icon/colour selection: `M` (modified), `A` (added/untracked),
/// `D` (deleted), `U` (unmerged).
public struct ChangedPath: Sendable, Equatable, Identifiable {
    public let status: String
    public let path: String
    public var id: String { path }

    public init(status: String, path: String) {
        self.status = status
        self.path = path
    }
}

/// Derives the union of changed paths a worktree has introduced by merging
/// `git status --short` (uncommitted) with `git diff --name-status base..HEAD`
/// (committed-on-branch). Pure so it can be unit-tested independently of the
/// network. Mirrors the SPA's `parseChangedFiles`.
public enum GitChangeSummary {
    /// - Parameters:
    ///   - status: the `Status` field of ``GitStatus`` (`git status --short`).
    ///   - branchFiles: the `BranchFiles` field (`git diff --name-status`).
    /// - Returns: de-duplicated changed paths sorted by path. The first
    ///   occurrence of a path wins (uncommitted status is preferred, since
    ///   `status` is scanned first).
    public static func changedPaths(status: String, branchFiles: String) -> [ChangedPath] {
        var order: [String] = []
        var byPath: [String: ChangedPath] = [:]
        func add(_ st: String, _ path: String) {
            let p = path.trimmingCharacters(in: .whitespaces)
            guard !p.isEmpty, byPath[p] == nil else { return }
            byPath[p] = ChangedPath(status: st, path: p)
            order.append(p)
        }

        // git status --short: "XY path" — X staged, Y worktree.
        for line in status.split(separator: "\n", omittingEmptySubsequences: false) {
            guard line.count >= 4 else { continue }
            let xy = String(line.prefix(2))
            let path = String(line.dropFirst(3))
            var st = "M"
            if xy.contains("?") || xy.contains("A") { st = "A" }
            else if xy.contains("D") { st = "D" }
            else if xy.contains("U") { st = "U" }
            else if xy.contains("M") { st = "M" }
            add(st, path)
        }

        // git diff --name-status base..HEAD: "X\tpath" (rename: "Rxxx\told\tnew").
        for line in branchFiles.split(separator: "\n", omittingEmptySubsequences: false) {
            let trimmed = line.trimmingCharacters(in: .whitespaces)
            guard !trimmed.isEmpty else { continue }
            let parts = trimmed.split(separator: "\t").map(String.init)
            guard parts.count >= 2, let code = parts[0].first else { continue }
            var st = "M"
            switch code {
            case "A": st = "A"
            case "D": st = "D"
            case "M": st = "M"
            default: st = "M" // R (rename), C (copy), T (type) → shown as M
            }
            add(st, parts[parts.count - 1])
        }

        return order.compactMap { byPath[$0] }.sorted { $0.path < $1.path }
    }
}

import Foundation

// NOTE ON JSON KEYS: the git endpoints return `gitutil` message structs
// (GitStatusRefreshMsg / FileDiffMsg / DirFilesMsg in internal/gitutil/types.go)
// which carry **no** `json:` tags. Go's encoder therefore emits the exported
// field names verbatim — PascalCase — so the CodingKeys below map to those exact
// keys (e.g. "TaskID", "BranchDiff", "IsDir"), not snake_case.

/// Result of `GET /api/tasks/{id}/git/status` (`gitutil.GitStatusRefreshMsg`).
public struct GitStatus: Sendable, Equatable, Decodable {
    public let taskID: String
    /// `git status --short` output.
    public let status: String
    /// `git diff --stat` (unstaged + staged).
    public let diff: String
    /// `git diff --stat` against the merge-base (committed changes).
    public let branchDiff: String
    /// `git diff --name-status` against the merge-base (for the file list).
    public let branchFiles: String

    enum CodingKeys: String, CodingKey {
        case taskID = "TaskID"
        case status = "Status"
        case diff = "Diff"
        case branchDiff = "BranchDiff"
        case branchFiles = "BranchFiles"
    }
}

/// Result of `GET /api/tasks/{id}/git/diff?path=…` (`gitutil.FileDiffMsg`).
public struct GitDiff: Sendable, Equatable, Decodable {
    public let taskID: String
    public let filePath: String
    /// The unified diff text.
    public let diff: String

    enum CodingKeys: String, CodingKey {
        case taskID = "TaskID"
        case filePath = "FilePath"
        case diff = "Diff"
    }
}

/// One entry in a directory listing (`gitutil.ChangedFile`).
public struct ChangedFile: Sendable, Equatable, Decodable {
    /// e.g. "M", "A", "D", "??".
    public let status: String
    public let path: String
    public let isDir: Bool

    enum CodingKeys: String, CodingKey {
        case status = "Status"
        case path = "Path"
        case isDir = "IsDir"
    }
}

/// Result of `GET /api/tasks/{id}/files?dir=…` (`gitutil.DirFilesMsg`).
public struct FileTree: Sendable, Equatable, Decodable {
    public let taskID: String
    public let dirPath: String
    /// `Files` is `null` in JSON when empty; decoded as an empty array.
    public let files: [ChangedFile]

    enum CodingKeys: String, CodingKey {
        case taskID = "TaskID"
        case dirPath = "DirPath"
        case files = "Files"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        taskID = try c.decode(String.self, forKey: .taskID)
        dirPath = try c.decode(String.self, forKey: .dirPath)
        files = try c.decodeIfPresent([ChangedFile].self, forKey: .files) ?? []
    }
}

/// A link extracted from a task's terminal output (`links.Link`), returned by
/// `GET /api/tasks/{id}/links`. The `isPR` JSON key is verbatim (Go tag
/// `json:"isPR"`).
public struct Link: Sendable, Equatable, Decodable {
    /// Markdown link text, or the URL itself for bare URLs.
    public let label: String
    public let url: String
    /// True when the URL points at a GitHub pull request.
    public let isPR: Bool

    enum CodingKeys: String, CodingKey {
        case label, url, isPR
    }

    /// The URL parsed and validated to a web scheme (http/https), else nil.
    /// Link strings originate in agent terminal output, so clients must not
    /// hand arbitrary schemes (file:, x-apple.systempreferences:, …) to the
    /// OS opener — same allowlist as the terminal's OSC-8 link handling.
    public var webURL: URL? {
        guard let u = URL(string: url), let scheme = u.scheme?.lowercased(),
              scheme == "http" || scheme == "https" else { return nil }
        return u
    }
}

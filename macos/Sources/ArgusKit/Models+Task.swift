import Foundation

/// The persisted workflow status of a task. String-backed so an unrecognised
/// value from a newer daemon still round-trips (via ``other(_:)``) rather than
/// failing to decode.
public enum TaskStatus: Sendable, Equatable, RawRepresentable {
    case pending
    case inProgress
    case inReview
    case complete
    case other(String)

    public init(rawValue: String) {
        switch rawValue {
        case "pending": self = .pending
        case "in_progress": self = .inProgress
        case "in_review": self = .inReview
        case "complete": self = .complete
        default: self = .other(rawValue)
        }
    }

    public var rawValue: String {
        switch self {
        case .pending: return "pending"
        case .inProgress: return "in_progress"
        case .inReview: return "in_review"
        case .complete: return "complete"
        case .other(let s): return s
        }
    }
}

/// A task as returned by `GET /api/tasks` and `GET /api/tasks/{id}` — the SPA
/// wire shape (`taskJSON` in `internal/api/handlers.go`).
///
/// `idle` / `needsInput` / `archived` / `prState` are `omitempty` on the wire,
/// so the custom decoder defaults the flags to `false` and treats the strings
/// as optional when the keys are absent.
public struct Task: Sendable, Equatable, Identifiable, Decodable {
    public let id: String
    public let name: String
    /// The raw status string (`pending` / `in_progress` / `in_review` / `complete`).
    public let status: String
    /// Runtime-derived: true only when in_progress and the session is missing
    /// or waiting for input.
    public let idle: Bool
    /// Runtime-derived: true when the daemon's watcher detects the agent is
    /// blocked waiting on the user (the red ? in the TUI).
    public let needsInput: Bool
    public let project: String
    public let branch: String?
    public let backend: String?
    public let elapsed: String?
    /// RFC3339 timestamp string.
    public let createdAt: String
    public let archived: Bool
    public let worktreePath: String?
    public let prompt: String?
    /// Cached GitHub PR review state (e.g. `awaiting-review`); absent for
    /// "none"/empty.
    public let prState: String?

    /// Typed view of ``status``.
    public var taskStatus: TaskStatus { TaskStatus(rawValue: status) }

    enum CodingKeys: String, CodingKey {
        case id, name, status, idle, project, branch, backend, elapsed, prompt, archived
        case needsInput = "needs_input"
        case createdAt = "created_at"
        case worktreePath = "worktree_path"
        case prState = "pr_state"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(String.self, forKey: .name)
        status = try c.decode(String.self, forKey: .status)
        idle = try c.decodeIfPresent(Bool.self, forKey: .idle) ?? false
        needsInput = try c.decodeIfPresent(Bool.self, forKey: .needsInput) ?? false
        project = try c.decode(String.self, forKey: .project)
        branch = try c.decodeIfPresent(String.self, forKey: .branch)
        backend = try c.decodeIfPresent(String.self, forKey: .backend)
        elapsed = try c.decodeIfPresent(String.self, forKey: .elapsed)
        createdAt = try c.decode(String.self, forKey: .createdAt)
        archived = try c.decodeIfPresent(Bool.self, forKey: .archived) ?? false
        worktreePath = try c.decodeIfPresent(String.self, forKey: .worktreePath)
        prompt = try c.decodeIfPresent(String.self, forKey: .prompt)
        prState = try c.decodeIfPresent(String.self, forKey: .prState)
    }
}

/// The envelope returned when creating (`POST /api/tasks`), or forking
/// (`POST /api/tasks/{id}/fork`) a task.
public struct CreateTaskResponse: Sendable, Equatable, Decodable {
    public let id: String
    public let name: String
    public let status: String
}

/// The response from `POST /api/tasks/{id}/restart` and `/resume`.
public struct SessionActionResult: Sendable, Equatable, Decodable {
    public let status: String
    public let pid: Int
    /// True when the daemon re-attached an already-live PTY rather than
    /// spawning fresh.
    public let healed: Bool

    enum CodingKeys: String, CodingKey { case status, pid, healed }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        status = try c.decode(String.self, forKey: .status)
        pid = try c.decodeIfPresent(Int.self, forKey: .pid) ?? 0
        healed = try c.decodeIfPresent(Bool.self, forKey: .healed) ?? false
    }
}

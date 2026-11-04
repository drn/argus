import Foundation

/// One path an agent asked to have writable. `path`/`reason`/`warning` are
/// agent-supplied (untrusted) text — render as plain text only.
public struct SandboxProposalEntry: Sendable, Equatable, Decodable {
    public let path: String
    public let reason: String
    /// Server-side risk note (e.g. a sensitive location); absent when benign.
    public let warning: String?

    public init(path: String, reason: String = "", warning: String? = nil) {
        self.path = path
        self.reason = reason
        self.warning = warning
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        path = try c.decodeIfPresent(String.self, forKey: .path) ?? ""
        reason = try c.decodeIfPresent(String.self, forKey: .reason) ?? ""
        let w = try c.decodeIfPresent(String.self, forKey: .warning)
        warning = (w?.isEmpty == false) ? w : nil
    }

    enum CodingKeys: String, CodingKey { case path, reason, warning }
}

/// A sandbox write-path proposal (`GET /api/sandbox/proposals`).
public struct SandboxProposal: Sendable, Equatable, Identifiable, Decodable {
    public let id: String
    public let taskID: String
    public let project: String
    public let entries: [SandboxProposalEntry]
    /// `pending`, `approved` or `rejected`.
    public let status: String
    public let scope: String
    public let appliedPaths: [String]
    public let createdAt: String
    public let decidedAt: String?
    public let decidedBy: String?

    public init(id: String, taskID: String = "", project: String = "",
                entries: [SandboxProposalEntry] = [], status: String = "pending",
                scope: String = "", appliedPaths: [String] = [], createdAt: String = "",
                decidedAt: String? = nil, decidedBy: String? = nil) {
        self.id = id; self.taskID = taskID; self.project = project; self.entries = entries
        self.status = status; self.scope = scope; self.appliedPaths = appliedPaths
        self.createdAt = createdAt; self.decidedAt = decidedAt; self.decidedBy = decidedBy
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        taskID = try c.decodeIfPresent(String.self, forKey: .taskID) ?? ""
        project = try c.decodeIfPresent(String.self, forKey: .project) ?? ""
        entries = try c.decodeIfPresent([SandboxProposalEntry].self, forKey: .entries) ?? []
        status = try c.decodeIfPresent(String.self, forKey: .status) ?? "pending"
        scope = try c.decodeIfPresent(String.self, forKey: .scope) ?? ""
        appliedPaths = try c.decodeIfPresent([String].self, forKey: .appliedPaths) ?? []
        createdAt = try c.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        decidedAt = try c.decodeIfPresent(String.self, forKey: .decidedAt)
        decidedBy = try c.decodeIfPresent(String.self, forKey: .decidedBy)
    }

    enum CodingKeys: String, CodingKey {
        case id, project, entries, status, scope
        case taskID = "task_id"
        case appliedPaths = "applied_paths"
        case createdAt = "created_at"
        case decidedAt = "decided_at"
        case decidedBy = "decided_by"
    }
}

/// Result of `POST /api/sandbox/proposals/{id}/approve`.
public struct SandboxApproveResult: Sendable, Equatable, Decodable {
    public let proposal: SandboxProposal
    public let scope: String
    public let applied: [String]
    public let alreadyGranted: [String]
    public let warnings: [String]
    public let takesEffect: String

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        proposal = try c.decode(SandboxProposal.self, forKey: .proposal)
        scope = try c.decodeIfPresent(String.self, forKey: .scope) ?? ""
        applied = try c.decodeIfPresent([String].self, forKey: .applied) ?? []
        alreadyGranted = try c.decodeIfPresent([String].self, forKey: .alreadyGranted) ?? []
        warnings = try c.decodeIfPresent([String].self, forKey: .warnings) ?? []
        takesEffect = try c.decodeIfPresent(String.self, forKey: .takesEffect) ?? ""
    }

    enum CodingKeys: String, CodingKey {
        case proposal, scope, applied, warnings
        case alreadyGranted = "already_granted"
        case takesEffect = "takes_effect"
    }
}

/// Body of the approve request. `paths` is omitted when nil (approve as-is).
public struct SandboxApproveRequest: Sendable, Encodable, Equatable {
    public let scope: String
    public let paths: [String]?

    public init(scope: String, paths: [String]? = nil) {
        self.scope = scope
        self.paths = paths
    }

    enum CodingKeys: String, CodingKey { case scope, paths }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(scope, forKey: .scope)
        try c.encodeIfPresent(paths, forKey: .paths)
    }
}

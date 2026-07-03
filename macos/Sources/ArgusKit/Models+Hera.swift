import Foundation

/// One hera role in the read-only roster (`heraRoleJSON` in
/// `internal/api/hera.go`). All fields are always present on the wire.
public struct HeraRole: Sendable, Equatable, Identifiable, Decodable {
    public let roleID: Int64
    public let orchID: Int64
    public let name: String
    /// `coordinator` | `worker` | `freelance`.
    public let kind: String
    /// `idle` | `working` | `blocked` | `done`; "" when no status row.
    public let status: String
    /// Live binding's argus task, or "" when unbound.
    public let taskID: String
    /// Bound task's display name, or "".
    public let taskName: String
    /// Bound task's workflow status, or "".
    public let taskStatus: String
    /// Whether the role has a live binding.
    public let live: Bool
    /// Bound task carries `meta:hera.ready_to_close=true`.
    public let readyToClose: Bool
    /// Role's `archived_at` is set.
    public let archived: Bool

    public var id: Int64 { roleID }

    enum CodingKeys: String, CodingKey {
        case roleID = "role_id"
        case orchID = "orch_id"
        case name, kind, status, live, archived
        case taskID = "task_id"
        case taskName = "task_name"
        case taskStatus = "task_status"
        case readyToClose = "ready_to_close"
    }
}

/// One orchestrator with its coordinator + worker roles (`heraOrchJSON`).
/// Freelance-kind roles are hoisted into ``HeraRoster/freelance``.
public struct HeraOrchestrator: Sendable, Equatable, Identifiable, Decodable {
    public let id: Int64
    public let name: String
    public let pinned: Bool
    public let archived: Bool
    public let roles: [HeraRole]
}

/// The full read-only hera roster from `GET /api/hera` (`heraJSON`).
public struct HeraRoster: Sendable, Equatable, Decodable {
    public let orchestrators: [HeraOrchestrator]
    public let freelance: [HeraRole]
}

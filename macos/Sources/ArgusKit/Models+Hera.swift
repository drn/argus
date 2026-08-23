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
    /// Mirrors the same daemon-authoritative idle-detection signal that drives
    /// `GET /api/tasks` and the SSE events stream (`add-mac-hera-rail-toggle`).
    public let needsInput: Bool

    public var id: Int64 { roleID }

    enum CodingKeys: String, CodingKey {
        case roleID = "role_id"
        case orchID = "orch_id"
        case name, kind, status, live, archived
        case taskID = "task_id"
        case taskName = "task_name"
        case taskStatus = "task_status"
        case readyToClose = "ready_to_close"
        case needsInput = "needs_input"
    }
}

/// One orchestrator with its coordinator + worker roles (`heraOrchJSON`).
/// Freelance-kind roles are hoisted into ``HeraRoster/freelance``.
public struct HeraOrchestrator: Sendable, Equatable, Identifiable, Decodable {
    public let id: Int64
    public let name: String
    public let pinned: Bool
    public let archived: Bool
    /// `active` | `backlog` | `blocked` | `done` (`add-hera-kanban-status`);
    /// always non-empty. Independent of nesting — set as-is regardless of
    /// whether this orchestrator is top-level or bridge-nested.
    public let kanbanStatus: String
    public let roles: [HeraRole]
    /// Non-nil identifies the parent orchestrator/role this orchestrator is
    /// nested beneath via a worker→coordinator bridge; both nil when this
    /// orchestrator is top-level (`add-mac-hera-rail-toggle`).
    public let bridgeParentOrchID: Int64?
    public let bridgeParentRoleID: Int64?
    /// True when any role in this orchestrator's subtree — including nested
    /// sub-orchestrators reached via bridges — currently needs input.
    public let subtreeNeedsInput: Bool

    enum CodingKeys: String, CodingKey {
        case id, name, pinned, archived, roles
        case kanbanStatus = "kanban_status"
        case bridgeParentOrchID = "bridge_parent_orch_id"
        case bridgeParentRoleID = "bridge_parent_role_id"
        case subtreeNeedsInput = "subtree_needs_input"
    }
}

/// The full read-only hera roster from `GET /api/hera` (`heraJSON`).
public struct HeraRoster: Sendable, Equatable, Decodable {
    public let orchestrators: [HeraOrchestrator]
    public let freelance: [HeraRole]
}

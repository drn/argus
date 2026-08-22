import Foundation

/// The pure decision behind the Hera-tree sidebar mode's dual-pane detail view
/// (`add-mac-hera-rail-toggle`, Stage 5): given a roster snapshot and the
/// currently selected role id, what should each of the two panes show? Kept
/// dependency-free and separate from any SwiftUI view (mirroring
/// ``HeraTreeBuilder``'s convention) so the decision — not just the rendering
/// glue — is unit-testable; `ArgusKitTests` has no SwiftUI/AppState harness.
public struct HeraDetailPanes: Sendable, Equatable {
    /// The active orchestrator's own coordinator task id, or nil when nothing
    /// is selected, the selected role no longer resolves to an orchestrator,
    /// or that orchestrator's coordinator has no live binding.
    public let leftTaskID: String?
    public let right: RightPane

    public enum RightPane: Sendable, Equatable {
        /// Nothing selected, the selected role vanished from the roster, or
        /// the selected worker/freelance role has no live binding.
        case none
        /// The selected worker or freelance role's own live task.
        case terminal(taskID: String)
        /// The selected role is itself a coordinator: show its orchestrator's
        /// roster instead of a terminal.
        case roster(orchID: Int64)
    }

    public init(leftTaskID: String?, right: RightPane) {
        self.leftTaskID = leftTaskID
        self.right = right
    }
}

/// Resolves ``HeraDetailPanes`` from a ``HeraRoster`` snapshot and the
/// selected role id. The daemon already resolves which orchestrator a role
/// belongs to (`role.orch_id`) and which role within it is the coordinator
/// (`role.kind == "coordinator"`) — this only reads that structure, it never
/// re-derives orchestration relationships.
public enum HeraDetailPaneResolver {
    public static func resolve(roster: HeraRoster, selectedRoleID: Int64?) -> HeraDetailPanes {
        guard let selectedRoleID else {
            return HeraDetailPanes(leftTaskID: nil, right: .none)
        }
        let allRoles = roster.orchestrators.flatMap(\.roles) + roster.freelance
        guard let role = allRoles.first(where: { $0.roleID == selectedRoleID }) else {
            return HeraDetailPanes(leftTaskID: nil, right: .none)
        }

        let orch = roster.orchestrators.first { $0.id == role.orchID }
        let leftTaskID = orch?.roles
            .first { $0.kind == "coordinator" }
            .flatMap { $0.live && !$0.taskID.isEmpty ? $0.taskID : nil }

        let right: HeraDetailPanes.RightPane
        if role.kind == "coordinator" {
            right = .roster(orchID: role.orchID)
        } else if role.live, !role.taskID.isEmpty {
            right = .terminal(taskID: role.taskID)
        } else {
            right = .none
        }

        return HeraDetailPanes(leftTaskID: leftTaskID, right: right)
    }
}

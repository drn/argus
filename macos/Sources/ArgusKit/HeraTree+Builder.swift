import Foundation

/// A node in the Hera-tree sidebar mode's nested tree (`add-mac-hera-rail-toggle`):
/// either an orchestrator header or one of its roles. A role node's
/// `children` holds the (at most one, but modeled as a list defensively) nested
/// orchestrator bridged beneath it, so the tree can recurse to any depth,
/// mirroring the TUI rail's `BridgeSubtree`.
public struct HeraTreeNode: Sendable, Equatable, Identifiable {
    public enum Kind: Sendable, Equatable {
        case orchestrator(HeraOrchestrator)
        case role(HeraRole)
    }

    public let kind: Kind
    public let children: [HeraTreeNode]

    /// Globally stable across a roster refresh (`orch-<id>` / `role-<id>`) so
    /// SwiftUI identity — and this file's ``HeraFoldState`` — survives a poll.
    public var id: String {
        switch kind {
        case .orchestrator(let o): return "orch-\(o.id)"
        case .role(let r): return "role-\(r.roleID)"
        }
    }

    public init(kind: Kind, children: [HeraTreeNode]) {
        self.kind = kind
        self.children = children
    }
}

/// One kanban-status section of top-level orchestrators.
public struct HeraKanbanSection: Sendable, Equatable, Identifiable {
    /// `active` | `backlog` | `blocked` | `done`, or another value the daemon
    /// hasn't been taught yet — never dropped, just sorted last.
    public let status: String
    public let orchestrators: [HeraTreeNode]

    public var id: String { status }

    public init(status: String, orchestrators: [HeraTreeNode]) {
        self.status = status
        self.orchestrators = orchestrators
    }
}

/// The full Hera-tree sidebar mode's data: kanban-grouped top-level
/// orchestrators (with bridge-nested sub-orchestrators folded into their
/// parent role's children) plus the flat freelance list.
public struct HeraTree: Sendable, Equatable {
    public let sections: [HeraKanbanSection]
    public let freelance: [HeraRole]

    public init(sections: [HeraKanbanSection], freelance: [HeraRole]) {
        self.sections = sections
        self.freelance = freelance
    }
}

/// Builds a ``HeraTree`` from a flat ``HeraRoster``. Pure and dependency-free
/// for unit testing, mirroring ``FileTreeBuilder``'s convention. The daemon
/// already resolves bridge relationships server-side (`bridge_parent_orch_id`/
/// `bridge_parent_role_id`) — this never re-derives them, it only partitions
/// and groups.
public enum HeraTreeBuilder {
    /// Canonical kanban section ordering, matching the TUI rail's kanban
    /// grouping (`add-hera-kanban-status`). Any unrecognized status value
    /// sorts after these, alphabetically, rather than being dropped.
    public static let canonicalKanbanOrder = ["active", "backlog", "blocked", "done"]

    public static func build(_ roster: HeraRoster) -> HeraTree {
        // Role ids are a global primary key, so grouping nested orchestrators
        // by their bridge parent's role id (rather than also matching parent
        // orch id) is sufficient and matches the TUI's own bridge-by-role-id
        // convention.
        let allRoleIDs = Set(roster.orchestrators.flatMap { $0.roles.map(\.roleID) })

        var childrenByParentRole: [Int64: [HeraOrchestrator]] = [:]
        var topLevel: [HeraOrchestrator] = []
        for orch in roster.orchestrators {
            // A bridge parent role id that doesn't resolve to any role in this
            // snapshot (stale/dangling reference) falls back to top-level
            // rather than silently dropping the orchestrator from the tree.
            if let parentRoleID = orch.bridgeParentRoleID, allRoleIDs.contains(parentRoleID) {
                childrenByParentRole[parentRoleID, default: []].append(orch)
            } else {
                topLevel.append(orch)
            }
        }

        func buildNode(_ orch: HeraOrchestrator) -> HeraTreeNode {
            let roleNodes = orch.roles.map { role -> HeraTreeNode in
                let nested = (childrenByParentRole[role.roleID] ?? []).map(buildNode)
                return HeraTreeNode(kind: .role(role), children: nested)
            }
            return HeraTreeNode(kind: .orchestrator(orch), children: roleNodes)
        }

        var sectionOrder: [String] = []
        var sectionNodes: [String: [HeraTreeNode]] = [:]
        for orch in topLevel {
            let status = orch.kanbanStatus
            if sectionNodes[status] == nil {
                sectionNodes[status] = []
                sectionOrder.append(status)
            }
            sectionNodes[status]!.append(buildNode(orch))
        }

        let orderedStatuses = canonicalKanbanOrder.filter { sectionNodes[$0] != nil }
            + sectionOrder.filter { !canonicalKanbanOrder.contains($0) }.sorted()

        let sections = orderedStatuses.map { HeraKanbanSection(status: $0, orchestrators: sectionNodes[$0] ?? []) }
        return HeraTree(sections: sections, freelance: roster.freelance)
    }
}

/// Local, unpersisted expand/collapse state for ``HeraTreeNode``s in the
/// sidebar's Hera-tree mode. Keyed by ``HeraTreeNode/id``, which is stable
/// across a roster refresh, so a node's fold state survives a poll without
/// this ever being synced to the daemon or across clients.
public struct HeraFoldState: Sendable, Equatable {
    public private(set) var collapsedIDs: Set<String>

    public init(collapsedIDs: Set<String> = []) {
        self.collapsedIDs = collapsedIDs
    }

    public func isCollapsed(_ id: String) -> Bool {
        collapsedIDs.contains(id)
    }

    public mutating func setCollapsed(_ id: String, _ collapsed: Bool) {
        if collapsed {
            collapsedIDs.insert(id)
        } else {
            collapsedIDs.remove(id)
        }
    }

    public mutating func toggle(_ id: String) {
        setCollapsed(id, !isCollapsed(id))
    }
}

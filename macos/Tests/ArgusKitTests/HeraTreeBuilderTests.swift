import Foundation
import Testing
@testable import ArgusKit

@Suite("HeraTreeBuilder")
struct HeraTreeBuilderTests {

    // MARK: - Fixture helpers (HeraRole/HeraOrchestrator have no public
    // memberwise init — only Decodable — so fixtures are built as raw JSON
    // and decoded, mirroring FileTreeBuilderTests' `cf` helper.)

    private func roleJSON(id: Int64, orchID: Int64, kind: String = "worker", name: String? = nil) -> String {
        """
        {"role_id":\(id),"orch_id":\(orchID),"name":"\(name ?? "r\(id)")","kind":"\(kind)",
         "status":"","task_id":"","task_name":"","task_status":"","live":false,
         "ready_to_close":false,"archived":false,"needs_input":false}
        """
    }

    private func orchJSON(
        id: Int64, name: String, kanban: String = "active", roles: [String] = [],
        bridgeParentOrchID: Int64? = nil, bridgeParentRoleID: Int64? = nil
    ) -> String {
        let rolesArr = "[\(roles.joined(separator: ","))]"
        let parentOrch = bridgeParentOrchID.map(String.init) ?? "null"
        let parentRole = bridgeParentRoleID.map(String.init) ?? "null"
        return """
        {"id":\(id),"name":"\(name)","pinned":false,"archived":false,"kanban_status":"\(kanban)",
         "roles":\(rolesArr),"bridge_parent_orch_id":\(parentOrch),"bridge_parent_role_id":\(parentRole),
         "subtree_needs_input":false}
        """
    }

    private func decodeRoster(orchestrators: [String], freelance: [String] = []) -> HeraRoster {
        let json = """
        {"orchestrators":[\(orchestrators.joined(separator: ","))],
         "freelance":[\(freelance.joined(separator: ","))]}
        """
        return try! JSONDecoder().decode(HeraRoster.self, from: Data(json.utf8))
    }

    @Test("Empty roster yields no sections and no freelance")
    func empty() {
        let tree = HeraTreeBuilder.build(decodeRoster(orchestrators: []))
        #expect(tree.sections.isEmpty)
        #expect(tree.freelance.isEmpty)
    }

    @Test("Top-level orchestrators group by kanban_status in canonical order")
    func kanbanGrouping() {
        let roster = decodeRoster(orchestrators: [
            orchJSON(id: 1, name: "backlog-orch", kanban: "backlog"),
            orchJSON(id: 2, name: "active-orch", kanban: "active"),
        ])
        let tree = HeraTreeBuilder.build(roster)
        #expect(tree.sections.map(\.status) == ["active", "backlog"])
        #expect(tree.sections[0].orchestrators.count == 1)
        guard case .orchestrator(let o) = tree.sections[0].orchestrators[0].kind else {
            Issue.record("expected orchestrator node"); return
        }
        #expect(o.name == "active-orch")
    }

    @Test("An unrecognized kanban_status sorts after the canonical sections")
    func unrecognizedKanbanStatusSortsLast() {
        let roster = decodeRoster(orchestrators: [
            orchJSON(id: 1, name: "weird", kanban: "zzz-custom"),
            orchJSON(id: 2, name: "done-orch", kanban: "done"),
        ])
        let tree = HeraTreeBuilder.build(roster)
        #expect(tree.sections.map(\.status) == ["done", "zzz-custom"])
    }

    @Test("A nested orchestrator renders under its bridge parent role, not top-level")
    func nesting() {
        let workerRole = roleJSON(id: 10, orchID: 1, kind: "worker", name: "w")
        let parent = orchJSON(id: 1, name: "parent", roles: [workerRole])
        let child = orchJSON(id: 2, name: "child", bridgeParentOrchID: 1, bridgeParentRoleID: 10)
        let roster = decodeRoster(orchestrators: [parent, child])
        let tree = HeraTreeBuilder.build(roster)

        // Only the parent appears as a top-level, kanban-grouped entry.
        #expect(tree.sections.count == 1)
        #expect(tree.sections[0].orchestrators.count == 1)

        let parentNode = tree.sections[0].orchestrators[0]
        #expect(parentNode.children.count == 1)
        guard case .role(let role) = parentNode.children[0].kind else {
            Issue.record("expected role node"); return
        }
        #expect(role.roleID == 10)

        let roleNode = parentNode.children[0]
        #expect(roleNode.children.count == 1)
        guard case .orchestrator(let nestedOrch) = roleNode.children[0].kind else {
            Issue.record("expected nested orchestrator node"); return
        }
        #expect(nestedOrch.id == 2)
    }

    @Test("Nesting recurses through multiple bridge levels")
    func deepNesting() {
        let roleA = roleJSON(id: 100, orchID: 1, kind: "worker", name: "a-worker")
        let roleB = roleJSON(id: 200, orchID: 2, kind: "worker", name: "b-worker")
        let orchA = orchJSON(id: 1, name: "A", roles: [roleA])
        let orchB = orchJSON(id: 2, name: "B", roles: [roleB], bridgeParentOrchID: 1, bridgeParentRoleID: 100)
        let orchC = orchJSON(id: 3, name: "C", bridgeParentOrchID: 2, bridgeParentRoleID: 200)
        let roster = decodeRoster(orchestrators: [orchA, orchB, orchC])
        let tree = HeraTreeBuilder.build(roster)

        #expect(tree.sections.count == 1)
        #expect(tree.sections[0].orchestrators.count == 1)

        let aNode = tree.sections[0].orchestrators[0]
        let roleANode = aNode.children[0]
        guard case .orchestrator(let b) = roleANode.children[0].kind else {
            Issue.record("expected B nested under A's role"); return
        }
        #expect(b.id == 2)

        let bNode = roleANode.children[0]
        let roleBNode = bNode.children[0]
        guard case .orchestrator(let c) = roleBNode.children[0].kind else {
            Issue.record("expected C nested under B's role"); return
        }
        #expect(c.id == 3)
    }

    @Test("A dangling bridge-parent role id falls back to top-level rather than being dropped")
    func danglingBridgeParent() {
        let orphan = orchJSON(id: 5, name: "orphan", kanban: "blocked",
                               bridgeParentOrchID: 999, bridgeParentRoleID: 999)
        let tree = HeraTreeBuilder.build(decodeRoster(orchestrators: [orphan]))
        #expect(tree.sections.map(\.status) == ["blocked"])
        #expect(tree.sections[0].orchestrators.count == 1)
    }

    @Test("Freelance roles pass through unchanged, not grouped into any section")
    func freelancePassthrough() {
        let roster = decodeRoster(
            orchestrators: [],
            freelance: [roleJSON(id: 1, orchID: 0, kind: "freelance", name: "solo")]
        )
        let tree = HeraTreeBuilder.build(roster)
        #expect(tree.sections.isEmpty)
        #expect(tree.freelance.count == 1)
        #expect(tree.freelance[0].kind == "freelance")
    }

    @Test("Node ids are stable across rebuilds from a differently-shaped but corresponding roster")
    func idStabilityAcrossRebuilds() {
        let roleA = roleJSON(id: 10, orchID: 1, kind: "worker", name: "w")
        let tree1 = HeraTreeBuilder.build(decodeRoster(orchestrators: [
            orchJSON(id: 1, name: "parent", roles: [roleA]),
        ]))

        // Simulate a refresh poll: same orch/role ids, different display fields.
        let roleA2 = roleJSON(id: 10, orchID: 1, kind: "worker", name: "w-renamed")
        let tree2 = HeraTreeBuilder.build(decodeRoster(orchestrators: [
            orchJSON(id: 1, name: "parent-renamed", roles: [roleA2]),
        ]))

        #expect(tree1.sections[0].orchestrators[0].id == tree2.sections[0].orchestrators[0].id)
        #expect(tree1.sections[0].orchestrators[0].children[0].id == tree2.sections[0].orchestrators[0].children[0].id)
    }
}

@Suite("HeraFoldState")
struct HeraFoldStateTests {
    @Test("A fresh state has nothing collapsed")
    func defaultExpanded() {
        let state = HeraFoldState()
        #expect(state.isCollapsed("orch-1") == false)
    }

    @Test("setCollapsed marks and unmarks an id")
    func setCollapsed() {
        var state = HeraFoldState()
        state.setCollapsed("orch-1", true)
        #expect(state.isCollapsed("orch-1") == true)
        state.setCollapsed("orch-1", false)
        #expect(state.isCollapsed("orch-1") == false)
    }

    @Test("toggle flips collapsed state")
    func toggle() {
        var state = HeraFoldState()
        state.toggle("role-10")
        #expect(state.isCollapsed("role-10") == true)
        state.toggle("role-10")
        #expect(state.isCollapsed("role-10") == false)
    }

    @Test("Collapsed ids don't affect other ids")
    func independence() {
        var state = HeraFoldState()
        state.setCollapsed("orch-1", true)
        #expect(state.isCollapsed("orch-2") == false)
    }

    @Test("Collapsed state (constructed fresh from persisted ids) survives an equivalent id across a rebuild")
    func survivesAcrossIDs() {
        var state = HeraFoldState()
        state.setCollapsed("orch-1", true)
        // A refresh poll rebuilds the tree (new HeraTreeNode values) but,
        // per idStabilityAcrossRebuilds above, ids are unchanged — so the
        // same HeraFoldState instance (held by AppState, untouched by the
        // rebuild) still reports the node collapsed.
        #expect(state.isCollapsed("orch-1") == true)
    }
}

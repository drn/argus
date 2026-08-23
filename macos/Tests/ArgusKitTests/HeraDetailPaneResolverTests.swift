import Foundation
import Testing
@testable import ArgusKit

@Suite("HeraDetailPaneResolver")
struct HeraDetailPaneResolverTests {

    // MARK: - Fixture helpers (HeraRole/HeraOrchestrator have no public
    // memberwise init — only Decodable — so fixtures are built as raw JSON
    // and decoded, mirroring HeraTreeBuilderTests' convention.)

    private func roleJSON(
        id: Int64, orchID: Int64, kind: String = "worker", name: String? = nil,
        live: Bool = false, taskID: String = ""
    ) -> String {
        """
        {"role_id":\(id),"orch_id":\(orchID),"name":"\(name ?? "r\(id)")","kind":"\(kind)",
         "status":"","task_id":"\(taskID)","task_name":"","task_status":"","live":\(live),
         "ready_to_close":false,"archived":false,"needs_input":false}
        """
    }

    private func orchJSON(id: Int64, name: String, roles: [String] = []) -> String {
        let rolesArr = "[\(roles.joined(separator: ","))]"
        return """
        {"id":\(id),"name":"\(name)","pinned":false,"archived":false,"kanban_status":"active",
         "roles":\(rolesArr),"bridge_parent_orch_id":null,"bridge_parent_role_id":null,
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

    @Test("Nothing selected yields no left task and no right pane")
    func nothingSelected() {
        let roster = decodeRoster(orchestrators: [])
        let panes = HeraDetailPaneResolver.resolve(roster: roster, selectedRoleID: nil)
        #expect(panes.leftTaskID == nil)
        #expect(panes.right == .none)
    }

    @Test("Selecting a worker shows the coordinator's task on the left and the worker's own task on the right")
    func workerSelected() {
        let coord = roleJSON(id: 1, orchID: 10, kind: "coordinator", live: true, taskID: "coord-task")
        let worker = roleJSON(id: 2, orchID: 10, kind: "worker", live: true, taskID: "worker-task")
        let roster = decodeRoster(orchestrators: [orchJSON(id: 10, name: "orch", roles: [coord, worker])])

        let panes = HeraDetailPaneResolver.resolve(roster: roster, selectedRoleID: 2)
        #expect(panes.leftTaskID == "coord-task")
        #expect(panes.right == .terminal(taskID: "worker-task"))
    }

    @Test("Selecting a freelance role shows its owning orchestrator's coordinator on the left and its own task on the right")
    func freelanceSelected() {
        let coord = roleJSON(id: 1, orchID: 10, kind: "coordinator", live: true, taskID: "coord-task")
        let freelance = roleJSON(id: 3, orchID: 10, kind: "freelance", live: true, taskID: "freelance-task")
        let roster = decodeRoster(
            orchestrators: [orchJSON(id: 10, name: "orch", roles: [coord])],
            freelance: [freelance]
        )

        let panes = HeraDetailPaneResolver.resolve(roster: roster, selectedRoleID: 3)
        #expect(panes.leftTaskID == "coord-task")
        #expect(panes.right == .terminal(taskID: "freelance-task"))
    }

    @Test("Selecting a coordinator shows a roster region on the right instead of a terminal")
    func coordinatorSelected() {
        let coord = roleJSON(id: 1, orchID: 10, kind: "coordinator", live: true, taskID: "coord-task")
        let worker = roleJSON(id: 2, orchID: 10, kind: "worker", live: true, taskID: "worker-task")
        let roster = decodeRoster(orchestrators: [orchJSON(id: 10, name: "orch", roles: [coord, worker])])

        let panes = HeraDetailPaneResolver.resolve(roster: roster, selectedRoleID: 1)
        // The active orchestrator IS the selected coordinator's own orchestrator,
        // so the left pane still shows its (own) live task.
        #expect(panes.leftTaskID == "coord-task")
        #expect(panes.right == .roster(orchID: 10))
    }

    @Test("A selected role id absent from the roster degrades to no panes rather than crashing")
    func selectedRoleVanished() {
        let coord = roleJSON(id: 1, orchID: 10, kind: "coordinator", live: true, taskID: "coord-task")
        let roster = decodeRoster(orchestrators: [orchJSON(id: 10, name: "orch", roles: [coord])])

        let panes = HeraDetailPaneResolver.resolve(roster: roster, selectedRoleID: 999)
        #expect(panes.leftTaskID == nil)
        #expect(panes.right == .none)
    }

    @Test("An unbound (never-live) worker resolves to no right pane rather than a dangling task id")
    func unboundWorkerSelected() {
        let coord = roleJSON(id: 1, orchID: 10, kind: "coordinator", live: true, taskID: "coord-task")
        let worker = roleJSON(id: 2, orchID: 10, kind: "worker", live: false)
        let roster = decodeRoster(orchestrators: [orchJSON(id: 10, name: "orch", roles: [coord, worker])])

        let panes = HeraDetailPaneResolver.resolve(roster: roster, selectedRoleID: 2)
        #expect(panes.leftTaskID == "coord-task")
        #expect(panes.right == .none)
    }

    @Test("An unbound coordinator still routes the right pane to its roster, only the left pane degrades")
    func unboundCoordinatorSelected() {
        let coord = roleJSON(id: 1, orchID: 10, kind: "coordinator", live: false)
        let worker = roleJSON(id: 2, orchID: 10, kind: "worker", live: true, taskID: "worker-task")
        let roster = decodeRoster(orchestrators: [orchJSON(id: 10, name: "orch", roles: [coord, worker])])

        let panes = HeraDetailPaneResolver.resolve(roster: roster, selectedRoleID: 1)
        #expect(panes.leftTaskID == nil)
        #expect(panes.right == .roster(orchID: 10))
    }

    @Test("A worker whose orchestrator has no coordinator role degrades the left pane gracefully")
    func noCoordinatorInOrchestrator() {
        let worker = roleJSON(id: 2, orchID: 10, kind: "worker", live: true, taskID: "worker-task")
        let roster = decodeRoster(orchestrators: [orchJSON(id: 10, name: "orch", roles: [worker])])

        let panes = HeraDetailPaneResolver.resolve(roster: roster, selectedRoleID: 2)
        #expect(panes.leftTaskID == nil)
        #expect(panes.right == .terminal(taskID: "worker-task"))
    }
}

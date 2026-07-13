import Foundation

// Client methods for the eight Hera mutation REST endpoints
// (add-hera-mutation-rest-api). Every call acts as {orch_id}'s live
// coordinator, resolved server-side — none of these methods take a
// sender/actor parameter.
extension ArgusClient {
    /// `POST /api/hera/orchestrators/{orch_id}/workers` — the REST
    /// equivalent of `hera_spawn_worker`.
    public func spawnHeraWorker(orchID: Int64, _ req: HeraSpawnWorkerRequest) async throws -> HeraSpawnWorkerResponse {
        try await sendDecoding("POST", "/api/hera/orchestrators/\(orchID)/workers", body: req)
    }

    /// `POST /api/hera/orchestrators/{orch_id}/messages` — the REST
    /// equivalent of `hera_send`, narrowed to coordinator-as-sender only.
    public func sendHeraMessage(orchID: Int64, _ req: HeraSendMessageRequest) async throws -> HeraSendMessageResponse {
        try await sendDecoding("POST", "/api/hera/orchestrators/\(orchID)/messages", body: req)
    }

    /// `POST /api/hera/orchestrators/{orch_id}/plan/nodes` — the REST
    /// equivalent of `hera_plan_node`.
    public func createHeraPlanNode(orchID: Int64, _ req: HeraPlanNodeCreateRequest) async throws -> HeraPlanNodeResponse {
        try await sendDecoding("POST", "/api/hera/orchestrators/\(orchID)/plan/nodes", body: req)
    }

    /// `POST /api/hera/orchestrators/{orch_id}/plan` — the whole-graph
    /// endpoint, the REST equivalent of `hera_plan`.
    public func createHeraPlan(orchID: Int64, _ req: HeraPlanCreateRequest) async throws -> HeraPlanCreateResponse {
        try await sendDecoding("POST", "/api/hera/orchestrators/\(orchID)/plan", body: req)
    }

    /// `PATCH /api/hera/orchestrators/{orch_id}/plan/nodes/{role_id}` — the
    /// REST equivalent of `hera_plan_node_update`.
    public func updateHeraPlanNode(orchID: Int64, roleID: Int64,
                                    _ req: HeraPlanNodeUpdateRequest) async throws -> HeraPlanNodeStatusResponse {
        try await sendDecoding("PATCH", "/api/hera/orchestrators/\(orchID)/plan/nodes/\(roleID)", body: req)
    }

    /// `POST /api/hera/orchestrators/{orch_id}/plan/nodes/{role_id}/cancel`
    /// — the REST equivalent of `hera_plan_node_cancel`.
    public func cancelHeraPlanNode(orchID: Int64, roleID: Int64) async throws -> HeraPlanNodeStatusResponse {
        try await sendDecoding("POST", "/api/hera/orchestrators/\(orchID)/plan/nodes/\(roleID)/cancel")
    }

    /// `POST /api/hera/orchestrators/{orch_id}/plan/blocks` — the REST
    /// equivalent of `hera_block`. Both endpoints are `role_id`s.
    public func addHeraBlock(orchID: Int64, _ req: HeraBlockCreateRequest) async throws -> HeraBlockResponse {
        try await sendDecoding("POST", "/api/hera/orchestrators/\(orchID)/plan/blocks", body: req)
    }

    /// `DELETE /api/hera/orchestrators/{orch_id}/plan/blocks` — the REST
    /// equivalent of `hera_unblock`. Idempotent: removing a non-existent
    /// edge succeeds.
    public func removeHeraBlock(orchID: Int64, blockedRoleID: Int64, blockerRoleID: Int64) async throws -> HeraBlockResponse {
        try await sendDecoding("DELETE", "/api/hera/orchestrators/\(orchID)/plan/blocks", query: [
            URLQueryItem(name: "blocked_role_id", value: String(blockedRoleID)),
            URLQueryItem(name: "blocker_role_id", value: String(blockerRoleID)),
        ])
    }
}

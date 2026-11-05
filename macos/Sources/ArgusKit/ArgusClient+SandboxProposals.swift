import Foundation

extension ArgusClient {
    private struct SandboxProposalsEnvelope: Decodable { let proposals: [SandboxProposal] }

    /// `GET /api/sandbox/proposals?status=…` — pass nil to list every status.
    public func sandboxProposals(status: String? = "pending") async throws -> [SandboxProposal] {
        var q: [URLQueryItem] = []
        if let status, !status.isEmpty { q.append(.init(name: "status", value: status)) }
        let env: SandboxProposalsEnvelope = try await getDecoding("/api/sandbox/proposals", query: q)
        return env.proposals
    }

    /// `POST /api/sandbox/proposals/{id}/approve` — master token only (403
    /// otherwise). 400 = invalid path/scope, 404 = unknown, 409 = already decided.
    public func approveSandboxProposal(id: String, _ req: SandboxApproveRequest) async throws -> SandboxApproveResult {
        try await sendDecoding("POST", "/api/sandbox/proposals/\(pc(id))/approve", body: req)
    }

    /// `POST /api/sandbox/proposals/{id}/reject`.
    @discardableResult
    public func rejectSandboxProposal(id: String) async throws -> SandboxProposal {
        try await sendDecoding("POST", "/api/sandbox/proposals/\(pc(id))/reject")
    }
}

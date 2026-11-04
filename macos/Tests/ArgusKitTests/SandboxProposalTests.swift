import Foundation
import Testing
@testable import ArgusKit

// Client tests join ClientRequestTests' serialized suite: MockURLProtocol is
// process-global state, so a separate suite would race it.
extension ClientRequestTests {
    private func sandboxClient() -> ArgusClient {
        ArgusClient(config: ArgusConfig(baseURL: URL(string: "http://127.0.0.1:7743")!, token: "t"),
                    session: MockURLProtocol.makeSession())
    }

    private var listJSON: String { #"""
    {"proposals":[
     {"id":"p1","task_id":"t1","project":"argus","entries":[{"path":"/a","reason":"r","warning":"risky"},{"path":"/b","reason":"x"}],
      "status":"pending","scope":"","applied_paths":[],"created_at":"2026-11-04T10:00:00.123456789Z"},
     {"id":"p2","entries":[{"path":"/c"}],"status":"approved","decided_at":"2026-11-04T11:00:00Z","decided_by":"me"}
    ]}
    """# }

    @Test("list decodes full and sparse proposals")
    func decode() async throws {
        MockURLProtocol.stubJSON(body: Data(listJSON.utf8))
        let ps = try await sandboxClient().sandboxProposals()
        #expect(MockURLProtocol.lastRequest?.httpMethod == "GET")
        #expect(MockURLProtocol.lastRequest?.url?.path == "/api/sandbox/proposals")
        #expect(MockURLProtocol.lastRequest?.url?.query == "status=pending")
        #expect(ps.count == 2)
        #expect(ps[0].entries[0].warning == "risky")
        #expect(ps[0].entries[1].warning == nil)
        #expect(ps[0].decidedAt == nil)
        #expect(ps[1].project == "")
        #expect(ps[1].taskID == "")
        #expect(ps[1].entries[0].reason == "")
        #expect(ps[1].decidedBy == "me")
    }

    @Test("approve as-is omits paths")
    func approveNoPaths() async throws {
        MockURLProtocol.stubJSON(body: Data(#"{"proposal":{"id":"p1"},"scope":"project","applied":["/a"],"already_granted":["/b"],"warnings":["w"],"takes_effect":"new_sessions"}"#.utf8))
        let r = try await sandboxClient().approveSandboxProposal(id: "p1", .init(scope: "project"))
        let req = MockURLProtocol.lastRequest
        #expect(req?.httpMethod == "POST")
        #expect(req?.url?.path == "/api/sandbox/proposals/p1/approve")
        let obj = try JSONSerialization.jsonObject(with: try #require(MockURLProtocol.lastBody)) as? [String: Any]
        #expect(obj?["scope"] as? String == "project")
        #expect(obj?["paths"] == nil)
        #expect(r.applied == ["/a"])
        #expect(r.alreadyGranted == ["/b"])
        #expect(r.warnings == ["w"])
        #expect(r.takesEffect == "new_sessions")
    }

    @Test("approve with edited paths sends them")
    func approveWithPaths() async throws {
        MockURLProtocol.stubJSON(body: Data(#"{"proposal":{"id":"p1"},"scope":"global"}"#.utf8))
        let r = try await sandboxClient().approveSandboxProposal(id: "p1", .init(scope: "global", paths: ["/x", "/y"]))
        let obj = try JSONSerialization.jsonObject(with: try #require(MockURLProtocol.lastBody)) as? [String: Any]
        #expect(obj?["paths"] as? [String] == ["/x", "/y"])
        #expect(r.applied.isEmpty)
    }

    @Test("reject posts to reject path")
    func reject() async throws {
        MockURLProtocol.stubJSON(body: Data(#"{"id":"p1","status":"rejected"}"#.utf8))
        let p = try await sandboxClient().rejectSandboxProposal(id: "p1")
        #expect(MockURLProtocol.lastRequest?.httpMethod == "POST")
        #expect(MockURLProtocol.lastRequest?.url?.path == "/api/sandbox/proposals/p1/reject")
        #expect(p.status == "rejected")
    }

    @Test("sandbox 403 and 409 map to friendly text")
    func errors() async throws {
        MockURLProtocol.stubJSON(status: 403, body: Data(#"{"error":"master token required"}"#.utf8))
        do {
            _ = try await sandboxClient().approveSandboxProposal(id: "p1", .init(scope: "global"))
            Issue.record("expected throw")
        } catch {
            #expect((error as? ArgusError)?.httpStatus == 403)
            #expect(SandboxProposalLogic.errorText(error).contains("Master token required"))
        }
        MockURLProtocol.stubJSON(status: 409, body: Data(#"{"error":"already decided"}"#.utf8))
        do {
            _ = try await sandboxClient().rejectSandboxProposal(id: "p1")
            Issue.record("expected throw")
        } catch {
            #expect(SandboxProposalLogic.errorText(error) == "Already decided: already decided")
        }
        MockURLProtocol.stubJSON(status: 400, body: Data(#"{"error":"bad path"}"#.utf8))
        do {
            _ = try await sandboxClient().approveSandboxProposal(id: "p1", .init(scope: "global"))
            Issue.record("expected throw")
        } catch {
            #expect(SandboxProposalLogic.errorText(error) == "bad path")
        }
    }

}

@Suite("Sandbox proposal logic")
struct SandboxProposalLogicTests {
    @Test("logic: default scope, normalize, validate, pathsToSend, summary")
    func logic() {
        let withProj = SandboxProposal(id: "a", project: "argus", entries: [.init(path: "/a"), .init(path: "/b")])
        let noProj = SandboxProposal(id: "b")
        #expect(SandboxProposalLogic.defaultScope(for: withProj) == "project")
        #expect(SandboxProposalLogic.defaultScope(for: noProj) == "global")
        #expect(SandboxProposalLogic.projectScopeAvailable(noProj) == false)
        #expect(SandboxProposalLogic.normalizePaths([" /a ", "", "/a", "/b"]) == ["/a", "/b"])
        #expect(SandboxProposalLogic.validationHint("rel/x") != nil)
        #expect(SandboxProposalLogic.validationHint("/a/../b") != nil)
        #expect(SandboxProposalLogic.validationHint("/") != nil)
        #expect(SandboxProposalLogic.validationHint("~/x") == nil)
        #expect(SandboxProposalLogic.validationHint("/ok") == nil)
        #expect(SandboxProposalLogic.blockingProblem(paths: ["", " "]) != nil)
        #expect(SandboxProposalLogic.blockingProblem(paths: ["/ok", "bad"])?.hasPrefix("bad") == true)
        #expect(SandboxProposalLogic.blockingProblem(paths: ["/ok"]) == nil)
        #expect(SandboxProposalLogic.pathsToSend(edited: ["/a", " /b "], for: withProj) == nil)
        #expect(SandboxProposalLogic.pathsToSend(edited: ["/a"], for: withProj) == ["/a"])
    }

    @Test("logic: result summary lists everything")
    func summary() throws {
        let data = Data(#"{"proposal":{"id":"p"},"scope":"project","applied":["/a"],"already_granted":["/b"],"warnings":["careful"],"takes_effect":"new_sessions"}"#.utf8)
        let r = try JSONDecoder().decode(SandboxApproveResult.self, from: data)
        let lines = SandboxProposalLogic.resultSummary(r)
        #expect(lines.contains("Applied: /a"))
        #expect(lines.contains("Already granted: /b"))
        #expect(lines.contains("Warning: careful"))
        #expect(lines.last == SandboxProposalLogic.appliesNote)
    }
}

import Foundation
import Testing
@testable import ArgusKit

// Wire-decoding coverage for the eight Hera mutation REST endpoints
// (add-hera-mutation-rest-api). Shapes mirror internal/api/hera_mutations.go
// verbatim. Request-building tests (which exercise MockURLProtocol, a global
// mutable singleton) live in ClientRequestTests.swift instead of a sibling
// suite here — see the comment there for why.

@Suite("Hera mutation model decoding")
struct HeraMutationModelDecodeTests {
    let decoder = JSONDecoder()

    private func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
        try decoder.decode(T.self, from: Data(json.utf8))
    }

    @Test("HeraSpawnWorkerResponse decodes")
    func spawnWorkerResponse() throws {
        let r = try decode(HeraSpawnWorkerResponse.self, """
        {"role_id":42,"orch_id":1,"name":"parser-work","kind":"worker","project":"argus",
         "argus_task_id":"t1","task_name":"parser-work","task_status":"in_progress"}
        """)
        #expect(r.roleID == 42)
        #expect(r.orchID == 1)
        #expect(r.kind == "worker")
        #expect(r.argusTaskID == "t1")
    }

    @Test("HeraSendMessageResponse decodes")
    func sendMessageResponse() throws {
        let r = try decode(HeraSendMessageResponse.self,
                            #"{"message_id":7,"to_role_id":3,"delivery_mode":"idle_submit"}"#)
        #expect(r.messageID == 7)
        #expect(r.toRoleID == 3)
        #expect(r.deliveryMode == "idle_submit")
    }

    @Test("HeraPlanNodeResponse decodes")
    func planNodeResponse() throws {
        let r = try decode(HeraPlanNodeResponse.self,
                            #"{"role_id":5,"name":"1a-writer","project":"argus","kind":"worker","status":"planned"}"#)
        #expect(r.roleID == 5)
        #expect(r.status == "planned")
    }

    @Test("HeraPlanCreateResponse decodes")
    func planCreateResponse() throws {
        let r = try decode(HeraPlanCreateResponse.self, #"{"nodes_created":3,"edges_created":2}"#)
        #expect(r.nodesCreated == 3)
        #expect(r.edgesCreated == 2)
    }

    @Test("HeraPlanNodeStatusResponse decodes (shared by update + cancel)")
    func planNodeStatusResponse() throws {
        let r = try decode(HeraPlanNodeStatusResponse.self, #"{"role_id":5,"status":"cancelled"}"#)
        #expect(r.roleID == 5)
        #expect(r.status == "cancelled")
    }

    @Test("HeraBlockResponse decodes (shared by add + remove)")
    func blockResponse() throws {
        let r = try decode(HeraBlockResponse.self, #"{"blocked_role_id":9,"blocker_role_id":8}"#)
        #expect(r.blockedRoleID == 9)
        #expect(r.blockerRoleID == 8)
    }
}

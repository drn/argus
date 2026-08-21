import Foundation
import Testing
@testable import ArgusKit

@Suite("ArgusClient request building", .serialized)
struct ClientRequestTests {
    let baseURL = URL(string: "http://127.0.0.1:7743")!

    init() { MockURLProtocol.reset() }

    private func makeClient(token: String = "secret-token") -> ArgusClient {
        ArgusClient(config: ArgusConfig(baseURL: baseURL, token: token),
                    session: MockURLProtocol.makeSession())
    }

    private func queryItems(_ req: URLRequest?) -> [String: String] {
        guard let url = req?.url,
              let comps = URLComponents(url: url, resolvingAgainstBaseURL: false) else { return [:] }
        return Dictionary(uniqueKeysWithValues: (comps.queryItems ?? []).map { ($0.name, $0.value ?? "") })
    }

    // MARK: - Auth + URL + method

    @Test("GET carries the bearer token and hits the right path")
    func tasksRequest() async throws {
        MockURLProtocol.stubJSON(body: Data(#"{"tasks":[]}"#.utf8))
        _ = try await makeClient().tasks()

        let req = MockURLProtocol.lastRequest
        #expect(req?.httpMethod == "GET")
        #expect(req?.url?.path == "/api/tasks")
        #expect(req?.value(forHTTPHeaderField: "Authorization") == "Bearer secret-token")
    }

    @Test("list filters become query items")
    func tasksFilters() async throws {
        MockURLProtocol.stubJSON(body: Data(#"{"tasks":[]}"#.utf8))
        _ = try await makeClient().tasks(status: "in_progress", project: "argus", archived: "all")

        let items = queryItems(MockURLProtocol.lastRequest)
        #expect(items["status"] == "in_progress")
        #expect(items["project"] == "argus")
        #expect(items["archived"] == "all")
    }

    // MARK: - Body encoding

    @Test("sendInput posts raw octet-stream bytes")
    func sendInputBody() async throws {
        MockURLProtocol.stubJSON(body: Data(#"{"status":"ok","bytes":5}"#.utf8))
        let payload = Data("hello".utf8)
        try await makeClient().sendInput(taskID: "t1", payload)

        let req = MockURLProtocol.lastRequest
        #expect(req?.httpMethod == "POST")
        #expect(req?.url?.path == "/api/tasks/t1/input")
        #expect(req?.value(forHTTPHeaderField: "Content-Type") == "application/octet-stream")
        #expect(MockURLProtocol.lastBody == payload)
    }

    @Test("resize posts JSON with rows and cols")
    func resizeBody() async throws {
        MockURLProtocol.stubJSON(body: Data(#"{"cols":80,"rows":24,"rerendered":false}"#.utf8))
        let result = try await makeClient().resize(taskID: "t1", rows: 24, cols: 80)
        #expect(result.cols == 80)
        #expect(result.rows == 24)
        #expect(result.rerendered == false)

        let req = MockURLProtocol.lastRequest
        #expect(req?.httpMethod == "POST")
        #expect(req?.url?.path == "/api/tasks/t1/resize")
        #expect(req?.value(forHTTPHeaderField: "Content-Type") == "application/json")

        let body = try #require(MockURLProtocol.lastBody)
        let obj = try JSONSerialization.jsonObject(with: body) as? [String: Any]
        #expect(obj?["rows"] as? Int == 24)
        #expect(obj?["cols"] as? Int == 80)
    }

    @Test("createTask omits empty backend/model")
    func createTaskBody() async throws {
        MockURLProtocol.stubJSON(status: 201,
                                 body: Data(#"{"id":"1","name":"n","status":"in_progress"}"#.utf8))
        let resp = try await makeClient().createTask(CreateTaskRequest(prompt: "do it", project: "argus"))
        #expect(resp.id == "1")

        let body = try #require(MockURLProtocol.lastBody)
        let obj = try JSONSerialization.jsonObject(with: body) as? [String: Any]
        #expect(obj?["project"] as? String == "argus")
        #expect(obj?["prompt"] as? String == "do it")
        #expect(obj?["backend"] == nil)
        #expect(obj?["model"] == nil)
    }

    @Test("renameTask posts the name")
    func renameBody() async throws {
        MockURLProtocol.stubJSON(body: Data(#"{"name":"renamed"}"#.utf8))
        try await makeClient().renameTask(id: "t1", name: "renamed")

        #expect(MockURLProtocol.lastRequest?.url?.path == "/api/tasks/t1/rename")
        let obj = try JSONSerialization.jsonObject(with: #require(MockURLProtocol.lastBody)) as? [String: Any]
        #expect(obj?["name"] as? String == "renamed")
    }

    @Test("deleteTask uses DELETE")
    func deleteMethod() async throws {
        MockURLProtocol.stubJSON(body: Data(#"{"status":"deleted"}"#.utf8))
        try await makeClient().deleteTask(id: "t1")
        #expect(MockURLProtocol.lastRequest?.httpMethod == "DELETE")
        #expect(MockURLProtocol.lastRequest?.url?.path == "/api/tasks/t1")
    }

    @Test("putTaskMeta sends namespace + entries and returns written count")
    func putMetaBody() async throws {
        MockURLProtocol.stubJSON(body: Data(#"{"written":2}"#.utf8))
        let n = try await makeClient().putTaskMeta(taskID: "t1",
                                                   .batch(namespace: "app", entries: ["a": "1", "b": "2"]))
        #expect(n == 2)
        #expect(MockURLProtocol.lastRequest?.httpMethod == "PUT")
        let obj = try JSONSerialization.jsonObject(with: #require(MockURLProtocol.lastBody)) as? [String: Any]
        #expect(obj?["namespace"] as? String == "app")
        #expect((obj?["entries"] as? [String: Any])?.count == 2)
    }

    // MARK: - Output headers

    @Test("output parses X-Output-Total and X-Source headers")
    func outputHeaders() async throws {
        MockURLProtocol.handler = { req in
            let resp = HTTPURLResponse(url: req.url!, statusCode: 200, httpVersion: "HTTP/1.1",
                                       headerFields: ["X-Output-Total": "4096", "X-Source": "log",
                                                      "Content-Type": "text/plain"])!
            return (resp, Data("terminal bytes".utf8))
        }
        let out = try await makeClient().output(taskID: "t1", tailBytes: 1024, clean: true)
        #expect(out.total == 4096)
        #expect(out.source == "log")
        #expect(String(decoding: out.data, as: UTF8.self) == "terminal bytes")

        let items = queryItems(MockURLProtocol.lastRequest)
        #expect(items["bytes"] == "1024")
        #expect(items["clean"] == "1")
    }

    // MARK: - Raw task pin round-trip (add-mac-keybinding-parity Stage 1 red:
    // `ArgusClient.setPinned` does not exist yet)
    //
    // ArgusKit's lossy `Task` model (`Models+Task.swift`) doesn't decode
    // several raw-only `model.Task` fields (e.g. `session_id`, `result`), so
    // a naive decode-into-`Task`-then-reencode would silently drop them on
    // every pin toggle. `setPinned` must instead GET `/api/tasks/{id}/raw`
    // as a generic `JSONValue` object, flip only the `pinned` key, and PUT
    // the same object back — proving the round trip needs no knowledge of
    // the fields it never touches. Per `internal/api/raw.go`, the server
    // re-pins `worktree`/`branch`/`base_branch` server-side regardless of
    // what's sent, so this suite doesn't assert on those three specially.

    private static let rawTaskFixtureKeys: Set<String> = [
        "id", "name", "status", "project", "branch", "prompt",
        "worktree", "session_id", "pinned", "result", "created_at",
    ]

    /// `archived` present (with the given value) adds the `"archived"` key to
    /// the fixture; `nil` (the default) omits it entirely, matching a real
    /// `archived:false` row's `omitempty` wire behavior
    /// (`internal/model/task.go`'s `Archived bool \`json:"archived,omitempty"\`\`).
    private func rawTaskFixture(pinned: Bool, archived: Bool? = nil) -> Data {
        let archivedField = archived.map { #","archived":\#($0)"# } ?? ""
        return Data("""
        {"id":"t1","name":"task-one","status":"in_progress","project":"argus",
         "branch":"feature-x","prompt":"do the thing",
         "worktree":"/Users/x/.argus/worktrees/argus/task-one",
         "session_id":"sess-abc-123","pinned":\(pinned),"result":"some result blob",
         "created_at":"2026-08-01T00:00:00Z"\(archivedField)}
        """.utf8)
    }

    @Test("setPinned GETs the raw task before PUTing it back")
    func setPinnedFetchesBeforeWriting() async throws {
        let fixture = rawTaskFixture(pinned: false)
        MockURLProtocol.handler = { req in
            let resp = HTTPURLResponse(url: req.url!, statusCode: 200, httpVersion: "HTTP/1.1",
                                       headerFields: ["Content-Type": "application/json"])!
            return (resp, req.httpMethod == "GET" ? fixture : Data("{}".utf8))
        }

        try await makeClient().setPinned(id: "t1", pinned: true)

        let log = MockURLProtocol.requestLog
        #expect(log.count == 2)
        #expect(log[0].request.httpMethod == "GET")
        #expect(log[0].request.url?.path == "/api/tasks/t1/raw")
        #expect(log[1].request.httpMethod == "PUT")
        #expect(log[1].request.url?.path == "/api/tasks/t1/raw")
    }

    @Test("setPinned(pinned: true) flips pinned and preserves every other field, including fields ArgusKit's lossy Task model doesn't decode")
    func setPinnedTrueRoundTrip() async throws {
        let fixture = rawTaskFixture(pinned: false)
        MockURLProtocol.handler = { req in
            let resp = HTTPURLResponse(url: req.url!, statusCode: 200, httpVersion: "HTTP/1.1",
                                       headerFields: ["Content-Type": "application/json"])!
            return (resp, req.httpMethod == "GET" ? fixture : Data("{}".utf8))
        }

        try await makeClient().setPinned(id: "t1", pinned: true)

        let put = try #require(MockURLProtocol.requestLog.last)
        let body = try #require(put.body)
        let obj = try #require(JSONSerialization.jsonObject(with: body) as? [String: Any])

        // No field silently dropped, none silently added.
        #expect(Set(obj.keys) == Self.rawTaskFixtureKeys)

        // The one field setPinned is asked to change.
        #expect(obj["pinned"] as? Bool == true)

        // Every other field survives unchanged — including two fields the
        // lossy `Task` model has no property for at all (`session_id`,
        // `result`), which only round-trip if setPinned operates on the raw
        // JSONValue rather than decoding into `Task`.
        #expect(obj["id"] as? String == "t1")
        #expect(obj["name"] as? String == "task-one")
        #expect(obj["status"] as? String == "in_progress")
        #expect(obj["project"] as? String == "argus")
        #expect(obj["branch"] as? String == "feature-x")
        #expect(obj["prompt"] as? String == "do the thing")
        #expect(obj["session_id"] as? String == "sess-abc-123")
        #expect(obj["result"] as? String == "some result blob")
        #expect(obj["created_at"] as? String == "2026-08-01T00:00:00Z")
    }

    @Test("setPinned(pinned: false) flips an already-pinned task back off")
    func setPinnedFalseRoundTrip() async throws {
        let fixture = rawTaskFixture(pinned: true)
        MockURLProtocol.handler = { req in
            let resp = HTTPURLResponse(url: req.url!, statusCode: 200, httpVersion: "HTTP/1.1",
                                       headerFields: ["Content-Type": "application/json"])!
            return (resp, req.httpMethod == "GET" ? fixture : Data("{}".utf8))
        }

        try await makeClient().setPinned(id: "t1", pinned: false)

        let put = try #require(MockURLProtocol.requestLog.last)
        let body = try #require(put.body)
        let obj = try #require(JSONSerialization.jsonObject(with: body) as? [String: Any])
        #expect(obj["pinned"] as? Bool == false)
    }

    // MARK: - Pinning an already-archived task (mutual-exclusivity mirror)
    //
    // `handleUpdateTaskRaw` (`internal/api/raw.go`) writes through the plain
    // `db.Update`, NOT the dedicated `db.SetPinned`/`db.SetArchived` methods
    // that enforce "at most one of {Pinned, Archived} is true"
    // (`internal/model/task.go`'s `Task.SetPinned`/`SetArchived`) — it
    // persists whatever the PUT body says verbatim. `setPinned` must
    // therefore mirror that invariant itself when pinning: flip an
    // already-`true` `archived` key back to `false` in the object before
    // PUTting it back, so the round trip can never produce a
    // `(pinned: true, archived: true)` row.

    @Test("setPinned(pinned: true) on an already-archived task clears archived, still flips pinned, and preserves every other field")
    func setPinnedTrueClearsArchived() async throws {
        let fixture = rawTaskFixture(pinned: false, archived: true)
        MockURLProtocol.handler = { req in
            let resp = HTTPURLResponse(url: req.url!, statusCode: 200, httpVersion: "HTTP/1.1",
                                       headerFields: ["Content-Type": "application/json"])!
            return (resp, req.httpMethod == "GET" ? fixture : Data("{}".utf8))
        }

        try await makeClient().setPinned(id: "t1", pinned: true)

        let put = try #require(MockURLProtocol.requestLog.last)
        let body = try #require(put.body)
        let obj = try #require(JSONSerialization.jsonObject(with: body) as? [String: Any])

        // The mutual-exclusivity flip: archived goes to false even though
        // the fetched row had it true.
        #expect(obj["archived"] as? Bool == false)
        // The field setPinned is asked to change.
        #expect(obj["pinned"] as? Bool == true)

        // No field silently dropped, none silently added beyond the expected
        // "archived" key (present here, unlike the no-archived-key fixture
        // used by the other round-trip tests).
        #expect(Set(obj.keys) == Self.rawTaskFixtureKeys.union(["archived"]))
        #expect(obj["id"] as? String == "t1")
        #expect(obj["name"] as? String == "task-one")
        #expect(obj["status"] as? String == "in_progress")
        #expect(obj["project"] as? String == "argus")
        #expect(obj["branch"] as? String == "feature-x")
        #expect(obj["prompt"] as? String == "do the thing")
        #expect(obj["session_id"] as? String == "sess-abc-123")
        #expect(obj["result"] as? String == "some result blob")
        #expect(obj["created_at"] as? String == "2026-08-01T00:00:00Z")
    }

    @Test("setPinned(pinned: true) leaves an already-false archived key unchanged, not removed")
    func setPinnedTrueLeavesFalseArchivedAlone() async throws {
        let fixture = rawTaskFixture(pinned: false, archived: false)
        MockURLProtocol.handler = { req in
            let resp = HTTPURLResponse(url: req.url!, statusCode: 200, httpVersion: "HTTP/1.1",
                                       headerFields: ["Content-Type": "application/json"])!
            return (resp, req.httpMethod == "GET" ? fixture : Data("{}".utf8))
        }

        try await makeClient().setPinned(id: "t1", pinned: true)

        let put = try #require(MockURLProtocol.requestLog.last)
        let body = try #require(put.body)
        let obj = try #require(JSONSerialization.jsonObject(with: body) as? [String: Any])

        #expect(obj["archived"] as? Bool == false)
        #expect(obj["pinned"] as? Bool == true)
        #expect(Set(obj.keys) == Self.rawTaskFixtureKeys.union(["archived"]))
    }

    @Test("setPinned(pinned: true) never inserts an archived key when the fetched row omitted it entirely")
    func setPinnedTrueDoesNotInsertMissingArchived() async throws {
        // Same fixture the other round-trip tests use — no "archived" key at
        // all, mirroring a real archived:false row's omitempty wire shape.
        let fixture = rawTaskFixture(pinned: false)
        MockURLProtocol.handler = { req in
            let resp = HTTPURLResponse(url: req.url!, statusCode: 200, httpVersion: "HTTP/1.1",
                                       headerFields: ["Content-Type": "application/json"])!
            return (resp, req.httpMethod == "GET" ? fixture : Data("{}".utf8))
        }

        try await makeClient().setPinned(id: "t1", pinned: true)

        let put = try #require(MockURLProtocol.requestLog.last)
        let body = try #require(put.body)
        let obj = try #require(JSONSerialization.jsonObject(with: body) as? [String: Any])

        #expect(obj["archived"] == nil)
        #expect(Set(obj.keys) == Self.rawTaskFixtureKeys)
    }

    // MARK: - Maintenance (add-mac-keybinding-parity Stage 1 red:
    // `ArgusClient.pruneCompleted` does not exist yet)

    @Test("pruneCompleted POSTs to the maintenance endpoint and decodes the report")
    func pruneCompletedRequest() async throws {
        MockURLProtocol.stubJSON(
            body: Data(#"{"pruned":2,"worktrees":3,"orphans":1,"skippedHeraBound":0}"#.utf8))

        let report = try await makeClient().pruneCompleted()

        #expect(report.pruned == 2)
        #expect(report.worktrees == 3)
        #expect(report.orphans == 1)
        #expect(report.skippedHeraBound == 0)
        #expect(MockURLProtocol.lastRequest?.httpMethod == "POST")
        #expect(MockURLProtocol.lastRequest?.url?.path == "/api/maintenance/prune-completed")
    }

    // MARK: - Claude session switcher (add-mac-keybinding-parity Stage 6b)

    @Test("claudeSessions GETs the right path and decodes sessions + current id")
    func claudeSessionsRequest() async throws {
        MockURLProtocol.stubJSON(body: Data("""
        {"sessions":[
          {"id":"s1","title":"newest","branch":"argus/a","pr_ref":"o/r#1",
           "mod_time":"2026-08-21T10:00:00Z","size_bytes":100},
          {"id":"s2","title":"older","branch":"argus/b","pr_ref":"",
           "mod_time":"2026-08-20T10:00:00Z","size_bytes":200}
        ],"current_session_id":"s1"}
        """.utf8))

        let (sessions, currentID) = try await makeClient().claudeSessions(taskID: "t1")

        #expect(MockURLProtocol.lastRequest?.httpMethod == "GET")
        #expect(MockURLProtocol.lastRequest?.url?.path == "/api/tasks/t1/claude-sessions")
        #expect(sessions.count == 2)
        #expect(sessions[0].id == "s1")
        #expect(sessions[1].branch == "argus/b")
        #expect(currentID == "s1")
    }

    @Test("claudeSessions surfaces a 400 (non-Claude backend) as ArgusError.isBadRequest")
    func claudeSessionsBadRequest() async throws {
        MockURLProtocol.stubJSON(status: 400,
                                 body: Data(#"{"error":"session listing is Claude-only"}"#.utf8))
        let client = makeClient()
        do {
            _ = try await client.claudeSessions(taskID: "t1")
            Issue.record("expected throw")
        } catch let error as ArgusError {
            #expect(error.isBadRequest)
            #expect(!error.isNotFound)
        }
    }

    @Test("claudeSessions surfaces a 404 as ArgusError.isNotFound")
    func claudeSessionsNotFound() async throws {
        MockURLProtocol.stubJSON(status: 404, body: Data(#"{"error":"task not found"}"#.utf8))
        let client = makeClient()
        do {
            _ = try await client.claudeSessions(taskID: "nope")
            Issue.record("expected throw")
        } catch let error as ArgusError {
            #expect(error.isNotFound)
        }
    }

    @Test("switchClaudeSession POSTs the session_id body to the right path")
    func switchClaudeSessionRequest() async throws {
        MockURLProtocol.stubJSON(body: Data(#"{"status":"switched","pid":4242}"#.utf8))

        let (status, pid) = try await makeClient().switchClaudeSession(taskID: "t1", sessionID: "s2")

        #expect(MockURLProtocol.lastRequest?.httpMethod == "POST")
        #expect(MockURLProtocol.lastRequest?.url?.path == "/api/tasks/t1/claude-session")
        let body = try #require(MockURLProtocol.lastBody)
        let obj = try #require(JSONSerialization.jsonObject(with: body) as? [String: Any])
        #expect(obj["session_id"] as? String == "s2")
        #expect(status == "switched")
        #expect(pid == 4242)
    }

    @Test("switchClaudeSession decodes the unchanged no-op with a nil pid")
    func switchClaudeSessionUnchanged() async throws {
        MockURLProtocol.stubJSON(body: Data(#"{"status":"unchanged"}"#.utf8))

        let (status, pid) = try await makeClient().switchClaudeSession(taskID: "t1", sessionID: "s1")

        #expect(status == "unchanged")
        #expect(pid == nil)
    }

    @Test("switchClaudeSession surfaces a 500 as a generic ArgusError.http")
    func switchClaudeSessionServerError() async throws {
        MockURLProtocol.stubJSON(status: 500, body: Data(#"{"error":"failed to restart session"}"#.utf8))
        let client = makeClient()
        do {
            _ = try await client.switchClaudeSession(taskID: "t1", sessionID: "s2")
            Issue.record("expected throw")
        } catch let error as ArgusError {
            #expect(error.httpStatus == 500)
            #expect(!error.isBadRequest)
        }
    }

    // MARK: - Error mapping

    @Test("404 maps to ArgusError.http with isNotFound")
    func notFound() async throws {
        MockURLProtocol.stubJSON(status: 404, body: Data(#"{"error":"task not found"}"#.utf8))
        let client = makeClient()
        do {
            _ = try await client.task(id: "nope")
            Issue.record("expected throw")
        } catch let error as ArgusError {
            #expect(error.isNotFound)
            #expect(error == .http(status: 404, body: "task not found"))
        }
    }

    @Test("401 maps to ArgusError with isUnauthorized")
    func unauthorized() async throws {
        MockURLProtocol.stubJSON(status: 401, body: Data(#"{"error":"invalid token"}"#.utf8))
        let client = makeClient()
        do {
            _ = try await client.status()
            Issue.record("expected throw")
        } catch let error as ArgusError {
            #expect(error.isUnauthorized)
        }
    }

    // MARK: - Config resolution

    @Test("ArgusConfig.resolve reads a trimmed token from a file")
    func configResolveFromFile() throws {
        let dir = FileManager.default.temporaryDirectory
            .appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let tokenFile = dir.appendingPathComponent("api-token")
        try "  file-token-123\n".write(to: tokenFile, atomically: true, encoding: .utf8)
        defer { try? FileManager.default.removeItem(at: dir) }

        let cfg = try ArgusConfig.resolve(tokenFileURL: tokenFile)
        #expect(cfg.token == "file-token-123")
        #expect(cfg.baseURL == ArgusConfig.defaultBaseURL)
        #expect(!cfg.description.contains("file-token-123"))
    }

    @Test("ArgusConfig.resolve prefers an explicit token over the file")
    func configResolveExplicit() throws {
        let cfg = try ArgusConfig.resolve(token: "explicit",
                                          tokenFileURL: URL(fileURLWithPath: "/nonexistent"))
        #expect(cfg.token == "explicit")
    }

    @Test("ArgusConfig.resolve throws when no token is available")
    func configResolveMissing() {
        #expect(throws: ArgusError.self) {
            _ = try ArgusConfig.resolve(tokenFileURL: URL(fileURLWithPath: "/nonexistent/api-token"))
        }
    }

    // MARK: - SSE stream auth (regression: token must NEVER ride the URL)

    @Test("SSE stream authenticates via the Authorization header, never ?token=")
    func streamAuthUsesHeaderNotQuery() async throws {
        // A token in the URL leaks into NSError descriptions
        // (NSErrorFailingURLStringKey) and from there into logs — the stream
        // request must carry the same bearer header as every other call.
        MockURLProtocol.stubJSON(status: 200,
                                 headers: ["Content-Type": "text/event-stream"],
                                 body: Data("data: aGk=\n\n".utf8))
        let client = makeClient()
        for try await _ in client.terminalStream(taskID: "42", since: 7) { break }

        let req = MockURLProtocol.lastRequest
        #expect(req?.value(forHTTPHeaderField: "Authorization") == "Bearer secret-token")
        let items = queryItems(req)
        #expect(items["token"] == nil)
        #expect(items["since"] == "7")
        #expect(req?.url?.absoluteString.contains("secret-token") == false)
    }
}

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

import Foundation

/// A typed async/await HTTP + SSE client for the argus daemon's REST API.
///
/// `ArgusClient` is `Sendable` and free of any actor isolation — call it from
/// any task or actor. It mirrors the endpoint coverage of the Go
/// `internal/apiclient` package. Non-streaming calls authenticate with
/// `Authorization: Bearer <token>`; SSE streams use the `?token=` query param
/// (EventSource-style), matching `internal/api/auth.go`.
///
/// The token is never logged — see ``ArgusConfig``.
public struct ArgusClient: Sendable {
    public let config: ArgusConfig
    let session: URLSession

    /// Constructs a client. Pass a custom `URLSession` (e.g. one backed by a
    /// mock `URLProtocol`) for testing.
    public init(config: ArgusConfig, session: URLSession = .shared) {
        self.config = config
        self.session = session
    }

    /// Convenience: resolve config (default base URL, token from
    /// `~/.argus/api-token`) and construct a client.
    public init(baseURL: URL? = nil, token: String? = nil,
                session: URLSession = .shared) throws {
        self.init(config: try ArgusConfig.resolve(baseURL: baseURL, token: token),
                  session: session)
    }

    // MARK: - Shared JSON coders

    static let decoder = JSONDecoder()
    static let encoder = JSONEncoder()

    // MARK: - URL / request building

    func makeURL(path: String, query: [URLQueryItem] = []) throws -> URL {
        guard var comps = URLComponents(url: config.baseURL, resolvingAgainstBaseURL: false) else {
            throw ArgusError.invalidResponse("invalid base URL: \(config.baseURL.absoluteString)")
        }
        let base = comps.path == "/" ? "" : comps.path
        comps.path = base + path
        if !query.isEmpty {
            comps.queryItems = query
        }
        guard let url = comps.url else {
            throw ArgusError.invalidResponse("could not compose URL for \(path)")
        }
        return url
    }

    /// Percent-encodes a single path component (task ID, schedule ID, …) so a
    /// value containing `/`, `?`, or `#` cannot inject extra path segments or
    /// a query. IDs are server-issued opaque strings today, but as SDK surface
    /// this must not rely on caller discipline.
    static func pc(_ component: String) -> String {
        var allowed = CharacterSet.urlPathAllowed
        allowed.remove(charactersIn: "/?#")
        return component.addingPercentEncoding(withAllowedCharacters: allowed) ?? component
    }

    func pc(_ component: String) -> String { Self.pc(component) }

    func makeRequest(method: String, path: String, query: [URLQueryItem] = [],
                     body: Data? = nil, contentType: String? = nil) throws -> URLRequest {
        var req = URLRequest(url: try makeURL(path: path, query: query))
        req.httpMethod = method
        if !config.token.isEmpty {
            req.setValue("Bearer \(config.token)", forHTTPHeaderField: "Authorization")
        }
        if let contentType {
            req.setValue(contentType, forHTTPHeaderField: "Content-Type")
        }
        req.httpBody = body
        return req
    }

    // MARK: - Send / decode

    /// Issues a request, mapping non-2xx to ``ArgusError/http(status:body:)``
    /// and transport failures to ``ArgusError/transport(_:)``.
    @discardableResult
    func send(_ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        let data: Data
        let response: URLResponse
        do {
            (data, response) = try await session.data(for: request)
        } catch {
            throw ArgusError.transport(String(describing: error))
        }
        guard let http = response as? HTTPURLResponse else {
            throw ArgusError.invalidResponse("non-HTTP response")
        }
        guard (200..<300).contains(http.statusCode) else {
            throw ArgusError.http(status: http.statusCode, body: Self.errorMessage(from: data))
        }
        return (data, http)
    }

    func decode<T: Decodable>(_ type: T.Type, from data: Data, path: String) throws -> T {
        do {
            return try Self.decoder.decode(T.self, from: data)
        } catch {
            throw ArgusError.decoding("decode \(path): \(error)")
        }
    }

    func encodeBody<B: Encodable>(_ value: B, path: String) throws -> Data {
        do {
            return try Self.encoder.encode(value)
        } catch {
            throw ArgusError.decoding("encode \(path): \(error)")
        }
    }

    /// Extracts a `{"error":"…"}` message, falling back to the raw (truncated)
    /// body.
    static func errorMessage(from data: Data) -> String {
        if let env = try? decoder.decode([String: String].self, from: data),
           let msg = env["error"] {
            return msg
        }
        let raw = String(decoding: data, as: UTF8.self)
        return raw.count > 512 ? String(raw.prefix(512)) : raw
    }

    // MARK: - Verb helpers

    func getDecoding<T: Decodable>(_ path: String, query: [URLQueryItem] = []) async throws -> T {
        let req = try makeRequest(method: "GET", path: path, query: query)
        let (data, _) = try await send(req)
        return try decode(T.self, from: data, path: path)
    }

    func sendDecoding<T: Decodable>(_ method: String, _ path: String,
                                    query: [URLQueryItem] = []) async throws -> T {
        let req = try makeRequest(method: method, path: path, query: query)
        let (data, _) = try await send(req)
        return try decode(T.self, from: data, path: path)
    }

    func sendDecoding<T: Decodable, B: Encodable>(_ method: String, _ path: String,
                                                  body: B, query: [URLQueryItem] = []) async throws -> T {
        let data = try encodeBody(body, path: path)
        let req = try makeRequest(method: method, path: path, query: query,
                                  body: data, contentType: "application/json")
        let (respData, _) = try await send(req)
        return try decode(T.self, from: respData, path: path)
    }

    func sendVoid(_ method: String, _ path: String, query: [URLQueryItem] = []) async throws {
        let req = try makeRequest(method: method, path: path, query: query)
        _ = try await send(req)
    }

    func sendVoid<B: Encodable>(_ method: String, _ path: String, body: B,
                                query: [URLQueryItem] = []) async throws {
        let data = try encodeBody(body, path: path)
        let req = try makeRequest(method: method, path: path, query: query,
                                  body: data, contentType: "application/json")
        _ = try await send(req)
    }

    func mapError(_ error: Error) -> Error {
        if error is ArgusError { return error }
        return ArgusError.transport(String(describing: error))
    }

    // MARK: - Status / config / metrics

    /// `GET /api/status` — the daemon's at-a-glance counts.
    public func status() async throws -> DaemonStatus {
        try await getDecoding("/api/status")
    }

    /// `GET /api/config` — the daemon's full `config.Config` snapshot, as a
    /// type-erased ``JSONValue``.
    public func config() async throws -> JSONValue {
        try await getDecoding("/api/config")
    }

    /// `GET /api/system-metrics` — the cached host-load snapshot plus live
    /// session counts.
    public func systemMetrics() async throws -> SystemMetrics {
        try await getDecoding("/api/system-metrics")
    }

    /// `GET /api/sessions/state` — the runner's live running/idle task IDs.
    public func sessionState() async throws -> SessionState {
        try await getDecoding("/api/sessions/state")
    }
}

import Foundation

/// A parsed Server-Sent Event. `name` is the `event:` field (nil for the
/// default/unnamed event); `data` is the accumulated `data:` payload with
/// multi-line fields joined by `\n`.
public struct SSEvent: Sendable, Equatable {
    public var name: String?
    public var data: String

    public init(name: String? = nil, data: String) {
        self.name = name
        self.data = data
    }
}

/// A minimal, WHATWG-compliant Server-Sent-Events line parser.
///
/// Feed it lines (without trailing newlines). It accumulates `event:` and
/// `data:` fields, ignores `:`-prefixed comments (keepalive pings) and unknown
/// fields (`id:` / `retry:`), and dispatches an ``SSEvent`` on a blank line —
/// but only when a `data:` field was seen, matching the spec (a lone comment or
/// event name with no data dispatches nothing).
public struct SSEParser: Sendable {
    private var eventName: String?
    private var dataBuffer: [String] = []
    private var sawData = false

    public init() {}

    /// Feeds one line. Returns an event if this line was a dispatching blank
    /// line, else nil.
    public mutating func feed(_ rawLine: String) -> SSEvent? {
        var line = rawLine
        // Tolerate CRLF framing even though most line sequences strip it.
        if line.hasSuffix("\r") { line.removeLast() }

        if line.isEmpty {
            return dispatch()
        }
        if line.hasPrefix(":") {
            return nil // comment / keepalive
        }
        let (field, value) = Self.splitField(line)
        switch field {
        case "event":
            eventName = value
        case "data":
            dataBuffer.append(value)
            sawData = true
        default:
            break // id / retry / unknown — ignored
        }
        return nil
    }

    /// Flushes any buffered event at end-of-stream (in case the stream ends
    /// without a trailing blank line).
    public mutating func finish() -> SSEvent? { dispatch() }

    private mutating func dispatch() -> SSEvent? {
        defer {
            eventName = nil
            dataBuffer.removeAll(keepingCapacity: true)
            sawData = false
        }
        guard sawData else { return nil }
        let data = dataBuffer.joined(separator: "\n")
        let name = (eventName?.isEmpty == false) ? eventName : nil
        return SSEvent(name: name, data: data)
    }

    /// Splits `field: value` on the first colon, dropping a single leading
    /// space in the value. A line with no colon is a field with an empty value.
    static func splitField(_ line: String) -> (String, String) {
        guard let idx = line.firstIndex(of: ":") else {
            return (line, "")
        }
        let field = String(line[line.startIndex..<idx])
        var rest = line[line.index(after: idx)...]
        if rest.first == " " { rest = rest.dropFirst() }
        return (field, String(rest))
    }
}

/// A decoded terminal-stream event from ``ArgusClient/terminalStream(taskID:since:)``.
public enum TerminalEvent: Sendable, Equatable {
    /// Raw PTY bytes (an unnamed SSE event, base64-decoded).
    case output(Data)
    /// The `exit` event; `rerendering` is true when a kick-restart is in flight.
    case exit(rerendering: Bool)
    /// The `clipboard` event: either staged text or a clear.
    case clipboard(text: String?, cleared: Bool)
}

/// A decoded daemon event from ``ArgusClient/eventsStream(since:)`` — the
/// `event:` name (e.g. `task.status_changed`, `resync`) plus the raw JSON
/// payload bytes for the caller to decode per `internal/model/event.go`.
public struct ServerEvent: Sendable, Equatable {
    public let type: String
    public let jsonData: Data

    public init(type: String, jsonData: Data) {
        self.type = type
        self.jsonData = jsonData
    }
}

extension ArgusClient {
    /// Opens an SSE connection and yields parsed ``SSEvent`` values. Auth is via
    /// the `?token=` query param (EventSource-style), matching
    /// `internal/api/auth.go`. The underlying request has no client-side
    /// timeout, so the stream can sit idle between the server's 30s keepalive
    /// pings; cancel by breaking out of the `for await` (or cancelling the task).
    public func stream(path: String, query: [URLQueryItem] = []) -> AsyncThrowingStream<SSEvent, Error> {
        AsyncThrowingStream { continuation in
            let work = _Concurrency.Task {
                do {
                    var q = query
                    if !config.token.isEmpty {
                        q.append(URLQueryItem(name: "token", value: config.token))
                    }
                    var req = URLRequest(url: try makeURL(path: path, query: q))
                    req.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                    // Streaming request must not time out on idle keepalives.
                    req.timeoutInterval = TimeInterval.greatestFiniteMagnitude

                    let (bytes, response) = try await session.bytes(for: req)
                    guard let http = response as? HTTPURLResponse else {
                        throw ArgusError.invalidResponse("non-HTTP SSE response")
                    }
                    guard (200..<300).contains(http.statusCode) else {
                        throw ArgusError.http(status: http.statusCode, body: "SSE \(path)")
                    }
                    var parser = SSEParser()
                    for try await line in bytes.lines {
                        if let ev = parser.feed(line) {
                            continuation.yield(ev)
                        }
                    }
                    if let ev = parser.finish() {
                        continuation.yield(ev)
                    }
                    continuation.finish()
                } catch is CancellationError {
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: mapError(error))
                }
            }
            continuation.onTermination = { _ in work.cancel() }
        }
    }

    /// Streams a task's live PTY output via `GET /api/tasks/{id}/stream`. Pass
    /// the ``OutputTail/total`` from a prior ``output(taskID:tailBytes:clean:)``
    /// as `since` for a gapless resume. Unnamed events carry base64 PTY bytes;
    /// the `exit` event carries `{"rerendering":Bool}` and `clipboard` carries
    /// `{"text":…}` or `{"cleared":true}`.
    public func terminalStream(taskID: String, since: UInt64 = 0) -> AsyncThrowingStream<TerminalEvent, Error> {
        var q: [URLQueryItem] = []
        if since > 0 { q.append(.init(name: "since", value: String(since))) }
        let base = stream(path: "/api/tasks/\(taskID)/stream", query: q)
        return AsyncThrowingStream { continuation in
            let work = _Concurrency.Task {
                do {
                    for try await ev in base {
                        if let te = Self.mapTerminalEvent(ev) {
                            continuation.yield(te)
                        }
                    }
                    continuation.finish()
                } catch is CancellationError {
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in work.cancel() }
        }
    }

    /// Streams daemon events via `GET /api/events/stream`. Pass the last-seen
    /// event ID as `since` for replay; a `resync` event means history rotated
    /// out and the client should re-snapshot daemon state.
    public func eventsStream(since: Int64 = 0) -> AsyncThrowingStream<ServerEvent, Error> {
        var q: [URLQueryItem] = []
        if since > 0 { q.append(.init(name: "since", value: String(since))) }
        let base = stream(path: "/api/events/stream", query: q)
        return AsyncThrowingStream { continuation in
            let work = _Concurrency.Task {
                do {
                    for try await ev in base {
                        continuation.yield(ServerEvent(type: ev.name ?? "", jsonData: Data(ev.data.utf8)))
                    }
                    continuation.finish()
                } catch is CancellationError {
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in work.cancel() }
        }
    }

    /// Maps a raw ``SSEvent`` from the terminal stream into a ``TerminalEvent``.
    /// `nil` for events that don't decode (e.g. bad base64) so the stream skips
    /// them rather than failing.
    static func mapTerminalEvent(_ ev: SSEvent) -> TerminalEvent? {
        switch ev.name {
        case nil, "":
            guard let data = Data(base64Encoded: ev.data) else { return nil }
            return .output(data)
        case "exit":
            var rerender = false
            if let d = ev.data.data(using: .utf8),
               let obj = try? decoder.decode([String: Bool].self, from: d),
               let r = obj["rerendering"] {
                rerender = r
            }
            return .exit(rerendering: rerender)
        case "clipboard":
            struct Clip: Decodable { var text: String?; var cleared: Bool? }
            if let d = ev.data.data(using: .utf8),
               let c = try? decoder.decode(Clip.self, from: d) {
                if c.cleared == true { return .clipboard(text: nil, cleared: true) }
                return .clipboard(text: c.text, cleared: false)
            }
            return .clipboard(text: nil, cleared: true)
        default:
            return nil
        }
    }
}

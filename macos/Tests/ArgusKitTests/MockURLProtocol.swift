import Foundation

/// A `URLProtocol` that intercepts every request, records it (method, URL,
/// headers, body), and returns a canned response supplied via ``handler``.
///
/// URLSession converts a request's `httpBody` into an `httpBodyStream` before
/// handing it to a `URLProtocol`, so ``bodyData`` reads the stream to recover
/// the bytes the client actually sent.
final class MockURLProtocol: URLProtocol {
    /// Produces the (response, body) to return for a given request. Set before
    /// issuing the request under test.
    nonisolated(unsafe) static var handler: (@Sendable (URLRequest) throws -> (HTTPURLResponse, Data))?
    /// The most recent intercepted request.
    nonisolated(unsafe) static var lastRequest: URLRequest?
    /// The most recent intercepted request body (recovered from the body stream).
    nonisolated(unsafe) static var lastBody: Data?
    /// Every intercepted request in issue order, each paired with its
    /// recovered body. `lastRequest`/`lastBody` only ever reflect the most
    /// recent call, which can't distinguish "GET then PUT" ordering when a
    /// single client method issues more than one request (e.g. a
    /// read-modify-write round trip) — tests asserting that ordering read
    /// this instead.
    nonisolated(unsafe) static var requestLog: [(request: URLRequest, body: Data?)] = []

    static let lock = NSLock()

    static func reset() {
        lock.lock()
        handler = nil
        lastRequest = nil
        lastBody = nil
        requestLog = []
        lock.unlock()
    }

    /// Builds a `URLSession` wired to this protocol.
    static func makeSession() -> URLSession {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [MockURLProtocol.self]
        return URLSession(configuration: config)
    }

    /// Installs a JSON response handler that also records nothing extra — use
    /// with the static capture vars for assertions.
    static func stubJSON(status: Int = 200, headers: [String: String] = [:], body: Data) {
        lock.lock()
        handler = { req in
            var h = headers
            if h["Content-Type"] == nil { h["Content-Type"] = "application/json" }
            let resp = HTTPURLResponse(url: req.url!, statusCode: status,
                                       httpVersion: "HTTP/1.1", headerFields: h)!
            return (resp, body)
        }
        lock.unlock()
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        MockURLProtocol.lock.lock()
        MockURLProtocol.lastRequest = request
        MockURLProtocol.lastBody = MockURLProtocol.bodyData(request)
        MockURLProtocol.requestLog.append((request, MockURLProtocol.lastBody))
        let handler = MockURLProtocol.handler
        MockURLProtocol.lock.unlock()

        guard let handler else {
            client?.urlProtocol(self, didFailWithError: URLError(.badServerResponse))
            return
        }
        do {
            let (response, data) = try handler(request)
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }

    override func stopLoading() {}

    /// Recovers the request body from `httpBody` or the body stream.
    static func bodyData(_ request: URLRequest) -> Data? {
        if let b = request.httpBody { return b }
        guard let stream = request.httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }
        var data = Data()
        let bufSize = 4096
        let buffer = UnsafeMutablePointer<UInt8>.allocate(capacity: bufSize)
        defer { buffer.deallocate() }
        while stream.hasBytesAvailable {
            let read = stream.read(buffer, maxLength: bufSize)
            if read <= 0 { break }
            data.append(buffer, count: read)
        }
        return data
    }
}

import Foundation

/// Errors surfaced by ``ArgusClient``.
///
/// Underlying transport/decoding errors are captured as their string
/// description rather than the boxed `Error` so the enum stays `Sendable`
/// without an `@unchecked` escape hatch.
public enum ArgusError: Error, Sendable, Equatable {
    /// The request never produced an HTTP response (connection refused, DNS,
    /// TLS, cancellation surfaced as a URLError, etc). Payload is the
    /// underlying error's description.
    case transport(String)

    /// The server returned a non-2xx status. `body` is the server-supplied
    /// `{"error":"…"}` message when present, else the raw (truncated) body.
    case http(status: Int, body: String)

    /// The response body could not be decoded into the expected type. Payload
    /// is the decoding error's description.
    case decoding(String)

    /// The response was malformed at the transport layer (e.g. not HTTP, or a
    /// base URL that can't be composed into a request URL).
    case invalidResponse(String)

    /// No API token could be resolved. Payload is the token file path that was
    /// checked.
    case tokenUnavailable(String)

    /// True when the error is an HTTP 404 — mirrors `apiclient.IsNotFound`.
    public var isNotFound: Bool {
        if case let .http(status, _) = self { return status == 404 }
        return false
    }

    /// True when the error is an HTTP 401 — mirrors `apiclient.IsUnauthorized`.
    public var isUnauthorized: Bool {
        if case let .http(status, _) = self { return status == 401 }
        return false
    }

    /// The HTTP status code, when this is a ``http(status:body:)`` error.
    public var httpStatus: Int? {
        if case let .http(status, _) = self { return status }
        return nil
    }
}

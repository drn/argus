import Foundation

/// Connection settings for talking to an argus daemon.
///
/// `baseURL` defaults to the daemon's local REST port. `token` is the bearer
/// token used for `Authorization: Bearer` headers (and, for SSE, the `?token=`
/// query param). The token is **never** logged: ``description`` redacts it and
/// nothing in this package prints it.
public struct ArgusConfig: Sendable, Equatable, CustomStringConvertible {
    /// The API root, e.g. `http://127.0.0.1:7743`. No trailing slash required.
    public let baseURL: URL
    /// Bearer token. Treat as a secret — do not log or persist in plaintext.
    public let token: String

    /// The daemon's default local REST base URL (matches
    /// `apiclient.DefaultLocalBaseURL` and the daemon's `ListenAndServe` port).
    public static let defaultBaseURLString = "http://127.0.0.1:7743"

    /// The default base URL as a `URL`. Force-unwrapped: the literal is valid.
    public static var defaultBaseURL: URL {
        URL(string: defaultBaseURLString)!
    }

    /// The default token file: `~/.argus/api-token`.
    public static var defaultTokenFileURL: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".argus", isDirectory: true)
            .appendingPathComponent("api-token", isDirectory: false)
    }

    public init(baseURL: URL, token: String) {
        // Normalise: drop a trailing slash so path joins don't double up.
        if baseURL.absoluteString.hasSuffix("/"),
           let trimmed = URL(string: String(baseURL.absoluteString.dropLast())) {
            self.baseURL = trimmed
        } else {
            self.baseURL = baseURL
        }
        self.token = token
    }

    /// Resolves a config from an optional explicit base URL and token.
    ///
    /// Token resolution: an explicit non-empty `token` wins; otherwise the
    /// trimmed contents of `tokenFileURL` (defaulting to `~/.argus/api-token`)
    /// are used. Throws ``ArgusError/tokenUnavailable(_:)`` when no token can be
    /// found. Base URL falls back to ``defaultBaseURL``.
    public static func resolve(
        baseURL: URL? = nil,
        token: String? = nil,
        tokenFileURL: URL? = nil
    ) throws -> ArgusConfig {
        let url = baseURL ?? defaultBaseURL
        if let token, !token.isEmpty {
            return ArgusConfig(baseURL: url, token: token)
        }
        let fileURL = tokenFileURL ?? defaultTokenFileURL
        guard let contents = try? String(contentsOf: fileURL, encoding: .utf8) else {
            throw ArgusError.tokenUnavailable(fileURL.path)
        }
        let trimmed = contents.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else {
            throw ArgusError.tokenUnavailable(fileURL.path)
        }
        return ArgusConfig(baseURL: url, token: trimmed)
    }

    /// Redacts the token so config values are safe to log.
    public var description: String {
        "ArgusConfig(baseURL: \(baseURL.absoluteString), token: <redacted>)"
    }
}

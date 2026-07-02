import Foundation

/// Persisted connection overrides.
///
/// The server URL is stored in `UserDefaults` (non-secret). The token override
/// is stored in the macOS Keychain via ``Keychain`` (secret). Both are optional:
/// an empty / absent value means "use the ArgusKit default" (default local port,
/// token read from `~/.argus/api-token`). The token is never written to
/// UserDefaults, never logged, and never printed.
@MainActor
final class Preferences {
    private let defaults: UserDefaults
    private static let serverURLKey = "argusmac.serverURLOverride"

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    /// Server URL override, e.g. `http://127.0.0.1:7743`. `nil`/empty => default.
    var serverURLString: String? {
        get { defaults.string(forKey: Self.serverURLKey) }
        set {
            if let value = newValue, !value.isEmpty {
                defaults.set(value, forKey: Self.serverURLKey)
            } else {
                defaults.removeObject(forKey: Self.serverURLKey)
            }
        }
    }

    /// Token override, stored in the Keychain. `nil`/empty => resolve from file.
    var tokenOverride: String? {
        get { Keychain.readToken() }
        set { Keychain.writeToken(newValue) }
    }
}

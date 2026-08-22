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
    private static let notifyNeedsInputKey = "argusmac.notifyNeedsInput"
    private static let notifyIdleKey = "argusmac.notifyIdle"
    private static let showMenuBarExtraKey = "argusmac.showMenuBarExtra"

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    /// Reads a Bool that defaults to `true` when the key was never written — so
    /// notifications + the menu-bar extra are opt-out, not opt-in.
    private func boolDefaultingTrue(_ key: String) -> Bool {
        defaults.object(forKey: key) == nil ? true : defaults.bool(forKey: key)
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

    /// Post a notification when a task needs input. Default on.
    var notifyOnNeedsInput: Bool {
        get { boolDefaultingTrue(Self.notifyNeedsInputKey) }
        set { defaults.set(newValue, forKey: Self.notifyNeedsInputKey) }
    }

    /// Post a notification when a task goes idle (only while the app is not
    /// frontmost — that gate lives in ``AppState``). Default OFF: idle events
    /// fire far more often than needs-input across a fleet of concurrently
    /// running tasks, so an enabled-by-default idle notification floods the
    /// user. Opt-in via Settings.
    var notifyOnIdle: Bool {
        get { defaults.bool(forKey: Self.notifyIdleKey) }
        set { defaults.set(newValue, forKey: Self.notifyIdleKey) }
    }

    /// Show the menu-bar extra. Default on.
    var showMenuBarExtra: Bool {
        get { boolDefaultingTrue(Self.showMenuBarExtraKey) }
        set { defaults.set(newValue, forKey: Self.showMenuBarExtraKey) }
    }
}

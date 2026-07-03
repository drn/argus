import Foundation

/// The settings snapshot from `GET /api/settings` (`settingsResponse` in
/// `internal/api/settings.go`). Mirrors the web-manageable subset of TUI
/// settings.
public struct Settings: Sendable, Equatable, Decodable {
    public let sandbox: SandboxSettings
    public let kb: KBSettings
    public let api: APISettings
    public let defaults: DefaultsSettings
}

public struct SandboxSettings: Sendable, Equatable, Decodable {
    public let enabled: Bool
    /// Whether sandbox-exec is available on this host.
    public let available: Bool
    public let denyRead: [String]
    public let extraWrite: [String]
    public let allowAppleEvents: [String]

    enum CodingKeys: String, CodingKey {
        case enabled, available
        case denyRead = "deny_read"
        case extraWrite = "extra_write"
        case allowAppleEvents = "allow_apple_events"
    }
}

public struct KBSettings: Sendable, Equatable, Decodable {
    public let enabled: Bool
    public let metisVaultPath: String

    enum CodingKeys: String, CodingKey {
        case enabled
        case metisVaultPath = "metis_vault_path"
    }
}

public struct APISettings: Sendable, Equatable, Decodable {
    public let enabled: Bool
    public let httpPort: Int

    enum CodingKeys: String, CodingKey {
        case enabled
        case httpPort = "http_port"
    }
}

public struct DefaultsSettings: Sendable, Equatable, Decodable {
    public let backend: String
    public let shareProject: String
    public let permissionMode: String

    enum CodingKeys: String, CodingKey {
        case backend
        case shareProject = "share_project"
        case permissionMode = "permission_mode"
    }
}

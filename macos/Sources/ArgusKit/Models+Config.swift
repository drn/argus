import Foundation

/// A project's full configuration, as returned by `GET /api/projects/full`
/// (`projectJSON` in `internal/api/handlers.go`). `sandbox` is left type-erased
/// because its shape is an inherit-aware override that the SwiftUI layer can
/// interpret lazily.
public struct ProjectInfo: Sendable, Equatable, Decodable {
    public let name: String
    public let path: String
    public let branch: String?
    public let backend: String?
    public let sandbox: JSONValue?
}

/// A configured agent backend, as returned by `GET /api/backends`
/// (`backendJSON` in `internal/api/handlers.go`).
public struct BackendInfo: Sendable, Equatable, Decodable {
    public let name: String
    public let command: String
    public let promptFlag: String?
    /// The backend's default model, if configured.
    public let model: String?
    /// The per-backend option list surfaced to the new-task model picker.
    public let models: [String]?

    enum CodingKeys: String, CodingKey {
        case name, command, model, models
        case promptFlag = "prompt_flag"
    }
}

/// A skill entry, as returned by `GET /api/skills` (`skillJSON`).
public struct Skill: Sendable, Equatable, Decodable {
    public let name: String
    public let description: String?
}

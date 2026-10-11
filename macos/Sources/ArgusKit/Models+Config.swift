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
    /// The backend's family (`claude`, `codex`, `pi`, `opencode`,
    /// `antigravity`, or "" for a custom command), from `agent.FamilyOf`.
    /// Optional so an older daemon that omits it still decodes.
    public let kind: String?
    /// Human-facing family label (the backend name for custom commands).
    public let label: String?
    /// Skill invocation prefix ("$" for codex, "/" otherwise).
    public let skillPrefix: String?
    /// Whether the Claude session list/switch endpoints accept this backend.
    public let sessionSwitcher: Bool?

    public init(name: String, command: String, promptFlag: String? = nil, model: String? = nil,
                models: [String]? = nil, kind: String? = nil, label: String? = nil,
                skillPrefix: String? = nil, sessionSwitcher: Bool? = nil) {
        self.name = name
        self.command = command
        self.promptFlag = promptFlag
        self.model = model
        self.models = models
        self.kind = kind
        self.label = label
        self.skillPrefix = skillPrefix
        self.sessionSwitcher = sessionSwitcher
    }

    enum CodingKeys: String, CodingKey {
        case name, command, model, models, kind, label
        case promptFlag = "prompt_flag"
        case skillPrefix = "skill_prefix"
        case sessionSwitcher = "session_switcher"
    }
}

/// A skill entry, as returned by `GET /api/skills` (`skillJSON`).
public struct Skill: Sendable, Equatable, Decodable {
    public let name: String
    public let description: String?
}

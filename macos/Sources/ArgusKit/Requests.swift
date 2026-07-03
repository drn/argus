import Foundation

/// Body for `POST /api/tasks` (`createTaskReq`). `backend` / `model` are
/// omitted when empty so the daemon falls back to its per-project / global
/// defaults.
public struct CreateTaskRequest: Sendable, Encodable {
    public var name: String
    public var prompt: String
    public var project: String
    public var backend: String?
    public var model: String?

    public init(name: String = "", prompt: String = "", project: String,
                backend: String? = nil, model: String? = nil) {
        self.name = name
        self.prompt = prompt
        self.project = project
        self.backend = backend
        self.model = model
    }

    enum CodingKeys: String, CodingKey { case name, prompt, project, backend, model }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(name, forKey: .name)
        try c.encode(prompt, forKey: .prompt)
        try c.encode(project, forKey: .project)
        try c.encodeIfPresent(backend, forKey: .backend)
        try c.encodeIfPresent(model, forKey: .model)
    }
}

/// Body for `POST /api/tasks/{id}/fork` (`forkReq`). Empty fields inherit from
/// the source task.
public struct ForkRequest: Sendable, Encodable {
    public var name: String
    public var prompt: String
    public var project: String

    public init(name: String = "", prompt: String = "", project: String = "") {
        self.name = name
        self.prompt = prompt
        self.project = project
    }
}

/// Partial-update body for `POST /api/schedules` and `PUT /api/schedules/{id}`
/// (`scheduleRequest`). Every field is optional so callers can do partial
/// updates; pass an empty string for `runOnceAt` to clear a one-shot back to
/// recurring.
public struct ScheduleRequest: Sendable, Encodable {
    public var name: String?
    public var project: String?
    public var prompt: String?
    public var backend: String?
    public var model: String?
    public var schedule: String?
    public var runOnceAt: String?
    public var enabled: Bool?

    public init(name: String? = nil, project: String? = nil, prompt: String? = nil,
                backend: String? = nil, model: String? = nil, schedule: String? = nil,
                runOnceAt: String? = nil, enabled: Bool? = nil) {
        self.name = name
        self.project = project
        self.prompt = prompt
        self.backend = backend
        self.model = model
        self.schedule = schedule
        self.runOnceAt = runOnceAt
        self.enabled = enabled
    }

    enum CodingKeys: String, CodingKey {
        case name, project, prompt, backend, model, schedule, enabled
        case runOnceAt = "run_once_at"
    }
}

/// Partial-update body for `PUT /api/settings` (`updateSettingsReq`). Every
/// section is optional; a `nil` slice means "leave alone" and an empty slice
/// means "clear". The `sandbox` section requires the master token.
public struct SettingsUpdate: Sendable, Encodable {
    public var sandbox: SandboxUpdate?
    public var kb: KBUpdate?
    public var api: APIUpdate?
    public var defaults: DefaultsUpdate?

    public init(sandbox: SandboxUpdate? = nil, kb: KBUpdate? = nil,
                api: APIUpdate? = nil, defaults: DefaultsUpdate? = nil) {
        self.sandbox = sandbox
        self.kb = kb
        self.api = api
        self.defaults = defaults
    }

    public struct SandboxUpdate: Sendable, Encodable {
        public var enabled: Bool?
        public var denyRead: [String]?
        public var extraWrite: [String]?
        public var allowAppleEvents: [String]?
        public init(enabled: Bool? = nil, denyRead: [String]? = nil,
                    extraWrite: [String]? = nil, allowAppleEvents: [String]? = nil) {
            self.enabled = enabled
            self.denyRead = denyRead
            self.extraWrite = extraWrite
            self.allowAppleEvents = allowAppleEvents
        }
        enum CodingKeys: String, CodingKey {
            case enabled
            case denyRead = "deny_read"
            case extraWrite = "extra_write"
            case allowAppleEvents = "allow_apple_events"
        }
    }

    public struct KBUpdate: Sendable, Encodable {
        public var enabled: Bool?
        public var metisVaultPath: String?
        public init(enabled: Bool? = nil, metisVaultPath: String? = nil) {
            self.enabled = enabled
            self.metisVaultPath = metisVaultPath
        }
        enum CodingKeys: String, CodingKey {
            case enabled
            case metisVaultPath = "metis_vault_path"
        }
    }

    public struct APIUpdate: Sendable, Encodable {
        public var enabled: Bool?
        public init(enabled: Bool? = nil) { self.enabled = enabled }
    }

    public struct DefaultsUpdate: Sendable, Encodable {
        public var backend: String?
        public var shareProject: String?
        public var permissionMode: String?
        public init(backend: String? = nil, shareProject: String? = nil,
                    permissionMode: String? = nil) {
            self.backend = backend
            self.shareProject = shareProject
            self.permissionMode = permissionMode
        }
        enum CodingKeys: String, CodingKey {
            case backend
            case shareProject = "share_project"
            case permissionMode = "permission_mode"
        }
    }
}

/// Body for `POST /api/tasks/{id}/messages` (`handleSendMessage`).
public struct SendMessageRequest: Sendable, Encodable {
    public var to: String
    public var body: String
    public var kind: String?
    public var inReplyTo: String?

    public init(to: String, body: String, kind: String? = nil, inReplyTo: String? = nil) {
        self.to = to
        self.body = body
        self.kind = kind
        self.inReplyTo = inReplyTo
    }

    enum CodingKeys: String, CodingKey {
        case to, body, kind
        case inReplyTo = "in_reply_to"
    }
}

/// Body for `PUT /api/tasks/{id}/meta` (`metaPutReq`). Set exactly one of
/// (`key` + `value`) or `entries`.
public struct MetaPutRequest: Sendable, Encodable {
    public var namespace: String
    public var key: String?
    public var value: String?
    public var entries: [String: String]?

    public init(namespace: String, key: String? = nil, value: String? = nil,
                entries: [String: String]? = nil) {
        self.namespace = namespace
        self.key = key
        self.value = value
        self.entries = entries
    }

    /// Convenience for a single-key upsert.
    public static func single(namespace: String, key: String, value: String) -> MetaPutRequest {
        MetaPutRequest(namespace: namespace, key: key, value: value)
    }

    /// Convenience for a batch upsert.
    public static func batch(namespace: String, entries: [String: String]) -> MetaPutRequest {
        MetaPutRequest(namespace: namespace, entries: entries)
    }
}

import Foundation

// Request/response models for the eight Hera mutation REST endpoints
// (add-hera-mutation-rest-api), all under
// `/api/hera/orchestrators/{orch_id}/...`. Every mutation resolves that
// orchestrator's live coordinator server-side and acts as it — none of these
// request bodies carries a sender/actor field.

// MARK: - Spawn worker

/// Body for `POST /api/hera/orchestrators/{orch_id}/workers`
/// (`hera_spawn_worker`'s REST equivalent). Only `prompt` is required;
/// omitted optionals default the same way the MCP tool does (role name
/// derived from the prompt slug, project/branch/backend from the
/// coordinator's own).
public struct HeraSpawnWorkerRequest: Sendable, Encodable {
    public var prompt: String
    public var roleName: String?
    public var project: String?
    public var branch: String?
    public var backend: String?
    public var model: String?

    public init(prompt: String, roleName: String? = nil, project: String? = nil,
                branch: String? = nil, backend: String? = nil, model: String? = nil) {
        self.prompt = prompt
        self.roleName = roleName
        self.project = project
        self.branch = branch
        self.backend = backend
        self.model = model
    }

    enum CodingKeys: String, CodingKey {
        case prompt
        case roleName = "role_name"
        case project, branch, backend, model
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(prompt, forKey: .prompt)
        try c.encodeIfPresent(roleName, forKey: .roleName)
        try c.encodeIfPresent(project, forKey: .project)
        try c.encodeIfPresent(branch, forKey: .branch)
        try c.encodeIfPresent(backend, forKey: .backend)
        try c.encodeIfPresent(model, forKey: .model)
    }
}

/// Response from a successful spawn-worker call.
public struct HeraSpawnWorkerResponse: Sendable, Equatable, Decodable {
    public let roleID: Int64
    public let orchID: Int64
    public let name: String
    public let kind: String
    public let project: String
    public let argusTaskID: String
    public let taskName: String
    public let taskStatus: String

    enum CodingKeys: String, CodingKey {
        case roleID = "role_id"
        case orchID = "orch_id"
        case name, kind, project
        case argusTaskID = "argus_task_id"
        case taskName = "task_name"
        case taskStatus = "task_status"
    }
}

// MARK: - Send message

/// Body for `POST /api/hera/orchestrators/{orch_id}/messages`
/// (`hera_send`'s REST equivalent, narrowed to coordinator-as-sender only —
/// no `from_role_id`, no `status`). `to` is the recipient's `role_id`.
public struct HeraSendMessageRequest: Sendable, Encodable {
    public var to: Int64
    public var tldr: String
    public var body: String
    public var inReplyTo: Int64?

    public init(to: Int64, tldr: String, body: String, inReplyTo: Int64? = nil) {
        self.to = to
        self.tldr = tldr
        self.body = body
        self.inReplyTo = inReplyTo
    }

    enum CodingKeys: String, CodingKey {
        case to, tldr, body
        case inReplyTo = "in_reply_to"
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(to, forKey: .to)
        try c.encode(tldr, forKey: .tldr)
        try c.encode(body, forKey: .body)
        try c.encodeIfPresent(inReplyTo, forKey: .inReplyTo)
    }
}

/// Response from a successful send-message call.
public struct HeraSendMessageResponse: Sendable, Equatable, Decodable {
    public let messageID: Int64
    public let toRoleID: Int64
    public let deliveryMode: String

    enum CodingKeys: String, CodingKey {
        case messageID = "message_id"
        case toRoleID = "to_role_id"
        case deliveryMode = "delivery_mode"
    }
}

// MARK: - Plan node create

/// Body for `POST /api/hera/orchestrators/{orch_id}/plan/nodes`
/// (`hera_plan_node`'s REST equivalent). `kind` defaults to `"worker"`
/// server-side when omitted; a worker node needs `prompt`, a `"subcoord"`
/// node needs `goal`.
public struct HeraPlanNodeCreateRequest: Sendable, Encodable {
    public var name: String
    public var kind: String?
    public var prompt: String?
    public var goal: String?
    public var project: String?

    public init(name: String, kind: String? = nil, prompt: String? = nil,
                goal: String? = nil, project: String? = nil) {
        self.name = name
        self.kind = kind
        self.prompt = prompt
        self.goal = goal
        self.project = project
    }

    enum CodingKeys: String, CodingKey { case name, kind, prompt, goal, project }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(name, forKey: .name)
        try c.encodeIfPresent(kind, forKey: .kind)
        try c.encodeIfPresent(prompt, forKey: .prompt)
        try c.encodeIfPresent(goal, forKey: .goal)
        try c.encodeIfPresent(project, forKey: .project)
    }
}

/// Response from a successful plan-node create call.
public struct HeraPlanNodeResponse: Sendable, Equatable, Decodable {
    public let roleID: Int64
    public let name: String
    public let project: String
    public let kind: String
    public let status: String

    enum CodingKeys: String, CodingKey {
        case roleID = "role_id"
        case name, project, kind, status
    }
}

// MARK: - Plan (whole graph)

/// One node in a whole-graph `POST .../plan` call — same shape as
/// ``HeraPlanNodeCreateRequest`` minus the wrapping endpoint.
public struct HeraPlanNodeSpec: Sendable, Encodable {
    public var name: String
    public var kind: String?
    public var prompt: String?
    public var goal: String?
    public var project: String?

    public init(name: String, kind: String? = nil, prompt: String? = nil,
                goal: String? = nil, project: String? = nil) {
        self.name = name
        self.kind = kind
        self.prompt = prompt
        self.goal = goal
        self.project = project
    }

    enum CodingKeys: String, CodingKey { case name, kind, prompt, goal, project }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(name, forKey: .name)
        try c.encodeIfPresent(kind, forKey: .kind)
        try c.encodeIfPresent(prompt, forKey: .prompt)
        try c.encodeIfPresent(goal, forKey: .goal)
        try c.encodeIfPresent(project, forKey: .project)
    }
}

/// One blocking edge in a whole-graph `POST .../plan` call. Both endpoints
/// are NAMES — an in-batch node's name or an existing role's current name —
/// since in-batch nodes have no id until the transaction commits (matches
/// `hera_plan` exactly).
public struct HeraPlanEdgeSpec: Sendable, Encodable {
    public var blocked: String
    public var blocker: String

    public init(blocked: String, blocker: String) {
        self.blocked = blocked
        self.blocker = blocker
    }
}

/// Body for `POST /api/hera/orchestrators/{orch_id}/plan` — the whole-graph
/// endpoint (`hera_plan`'s REST equivalent). Nodes and edges are created in
/// one transaction; any validation error rolls back the entire call.
public struct HeraPlanCreateRequest: Sendable, Encodable {
    public var nodes: [HeraPlanNodeSpec]
    public var edges: [HeraPlanEdgeSpec]

    public init(nodes: [HeraPlanNodeSpec], edges: [HeraPlanEdgeSpec] = []) {
        self.nodes = nodes
        self.edges = edges
    }
}

/// Response from a successful whole-graph plan create call.
public struct HeraPlanCreateResponse: Sendable, Equatable, Decodable {
    public let nodesCreated: Int
    public let edgesCreated: Int

    enum CodingKeys: String, CodingKey {
        case nodesCreated = "nodes_created"
        case edgesCreated = "edges_created"
    }
}

// MARK: - Plan node update / cancel

/// Body for `PATCH /api/hera/orchestrators/{orch_id}/plan/nodes/{role_id}`
/// (`hera_plan_node_update`'s REST equivalent). At least one of `prompt` or
/// `project` must be supplied.
public struct HeraPlanNodeUpdateRequest: Sendable, Encodable {
    public var prompt: String?
    public var project: String?

    public init(prompt: String? = nil, project: String? = nil) {
        self.prompt = prompt
        self.project = project
    }

    enum CodingKeys: String, CodingKey { case prompt, project }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(prompt, forKey: .prompt)
        try c.encodeIfPresent(project, forKey: .project)
    }
}

/// Response shared by the plan-node update and cancel endpoints (both return
/// `{role_id, status}`).
public struct HeraPlanNodeStatusResponse: Sendable, Equatable, Decodable {
    public let roleID: Int64
    public let status: String

    enum CodingKeys: String, CodingKey {
        case roleID = "role_id"
        case status
    }
}

// MARK: - Blocking edges

/// Body for `POST /api/hera/orchestrators/{orch_id}/plan/blocks`
/// (`hera_block`'s REST equivalent). Both endpoints are `role_id`s, unlike
/// the MCP tool which addresses roles by name.
public struct HeraBlockCreateRequest: Sendable, Encodable {
    public var blockedRoleID: Int64
    public var blockerRoleID: Int64

    public init(blockedRoleID: Int64, blockerRoleID: Int64) {
        self.blockedRoleID = blockedRoleID
        self.blockerRoleID = blockerRoleID
    }

    enum CodingKeys: String, CodingKey {
        case blockedRoleID = "blocked_role_id"
        case blockerRoleID = "blocker_role_id"
    }
}

/// Response shared by the block create/delete endpoints (both return
/// `{blocked_role_id, blocker_role_id}`).
public struct HeraBlockResponse: Sendable, Equatable, Decodable {
    public let blockedRoleID: Int64
    public let blockerRoleID: Int64

    enum CodingKeys: String, CodingKey {
        case blockedRoleID = "blocked_role_id"
        case blockerRoleID = "blocker_role_id"
    }
}

import Foundation

/// An inter-task message (`model.TaskMessage`), as returned in the
/// `messages` array of `GET /api/tasks/{id}/inbox`.
///
/// `readAt` uses Go's `omitzero` tag, so it is absent for unread messages;
/// `inReplyTo` is `omitempty`.
public struct InboxMessage: Sendable, Equatable, Identifiable, Decodable {
    public let id: String
    public let from: String
    public let to: String
    /// `note` | `question` | `answer`.
    public let kind: String
    public let body: String
    public let inReplyTo: String?
    /// RFC3339 timestamp.
    public let createdAt: String
    /// RFC3339 timestamp; nil when the message is unread.
    public let readAt: String?

    enum CodingKeys: String, CodingKey {
        case id, from, to, kind, body
        case inReplyTo = "in_reply_to"
        case createdAt = "created_at"
        case readAt = "read_at"
    }

    /// True when the message has not yet been acked.
    public var isUnread: Bool { readAt == nil }
}

/// The inbox response envelope for `GET /api/tasks/{id}/inbox`.
public struct Inbox: Sendable, Equatable, Decodable {
    public let messages: [InboxMessage]
    public let unreadCount: Int

    enum CodingKeys: String, CodingKey {
        case messages
        case unreadCount = "unread_count"
    }
}

/// The create envelope returned by `POST /api/tasks/{id}/messages`.
public struct SendMessageResponse: Sendable, Equatable, Decodable {
    public let id: String
    /// RFC3339 timestamp.
    public let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case createdAt = "created_at"
    }
}

/// One sidecar metadata row (`metaEntryJSON`), as returned in the `entries`
/// array of `GET /api/tasks/{id}/meta`.
public struct MetaEntry: Sendable, Equatable, Decodable {
    public let namespace: String
    public let key: String
    public let value: String
    /// RFC3339 timestamp.
    public let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case namespace, key, value
        case updatedAt = "updated_at"
    }
}

/// The active PTY size from `GET /api/tasks/{id}/size`.
public struct PTYSize: Sendable, Equatable, Decodable {
    public let cols: Int
    public let rows: Int
}

/// The response from `POST /api/tasks/{id}/resize`.
public struct ResizeResult: Sendable, Equatable, Decodable {
    public let cols: Int
    public let rows: Int
    /// True when the resize also triggered a kill+resume rerender.
    public let rerendered: Bool
}

/// The most-recent terminal output tail from `GET /api/tasks/{id}/output`.
/// `total` is parsed from the `X-Output-Total` header (pass it back as
/// `since` on ``ArgusClient/terminalStream(taskID:since:)`` for a gapless
/// resume); `source` is the `X-Source` header (`ring` | `log` | `live`).
public struct OutputTail: Sendable, Equatable {
    public let data: Data
    public let total: UInt64
    public let source: String
}

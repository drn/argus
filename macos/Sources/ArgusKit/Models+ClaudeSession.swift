import Foundation

/// A Claude Code conversation session available for a task, as returned by
/// `GET /api/tasks/{id}/claude-sessions` (mirrors `internal/claudesession.Session`
/// via the daemon's `claudeSessionJSON` wire shape — see
/// `specs/rest-api/spec.md`'s "Claude session switcher" delta). Listed
/// newest-activity-first by the server.
public struct ClaudeSession: Sendable, Equatable, Codable, Identifiable {
    public let id: String
    public let title: String
    public let branch: String
    public let prRef: String
    /// Most recent activity timestamp.
    public let modTime: Date
    public let sizeBytes: Int64

    public init(id: String, title: String, branch: String, prRef: String,
                modTime: Date, sizeBytes: Int64) {
        self.id = id
        self.title = title
        self.branch = branch
        self.prRef = prRef
        self.modTime = modTime
        self.sizeBytes = sizeBytes
    }

    enum CodingKeys: String, CodingKey {
        case id, title, branch
        case prRef = "pr_ref"
        case modTime = "mod_time"
        case sizeBytes = "size_bytes"
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        title = try c.decode(String.self, forKey: .title)
        branch = try c.decode(String.self, forKey: .branch)
        prRef = try c.decode(String.self, forKey: .prRef)
        let raw = try c.decode(String.self, forKey: .modTime)
        guard let parsed = Self.parseModTime(raw) else {
            throw DecodingError.dataCorruptedError(
                forKey: .modTime, in: c,
                debugDescription: "unrecognized mod_time timestamp: \(raw)")
        }
        modTime = parsed
        sizeBytes = try c.decode(Int64.self, forKey: .sizeBytes)
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(id, forKey: .id)
        try c.encode(title, forKey: .title)
        try c.encode(branch, forKey: .branch)
        try c.encode(prRef, forKey: .prRef)
        try c.encode(Self.formatModTime(modTime), forKey: .modTime)
        try c.encode(sizeBytes, forKey: .sizeBytes)
    }

    // MARK: - Timestamp parsing
    //
    // Every other RFC3339 timestamp in this SDK (`Task.createdAt`,
    // `Schedule.createdAt`, ...) is deliberately kept as a raw `String` — see
    // `Models+Schedule.swift`'s "Timestamp fields are RFC3339 strings"
    // convention. `mod_time` is the first field this SDK decodes into a real
    // `Date` (needed for sorting/relative-time display in the session
    // picker), so there is no existing `Date`-decoding strategy to match.
    //
    // The daemon marshals `mod_time` from a Go `time.Time` via the stdlib's
    // default JSON encoding, which is RFC3339 with a variable-length
    // fractional-second component that Go trims to nothing when it's exactly
    // zero (e.g. "2026-08-21T10:00:00Z") and otherwise renders at whatever
    // precision survives trailing-zero trimming (e.g.
    // "2026-08-21T10:00:00.123456789Z"). `ISO8601DateFormatter`'s
    // `.withFractionalSeconds` option is calibrated to millisecond precision
    // and doesn't reliably parse an arbitrary-length fractional component, so
    // rather than gamble on how many digits Go emitted, the fractional part
    // is stripped before parsing — sub-second precision has no observable
    // effect on this feature (session ordering/relative time are
    // second-granularity at best).
    //
    // A fresh formatter is built per call rather than cached in a `static
    // let` — `ISO8601DateFormatter` is a mutable, non-`Sendable` Foundation
    // class, so a shared instance trips Swift 6's strict-concurrency
    // global-mutable-state check; this type only ever decodes/encodes a
    // handful of sessions at a time, so the reallocation cost is immaterial.
    private static func makeFormatter() -> ISO8601DateFormatter {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }

    static func parseModTime(_ raw: String) -> Date? {
        let formatter = makeFormatter()
        if let d = formatter.date(from: raw) { return d }
        guard let dotIdx = raw.firstIndex(of: ".") else { return nil }
        let head = raw[..<dotIdx]
        let rest = raw[raw.index(after: dotIdx)...]
        guard let tzIdx = rest.firstIndex(where: { $0 == "Z" || $0 == "+" || $0 == "-" }) else {
            return nil
        }
        return formatter.date(from: "\(head)\(rest[tzIdx...])")
    }

    static func formatModTime(_ date: Date) -> String {
        makeFormatter().string(from: date)
    }
}

/// The response envelope for `GET /api/tasks/{id}/claude-sessions`.
struct ClaudeSessionsResponse: Decodable {
    let sessions: [ClaudeSession]
    let currentSessionID: String

    enum CodingKeys: String, CodingKey {
        case sessions
        case currentSessionID = "current_session_id"
    }
}

/// The response for `POST /api/tasks/{id}/claude-session`. `pid` is absent
/// when `status == "unchanged"` (the posted session was already current).
struct ClaudeSessionSwitchResponse: Decodable {
    let status: String
    let pid: Int?
}

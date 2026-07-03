import Foundation

/// A scheduled task, as returned by `GET /api/schedules` and the create/update
/// endpoints (`scheduleJSON` in `internal/api/schedules.go`). Timestamp fields
/// are RFC3339 strings; `omitempty` fields are optional.
public struct Schedule: Sendable, Equatable, Identifiable, Decodable {
    public let id: String
    public let name: String
    public let project: String
    public let prompt: String
    public let backend: String?
    public let model: String?
    /// Cron expression (may be "" for one-shot schedules).
    public let schedule: String
    /// RFC3339 fire time for one-shot schedules; nil for recurring.
    public let runOnceAt: String?
    public let enabled: Bool
    public let createdAt: String
    public let lastRunAt: String?
    public let nextRunAt: String?
    public let lastTaskID: String?
    public let lastError: String?

    enum CodingKeys: String, CodingKey {
        case id, name, project, prompt, backend, model, schedule, enabled
        case runOnceAt = "run_once_at"
        case createdAt = "created_at"
        case lastRunAt = "last_run_at"
        case nextRunAt = "next_run_at"
        case lastTaskID = "last_task_id"
        case lastError = "last_error"
    }
}

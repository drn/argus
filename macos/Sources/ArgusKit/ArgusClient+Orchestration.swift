import Foundation

extension ArgusClient {
    /// `GET /api/hera` — the read-only Hera orchestration roster.
    public func heraRoster() async throws -> HeraRoster {
        try await getDecoding("/api/hera")
    }

    // MARK: - Schedules

    private struct SchedulesEnvelope: Decodable { let schedules: [Schedule] }

    /// `GET /api/schedules`.
    public func schedules() async throws -> [Schedule] {
        let env: SchedulesEnvelope = try await getDecoding("/api/schedules")
        return env.schedules
    }

    /// `POST /api/schedules` — creates a schedule.
    public func createSchedule(_ req: ScheduleRequest) async throws -> Schedule {
        try await sendDecoding("POST", "/api/schedules", body: req)
    }

    /// `PUT /api/schedules/{id}` — partial-updates a schedule.
    public func updateSchedule(id: String, _ req: ScheduleRequest) async throws -> Schedule {
        try await sendDecoding("PUT", "/api/schedules/\(id)", body: req)
    }

    /// `DELETE /api/schedules/{id}`.
    public func deleteSchedule(id: String) async throws {
        try await sendVoid("DELETE", "/api/schedules/\(id)")
    }

    private struct RunScheduleResponse: Decodable {
        let taskID: String
        enum CodingKeys: String, CodingKey { case taskID = "task_id" }
    }

    /// `POST /api/schedules/{id}/run` — fires a schedule out-of-cycle. Returns
    /// the created task's ID.
    @discardableResult
    public func runSchedule(id: String) async throws -> String {
        let resp: RunScheduleResponse = try await sendDecoding("POST", "/api/schedules/\(id)/run")
        return resp.taskID
    }
}

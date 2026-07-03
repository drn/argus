import Foundation

/// Live and idle session counts.
public struct SessionCounts: Sendable, Equatable, Decodable {
    public let running: Int
    public let idle: Int
}

/// Per-status task counts.
public struct TaskCounts: Sendable, Equatable, Decodable {
    public let pending: Int
    public let inProgress: Int
    public let inReview: Int
    public let complete: Int

    enum CodingKeys: String, CodingKey {
        case pending, complete
        case inProgress = "in_progress"
        case inReview = "in_review"
    }
}

/// The daemon's at-a-glance health from `GET /api/status` (`statusResponse`).
public struct DaemonStatus: Sendable, Equatable, Decodable {
    public let ok: Bool
    public let sessions: SessionCounts
    public let tasks: TaskCounts
}

/// The host-load snapshot from `GET /api/system-metrics` — the flattened
/// `sysmetrics.Snapshot` plus live session counts (`systemMetricsResponse` in
/// `internal/api/metrics.go`). Each metric group carries an `*Avail` flag; when
/// false the numeric fields are meaningless and callers should render a
/// placeholder.
public struct SystemMetrics: Sendable, Equatable, Decodable {
    public let cpuPercent: Double
    public let cpuAvail: Bool

    public let load1: Double
    public let load5: Double
    public let load15: Double
    public let loadAvail: Bool

    public let memTotal: UInt64
    public let memUsed: UInt64
    public let memAvailable: UInt64
    public let memPercent: Double
    public let memAvail: Bool

    public let swapTotal: UInt64
    public let swapUsed: UInt64
    public let swapPercent: Double
    public let swapAvail: Bool

    public let diskTotal: UInt64
    public let diskUsed: UInt64
    public let diskFree: UInt64
    public let diskPercent: Double
    public let diskPath: String
    public let diskAvail: Bool

    public let procRSS: UInt64
    public let procAvail: Bool

    public let uptimeSec: UInt64
    public let uptimeAvail: Bool

    /// RFC3339 timestamp of when the snapshot was collected.
    public let sampledAt: String

    public let sessions: SessionCounts

    enum CodingKeys: String, CodingKey {
        case cpuPercent = "cpu_percent"
        case cpuAvail = "cpu_avail"
        case load1, load5, load15
        case loadAvail = "load_avail"
        case memTotal = "mem_total"
        case memUsed = "mem_used"
        case memAvailable = "mem_available"
        case memPercent = "mem_percent"
        case memAvail = "mem_avail"
        case swapTotal = "swap_total"
        case swapUsed = "swap_used"
        case swapPercent = "swap_percent"
        case swapAvail = "swap_avail"
        case diskTotal = "disk_total"
        case diskUsed = "disk_used"
        case diskFree = "disk_free"
        case diskPercent = "disk_percent"
        case diskPath = "disk_path"
        case diskAvail = "disk_avail"
        case procRSS = "proc_rss"
        case procAvail = "proc_avail"
        case uptimeSec = "uptime_sec"
        case uptimeAvail = "uptime_avail"
        case sampledAt = "sampled_at"
        case sessions
    }
}

/// The runner's live session state from `GET /api/sessions/state`.
public struct SessionState: Sendable, Equatable, Decodable {
    public let running: [String]
    public let idle: [String]
}

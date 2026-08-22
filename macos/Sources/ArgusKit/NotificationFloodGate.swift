import Foundation

/// Client-side burst limiter for needs-input notifications, layered ON TOP OF
/// `NotificationManager`'s per-task dedupe (which only stops the *same* task
/// from restacking its own banner). This exists to stop a burst of
/// *different* tasks — e.g. catching up on an SSE reconnect backlog, or
/// several tasks legitimately hitting needs-input within the same second —
/// from each firing its own native banner in rapid succession.
///
/// Pure sliding-window rate limiter: while arrivals stay at or under
/// ``burstThreshold`` within ``window`` seconds of each other, each posts its
/// own banner immediately — no added latency for the common, sparse case,
/// and deliberately NOT a blanket "one notification every N seconds" throttle
/// that would delay a single isolated arrival. Once a burst crosses the
/// threshold, every further arrival in that burst folds into one running
/// summary instead of posting its own banner. The window is self-terminating:
/// once arrivals stop for ``window`` seconds, state resets and the next
/// arrival posts individually again.
public struct NotificationFloodGate: Sendable {
    /// How long a run of arrivals counts as "the same burst." Long enough to
    /// span an SSE reconnect replaying a backlog of several tasks (each event
    /// lands well under a second after the last), short enough that two
    /// genuinely-unrelated needs-input events minutes apart never coalesce.
    public static let window: TimeInterval = 5

    /// Individual banners allowed through before the rest of a burst gets
    /// coalesced. Small on purpose: two or three tasks legitimately finishing
    /// around the same moment should each still get their own banner, but a
    /// real flood (reconnect backlog, several tasks at once) should collapse
    /// quickly rather than after dozens of banners.
    public static let burstThreshold = 3

    private var recentArrivals: [Date] = []
    private var coalesced: [(id: String, name: String)] = []

    public init() {}

    public enum Decision: Equatable, Sendable {
        /// Post this arrival's own banner normally.
        case postIndividual
        /// Fold this arrival into the current burst; post/update the
        /// returned summary instead of an individual banner.
        case coalesce(NotificationBurstSummary)
    }

    /// Records one needs-input arrival and decides how it should be
    /// presented. Call once per arrival that has already passed the
    /// per-task dedupe check (a re-arrival for a task already coalesced in
    /// the current burst is folded in without duplicating its name).
    public mutating func decide(taskID: String, taskName: String, now: Date = Date()) -> Decision {
        recentArrivals.removeAll { now.timeIntervalSince($0) > Self.window }
        if recentArrivals.isEmpty {
            coalesced.removeAll()
        }
        recentArrivals.append(now)

        guard recentArrivals.count > Self.burstThreshold else {
            return .postIndividual
        }
        if !coalesced.contains(where: { $0.id == taskID }) {
            coalesced.append((id: taskID, name: taskName))
        }
        return .coalesce(NotificationBurstSummary(taskNames: coalesced.map(\.name)))
    }
}

/// Display text for a coalesced burst of needs-input notifications.
public struct NotificationBurstSummary: Equatable, Sendable {
    public let taskNames: [String]

    public init(taskNames: [String]) {
        self.taskNames = taskNames
    }

    public var title: String {
        "\(taskNames.count) tasks need input"
    }

    /// Names in full for a short list; truncated with a trailing remainder
    /// count once the list would otherwise overflow a notification banner.
    public var body: String {
        let maxNamed = 3
        guard taskNames.count > maxNamed else {
            return taskNames.joined(separator: ", ")
        }
        let shown = taskNames.prefix(maxNamed).joined(separator: ", ")
        return "\(shown), and \(taskNames.count - maxNamed) more"
    }
}

import Foundation

/// A decoded daemon event from the `/api/events/stream` SSE channel. Mirrors the
/// event types + payload fields in `internal/model/event.go`; every associated
/// value is defaulted from the raw payload so a missing/renamed field decodes to
/// a benign zero rather than dropping the event.
///
/// ``resync`` and ``unknown`` are the two "I can't apply this incrementally"
/// signals — the caller must re-snapshot `/api/tasks` for both (a resync means
/// history rotated out from under the cursor; an unknown type is a newer daemon
/// speaking an event this client doesn't model yet).
public enum DaemonEvent: Sendable, Equatable {
    case taskCreated(taskID: String, name: String, project: String, status: String)
    case taskStatusChanged(taskID: String, from: String, to: String)
    case taskCompleted(taskID: String)
    case taskArchived(taskID: String)
    case taskDeleted(taskID: String)
    case taskRenamed(taskID: String, from: String, to: String)
    case taskForked(taskID: String, fromTaskID: String, toTaskID: String)
    case messageSent(taskID: String)
    case messageAcked(taskID: String)
    case sessionStarted(taskID: String, pid: Int, resume: Bool)
    case sessionExited(taskID: String, stopped: Bool, pendingRestart: Bool)
    case sessionIdle(taskID: String)
    case sessionNeedsInput(taskID: String, needsInput: Bool)
    case sessionFocus(taskID: String, focused: Bool)
    /// The synthetic `resync` event (`internal/api/events_stream.go`): the
    /// cursor predated the ring, so the client must re-snapshot daemon state.
    case resync(reason: String)
    /// An event type this client version does not model. Treated as a
    /// re-snapshot trigger so a newer daemon never silently desyncs the UI.
    case unknown(type: String)

    /// The task the event pertains to, for the caller's convenience. Empty for
    /// ``resync`` (which is not scoped to a task).
    public var taskID: String {
        switch self {
        case let .taskCreated(id, _, _, _):
            return id
        case let .taskStatusChanged(id, _, _):
            return id
        case let .taskCompleted(id):
            return id
        case let .taskArchived(id):
            return id
        case let .taskDeleted(id):
            return id
        case let .taskRenamed(id, _, _):
            return id
        case let .taskForked(id, _, _):
            return id
        case let .messageSent(id):
            return id
        case let .messageAcked(id):
            return id
        case let .sessionStarted(id, _, _):
            return id
        case let .sessionExited(id, _, _):
            return id
        case let .sessionIdle(id):
            return id
        case let .sessionNeedsInput(id, _):
            return id
        case let .sessionFocus(id, _):
            return id
        case .resync, .unknown:
            return ""
        }
    }
}

/// A raw daemon event decoded far enough to know its cursor id and typed body,
/// without deciding how to apply it. Returned by ``EventsStreamSession/decode(_:)``.
public struct DecodedEvent: Sendable, Equatable {
    /// The `id` from the event envelope — the monotonic ring cursor. `0` for the
    /// synthetic `resync` event (never persisted) or an undecodable frame.
    public let id: Int64
    public let event: DaemonEvent

    public init(id: Int64, event: DaemonEvent) {
        self.id = id
        self.event = event
    }
}

/// A pure, UI-free state machine for driving the daemon's `/api/events/stream`
/// SSE channel. It owns the ring-cursor bookkeeping, the ``DaemonEvent``
/// interpretation, the incremental-vs-resnapshot decision, and the
/// reconnect/backoff policy — so the whole task-list sync protocol can be
/// unit-tested without a network.
///
/// Value semantics deliberately mirror ``TerminalStreamSession``: feed it inputs
/// (``start()``, ``handle(_:)``, ``streamClosed(error:)``) and it returns
/// ``Action`` values the caller executes (open the stream, re-fetch the task
/// snapshot, apply an incremental event). It never performs I/O.
///
/// ## Protocol replicated (`internal/api/events_stream.go` + `model/event.go`)
/// 1. Open `/api/events/stream?since=<cursor>`. `since` is EXCLUSIVE: the server
///    replays `(cursor, latest]` then streams live. ``cursor`` starts at 0
///    (whole-ring replay on first connect) and advances to each event's `id`.
/// 2. Each event advances ``cursor`` to its `id` (monotonic; a lower id never
///    regresses it) so a reconnect resumes exactly where we left off.
/// 3. `resync` (cursor pre-dated the ring) and any `unknown` event type →
///    `.resnapshot`; `task.created` / `task.forked` also `.resnapshot` (the
///    event lacks the full task shape the sidebar needs). Everything else is an
///    incremental `.apply`.
/// 4. A stream that drops (network blip, daemon bounce) → reconnect from
///    ``cursor`` with exponential backoff — identical failed-attempt-keeps-
///    retrying semantics to ``TerminalStreamSession``.
public struct EventsStreamSession: Sendable, Equatable {

    // MARK: - Backoff policy (injectable so tests can pin the schedule)

    /// The delay before the first reconnect attempt (and the value the backoff
    /// resets to on healthy traffic).
    public var baseDelay: Duration
    /// The ceiling the exponential backoff saturates at.
    public var maxDelay: Duration
    /// The per-attempt growth factor applied to the pending delay.
    public var multiplier: Double

    // MARK: - Observable state

    /// The lifecycle phase, purely derived from the inputs fed so far. There is
    /// no `.ended` — the events stream is meant to run for the app's lifetime, so
    /// every close reconnects.
    public enum Phase: Sendable, Equatable {
        /// Nothing started yet.
        case idle
        /// A stream open is pending or in-flight; no event seen yet.
        case connecting
        /// Events are flowing.
        case live
        /// Waiting out a backoff delay before the next reconnect.
        case reconnecting
    }

    /// The monotonic ring cursor: the highest event `id` seen. Passed back as
    /// `since` on every (re)connect for exclusive replay.
    public private(set) var cursor: Int64 = 0
    public private(set) var phase: Phase = .idle

    /// The delay the *next* reconnect will use; grows on each unhealthy
    /// reconnect, resets to ``baseDelay`` on any received event.
    private var pendingDelay: Duration

    public init(baseDelay: Duration = .milliseconds(500),
                maxDelay: Duration = .seconds(30),
                multiplier: Double = 2.0) {
        self.baseDelay = baseDelay
        self.maxDelay = maxDelay
        self.multiplier = multiplier
        self.pendingDelay = baseDelay
    }

    // MARK: - Actions the caller executes

    public enum Action: Sendable, Equatable {
        /// Open the SSE stream at `GET /api/events/stream?since=<cursor>`.
        case openStream(since: Int64)
        /// Re-open the SSE stream, but only after waiting `after`.
        case reconnect(since: Int64, after: Duration)
        /// Re-fetch the full task list from `/api/tasks` (resync / unknown /
        /// created / forked — anything not applyable from the event alone).
        case resnapshot
        /// Apply a decoded incremental event to app state (task-list patch +
        /// OS-integration side effects).
        case apply(DaemonEvent)
    }

    // MARK: - Inputs

    /// Begins streaming from the current ``cursor`` (0 on first connect). Resets
    /// the backoff and returns the open action. Call once to kick the loop; the
    /// reconnect path is driven by ``streamClosed(error:)``.
    public mutating func start() -> [Action] {
        pendingDelay = baseDelay
        phase = .connecting
        return [.openStream(since: cursor)]
    }

    /// Signals that a scheduled reconnect attempt is now actually opening the
    /// stream. Flips `.reconnecting` → `.connecting` so a FAILURE of this attempt
    /// re-enters ``streamClosed(error:)``'s backoff path with the grown delay
    /// instead of being swallowed. Call it at the top of every consume attempt;
    /// it is a no-op in every other phase. (Same rationale as
    /// ``TerminalStreamSession/streamOpening()``.)
    public mutating func streamOpening() {
        if phase == .reconnecting { phase = .connecting }
    }

    /// Interprets one raw ``ServerEvent`` from the live stream: advances the
    /// cursor, marks traffic healthy (phase `.live`, backoff reset), and returns
    /// the incremental-vs-resnapshot decision.
    public mutating func handle(_ raw: ServerEvent) -> [Action] {
        let decoded = Self.decode(raw)
        return handle(decoded)
    }

    /// Cursor + decision logic for an already-decoded event (exposed for tests).
    public mutating func handle(_ decoded: DecodedEvent) -> [Action] {
        // A real event id advances the cursor; the synthetic resync (id 0) and
        // undecodable frames do not. Never regress on an out-of-order lower id.
        if decoded.id > cursor { cursor = decoded.id }
        // Any received event is healthy traffic: reset the backoff so a later
        // drop reconnects promptly, and flip to live.
        pendingDelay = baseDelay
        phase = .live
        return Self.effect(for: decoded.event)
    }

    /// Signals that the SSE stream finished (cleanly or with `error`). Returns a
    /// reconnect action, or nil when one is already pending (the old stream's own
    /// close must not double-book a reconnect; ``streamOpening()`` re-arms it once
    /// the scheduled attempt begins). `error` is accepted for the caller's
    /// logging; the decision is phase-based.
    public mutating func streamClosed(error: Error? = nil) -> Action? {
        switch phase {
        case .reconnecting:
            return nil
        case .idle, .connecting, .live:
            return scheduleReconnect()
        }
    }

    // MARK: - Decision + backoff helpers

    /// The incremental-vs-resnapshot decision for a decoded event. `resync`,
    /// `unknown`, `task.created` and `task.forked` all require a full re-snapshot
    /// (the first two because we've lost sync; the latter two because the event
    /// lacks the full task shape). Everything else applies incrementally.
    static func effect(for event: DaemonEvent) -> [Action] {
        switch event {
        case .resync, .unknown, .taskCreated, .taskForked:
            return [.resnapshot]
        default:
            return [.apply(event)]
        }
    }

    /// Emits a reconnect for the current cursor after ``pendingDelay``, then
    /// grows the delay (capped at ``maxDelay``) for the next attempt and flips
    /// the phase to `.reconnecting`.
    private mutating func scheduleReconnect() -> Action {
        let delay = pendingDelay
        let grown = pendingDelay * multiplier
        pendingDelay = grown > maxDelay ? maxDelay : grown
        phase = .reconnecting
        return .reconnect(since: cursor, after: delay)
    }

    // MARK: - Decoding

    /// Decodes a raw ``ServerEvent`` (the full `model.Event` envelope JSON) into
    /// a ``DecodedEvent``. Never throws or crashes: a malformed envelope falls
    /// back to the SSE `event:` name with a zero cursor, and an unrecognised type
    /// maps to ``DaemonEvent/unknown(type:)`` so the caller re-snapshots.
    public static func decode(_ raw: ServerEvent) -> DecodedEvent {
        let env = try? JSONDecoder().decode(Envelope.self, from: raw.jsonData)
        // Prefer the envelope's type; fall back to the SSE event name.
        let type = (env?.type).flatMap { $0.isEmpty ? nil : $0 } ?? raw.type
        let id = env?.id ?? 0
        let taskID = env?.taskID ?? ""
        let payload = env?.payload
        return DecodedEvent(id: id, event: mapEvent(type: type, taskID: taskID, payload: payload))
    }

    /// The `model.Event` wire envelope: `{id, type, at, task_id, payload}`. `at`
    /// is ignored. Payload travels as raw JSON (`JSONValue`) so each type reads
    /// only the fields it needs.
    struct Envelope: Decodable {
        var id: Int64?
        var type: String?
        var taskID: String?
        var payload: JSONValue?

        enum CodingKeys: String, CodingKey {
            case id, type, payload
            case taskID = "task_id"
        }
    }

    static func mapEvent(type: String, taskID: String, payload: JSONValue?) -> DaemonEvent {
        switch type {
        case "task.created":
            return .taskCreated(taskID: taskID,
                                name: payload?["name"]?.stringValue ?? "",
                                project: payload?["project"]?.stringValue ?? "",
                                status: payload?["status"]?.stringValue ?? "")
        case "task.status_changed":
            return .taskStatusChanged(taskID: taskID,
                                      from: payload?["from"]?.stringValue ?? "",
                                      to: payload?["to"]?.stringValue ?? "")
        case "task.completed":
            return .taskCompleted(taskID: taskID)
        case "task.archived":
            return .taskArchived(taskID: taskID)
        case "task.deleted":
            return .taskDeleted(taskID: taskID)
        case "task.renamed":
            return .taskRenamed(taskID: taskID,
                                from: payload?["from"]?.stringValue ?? "",
                                to: payload?["to"]?.stringValue ?? "")
        case "task.forked":
            return .taskForked(taskID: taskID,
                               fromTaskID: payload?["from_task_id"]?.stringValue ?? "",
                               toTaskID: payload?["to_task_id"]?.stringValue ?? taskID)
        case "message.sent":
            return .messageSent(taskID: taskID)
        case "message.acked":
            return .messageAcked(taskID: taskID)
        case "session.started":
            return .sessionStarted(taskID: taskID,
                                   pid: payload?["pid"]?.intValue ?? 0,
                                   resume: payload?["resume"]?.boolValue ?? false)
        case "session.exited":
            return .sessionExited(taskID: taskID,
                                  stopped: payload?["stopped"]?.boolValue ?? false,
                                  pendingRestart: payload?["pending_restart"]?.boolValue ?? false)
        case "session.idle":
            return .sessionIdle(taskID: taskID)
        case "session.needs_input":
            return .sessionNeedsInput(taskID: taskID,
                                      needsInput: payload?["needs_input"]?.boolValue ?? false)
        case "session.focus":
            return .sessionFocus(taskID: taskID,
                                 focused: payload?["focused"]?.boolValue ?? false)
        case "resync":
            return .resync(reason: payload?["reason"]?.stringValue ?? "")
        default:
            return .unknown(type: type)
        }
    }
}

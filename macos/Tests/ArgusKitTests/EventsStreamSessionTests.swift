import Foundation
import Testing
@testable import ArgusKit

/// Behavioral coverage for ``EventsStreamSession`` — the UI-free state machine
/// that syncs the task list off the daemon's `/api/events/stream` SSE channel
/// (ring-cursor bookkeeping, event decoding, incremental-vs-resnapshot decision,
/// reconnect/backoff policy). No network: inputs in, ``EventsStreamSession/Action``
/// values out.
@Suite("Events stream session")
struct EventsStreamSessionTests {

    // MARK: - Helpers

    /// Builds a ``ServerEvent`` carrying the full `model.Event` envelope JSON, as
    /// the daemon's `writeSSEEvent` marshals it (id/type/at/task_id/payload).
    private func serverEvent(id: Int64, type: String, taskID: String? = nil,
                             payload: String? = nil) -> ServerEvent {
        var obj = "{\"id\":\(id),\"type\":\"\(type)\""
        if let taskID { obj += ",\"task_id\":\"\(taskID)\"" }
        if let payload { obj += ",\"payload\":\(payload)" }
        obj += "}"
        return ServerEvent(type: type, jsonData: Data(obj.utf8))
    }

    // MARK: - start() / cursor seeding

    @Test("start opens the stream at cursor 0 and flips to connecting")
    func startOpensAtZero() {
        var session = EventsStreamSession()
        #expect(session.cursor == 0)
        #expect(session.phase == .idle)
        let actions = session.start()
        #expect(actions == [.openStream(since: 0)])
        #expect(session.phase == .connecting)
    }

    @Test("start after events reopens at the accumulated cursor (gapless resume)")
    func startReopensAtCursor() {
        var session = EventsStreamSession()
        _ = session.start()
        _ = session.handle(serverEvent(id: 7, type: "session.idle", taskID: "t1"))
        #expect(session.cursor == 7)
        // A reconnect re-drives start(): it resumes from the last-seen id.
        let actions = session.start()
        #expect(actions == [.openStream(since: 7)])
        #expect(session.phase == .connecting)
    }

    // MARK: - Cursor bookkeeping

    @Test("cursor advances to each real event id, marks traffic live")
    func cursorAdvancesPerEvent() {
        var session = EventsStreamSession()
        _ = session.start()

        _ = session.handle(serverEvent(id: 3, type: "session.idle", taskID: "a"))
        #expect(session.cursor == 3)
        #expect(session.phase == .live)

        _ = session.handle(serverEvent(id: 10, type: "session.idle", taskID: "a"))
        #expect(session.cursor == 10)
    }

    @Test("an out-of-order lower id never regresses the cursor")
    func cursorNeverRegresses() {
        var session = EventsStreamSession()
        _ = session.start()
        _ = session.handle(serverEvent(id: 20, type: "session.idle", taskID: "a"))
        #expect(session.cursor == 20)
        _ = session.handle(serverEvent(id: 5, type: "session.idle", taskID: "a"))
        #expect(session.cursor == 20)
    }

    @Test("the synthetic resync event (id 0) does not advance the cursor")
    func resyncDoesNotAdvanceCursor() {
        var session = EventsStreamSession()
        _ = session.start()
        _ = session.handle(serverEvent(id: 42, type: "session.idle", taskID: "a"))
        #expect(session.cursor == 42)
        let actions = session.handle(serverEvent(id: 0, type: "resync",
            payload: "{\"reason\":\"cursor_older_than_ring\",\"cursor\":1,\"oldest\":9}"))
        #expect(actions == [.resnapshot])
        #expect(session.cursor == 42) // unchanged
    }

    // MARK: - Incremental-vs-resnapshot decisions

    @Test("resync always re-snapshots")
    func resyncResnapshots() {
        var session = EventsStreamSession()
        let actions = session.handle(serverEvent(id: 0, type: "resync",
            payload: "{\"reason\":\"cursor_older_than_ring\"}"))
        #expect(actions == [.resnapshot])
    }

    @Test("an unknown event type re-snapshots rather than crashing")
    func unknownResnapshots() {
        var session = EventsStreamSession()
        let actions = session.handle(serverEvent(id: 5, type: "task.teleported", taskID: "x"))
        #expect(actions == [.resnapshot])
        // Still advances the cursor so a reconnect doesn't replay it forever.
        #expect(session.cursor == 5)
    }

    @Test("task.created and task.forked re-snapshot (event lacks the full task shape)")
    func createdAndForkedResnapshot() {
        var session = EventsStreamSession()
        let created = session.handle(serverEvent(id: 1, type: "task.created", taskID: "t1",
            payload: "{\"name\":\"foo\",\"project\":\"p\",\"status\":\"pending\"}"))
        #expect(created == [.resnapshot])
        let forked = session.handle(serverEvent(id: 2, type: "task.forked", taskID: "t2",
            payload: "{\"from_task_id\":\"t1\",\"to_task_id\":\"t2\"}"))
        #expect(forked == [.resnapshot])
    }

    @Test("task list mutations apply incrementally")
    func listMutationsApplyIncrementally() {
        var session = EventsStreamSession()

        #expect(session.handle(serverEvent(id: 1, type: "task.renamed", taskID: "t",
            payload: "{\"from\":\"old\",\"to\":\"new\"}"))
            == [.apply(.taskRenamed(taskID: "t", from: "old", to: "new"))])

        #expect(session.handle(serverEvent(id: 2, type: "task.status_changed", taskID: "t",
            payload: "{\"from\":\"pending\",\"to\":\"in_progress\"}"))
            == [.apply(.taskStatusChanged(taskID: "t", from: "pending", to: "in_progress"))])

        #expect(session.handle(serverEvent(id: 3, type: "task.completed", taskID: "t"))
            == [.apply(.taskCompleted(taskID: "t"))])

        #expect(session.handle(serverEvent(id: 4, type: "task.archived", taskID: "t"))
            == [.apply(.taskArchived(taskID: "t"))])

        #expect(session.handle(serverEvent(id: 5, type: "task.deleted", taskID: "t"))
            == [.apply(.taskDeleted(taskID: "t"))])
    }

    @Test("session events apply incrementally with decoded payloads")
    func sessionEventsApplyIncrementally() {
        var session = EventsStreamSession()

        #expect(session.handle(serverEvent(id: 1, type: "session.needs_input", taskID: "t",
            payload: "{\"needs_input\":true}"))
            == [.apply(.sessionNeedsInput(taskID: "t", needsInput: true))])

        #expect(session.handle(serverEvent(id: 2, type: "session.needs_input", taskID: "t",
            payload: "{\"needs_input\":false}"))
            == [.apply(.sessionNeedsInput(taskID: "t", needsInput: false))])

        #expect(session.handle(serverEvent(id: 3, type: "session.idle", taskID: "t"))
            == [.apply(.sessionIdle(taskID: "t"))])

        #expect(session.handle(serverEvent(id: 4, type: "session.started", taskID: "t",
            payload: "{\"pid\":123,\"resume\":true}"))
            == [.apply(.sessionStarted(taskID: "t", pid: 123, resume: true))])

        #expect(session.handle(serverEvent(id: 5, type: "session.exited", taskID: "t",
            payload: "{\"stopped\":true,\"err\":\"\",\"pending_restart\":false}"))
            == [.apply(.sessionExited(taskID: "t", stopped: true, pendingRestart: false))])

        #expect(session.handle(serverEvent(id: 6, type: "session.focus", taskID: "t",
            payload: "{\"focused\":true}"))
            == [.apply(.sessionFocus(taskID: "t", focused: true))])
    }

    // MARK: - Decoding robustness

    @Test("decode maps every known type and reads its payload fields")
    func decodeMapsKnownTypes() {
        #expect(EventsStreamSession.decode(serverEvent(id: 9, type: "task.renamed", taskID: "t",
            payload: "{\"from\":\"a\",\"to\":\"b\"}"))
            == DecodedEvent(id: 9, event: .taskRenamed(taskID: "t", from: "a", to: "b")))
    }

    @Test("missing payload fields decode to benign zero values")
    func decodeMissingFields() {
        // needs_input event with an empty payload → needsInput defaults false.
        #expect(EventsStreamSession.decode(serverEvent(id: 1, type: "session.needs_input",
            taskID: "t", payload: "{}"))
            == DecodedEvent(id: 1, event: .sessionNeedsInput(taskID: "t", needsInput: false)))
    }

    @Test("a malformed envelope falls back to the SSE event name with a zero cursor")
    func decodeMalformedEnvelope() {
        let raw = ServerEvent(type: "session.idle", jsonData: Data("{not json".utf8))
        let decoded = EventsStreamSession.decode(raw)
        #expect(decoded.id == 0)
        #expect(decoded.event == .sessionIdle(taskID: ""))
    }

    @Test("an empty/garbage frame decodes to unknown, never crashes")
    func decodeGarbageIsUnknown() {
        let raw = ServerEvent(type: "", jsonData: Data("garbage".utf8))
        let decoded = EventsStreamSession.decode(raw)
        #expect(decoded.event == .unknown(type: ""))
        // And handling it re-snapshots rather than throwing.
        var session = EventsStreamSession()
        #expect(session.handle(decoded) == [.resnapshot])
    }

    @Test("taskID accessor returns the scoped id (empty for resync)")
    func taskIDAccessor() {
        #expect(DaemonEvent.taskDeleted(taskID: "t").taskID == "t")
        #expect(DaemonEvent.resync(reason: "x").taskID == "")
    }

    // MARK: - Reconnect / backoff policy (parity with TerminalStreamSession)

    @Test("first unhealthy close reconnects from the cursor at baseDelay")
    func firstReconnectUsesBaseDelay() {
        var session = EventsStreamSession(baseDelay: .milliseconds(250),
                                          maxDelay: .seconds(4), multiplier: 2.0)
        _ = session.start()
        _ = session.handle(serverEvent(id: 11, type: "session.idle", taskID: "a"))
        let action = session.streamClosed()
        #expect(action == .reconnect(since: 11, after: .milliseconds(250)))
        #expect(session.phase == .reconnecting)
    }

    @Test("close while a reconnect is already pending does not double-book")
    func noDoubleReconnect() {
        var session = EventsStreamSession(baseDelay: .milliseconds(100))
        _ = session.start()
        #expect(session.streamClosed() == .reconnect(since: 0, after: .milliseconds(100)))
        #expect(session.phase == .reconnecting)
        // The old stream winding down must not schedule a second reconnect.
        #expect(session.streamClosed() == nil)
        #expect(session.phase == .reconnecting)
    }

    @Test("failed reconnect attempts keep retrying with grown, capped backoff; healthy traffic resets it")
    func failedAttemptsKeepRetryingWithGrowingBackoff() {
        var session = EventsStreamSession(baseDelay: .milliseconds(100),
                                          maxDelay: .milliseconds(500), multiplier: 2.0)
        _ = session.start()

        #expect(session.streamClosed() == .reconnect(since: 0, after: .milliseconds(100)))
        #expect(session.streamClosed() == nil) // pending; not re-armed yet

        // The scheduled attempt begins dialing, then fails → next grown delay.
        session.streamOpening()
        #expect(session.phase == .connecting)
        #expect(session.streamClosed(error: ArgusError.invalidResponse("down"))
                == .reconnect(since: 0, after: .milliseconds(200)))

        session.streamOpening()
        #expect(session.streamClosed() == .reconnect(since: 0, after: .milliseconds(400)))
        session.streamOpening()
        #expect(session.streamClosed() == .reconnect(since: 0, after: .milliseconds(500)))
        session.streamOpening()
        #expect(session.streamClosed() == .reconnect(since: 0, after: .milliseconds(500))) // capped

        // Healthy traffic resets the schedule back to baseDelay.
        session.streamOpening()
        _ = session.handle(serverEvent(id: 1, type: "session.idle", taskID: "a"))
        #expect(session.phase == .live)
        #expect(session.streamClosed() == .reconnect(since: 1, after: .milliseconds(100)))
    }

    @Test("streamOpening is a no-op outside the reconnecting phase")
    func streamOpeningNoOpOutsideReconnecting() {
        var session = EventsStreamSession()
        session.streamOpening() // idle
        #expect(session.phase == .idle)
        _ = session.start()
        session.streamOpening() // connecting
        #expect(session.phase == .connecting)
    }

    @Test("a resync-driven close still reconnects from the preserved cursor")
    func resyncThenClose() {
        var session = EventsStreamSession(baseDelay: .milliseconds(50))
        _ = session.start()
        _ = session.handle(serverEvent(id: 30, type: "session.idle", taskID: "a"))
        _ = session.handle(serverEvent(id: 0, type: "resync", payload: "{\"reason\":\"x\"}"))
        // Resync did not touch the cursor; a subsequent drop resumes from 30.
        #expect(session.streamClosed() == .reconnect(since: 30, after: .milliseconds(50)))
    }
}

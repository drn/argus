import Foundation
import Testing
@testable import ArgusKit

/// Behavioral coverage for ``TerminalStreamSession`` — the UI-free state
/// machine that drives the live terminal stream (offset bookkeeping, event
/// interpretation, reconnect/backoff policy). No network, no terminal
/// emulator: everything is inputs in, ``TerminalStreamSession/Action`` values
/// out.
@Suite("Terminal stream session")
struct TerminalStreamSessionTests {

    private func bytes(_ s: String) -> Data { Data(s.utf8) }

    // MARK: - start() / offset seeding

    @Test("start with no tail data seeds offset and only opens the stream")
    func startNoTail() {
        var session = TerminalStreamSession()
        let actions = session.start(tailTotal: 42)
        #expect(actions == [.openStream(since: 42)])
        #expect(session.offset == 42)
        #expect(session.phase == .connecting)
    }

    @Test("start with tail data feeds it before opening the stream")
    func startWithTail() {
        var session = TerminalStreamSession()
        let tail = bytes("replayed output")
        let actions = session.start(tailTotal: 100, tailData: tail)
        #expect(actions == [.feed(tail), .openStream(since: 100)])
        #expect(session.offset == 100)
        #expect(session.phase == .connecting)
    }

    @Test("offset starts at zero before start() is ever called")
    func offsetDefaultsToZero() {
        let session = TerminalStreamSession()
        #expect(session.offset == 0)
        #expect(session.phase == .idle)
    }

    @Test("restart re-seeds the offset from a fresh tail, discarding the old cursor")
    func restartReseedsOffset() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 10)
        _ = session.handle(.output(bytes("12345"))) // offset -> 15
        #expect(session.offset == 15)

        // A Restart re-fetches the tail and re-drives start() with the fresh total.
        let actions = session.start(tailTotal: 500)
        #expect(session.offset == 500)
        #expect(session.phase == .connecting)
        #expect(actions == [.openStream(since: 500)])
    }

    // MARK: - Offset bookkeeping across frames

    @Test("offset advances by exactly the decoded byte count per frame")
    func offsetAdvancesByFrameSize() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)

        _ = session.handle(.output(bytes("hello"))) // 5 bytes
        #expect(session.offset == 5)

        _ = session.handle(.output(bytes(" world!"))) // 7 bytes
        #expect(session.offset == 12)

        _ = session.handle(.output(Data())) // empty frame is a no-op on the cursor
        #expect(session.offset == 12)
    }

    @Test("reconnect after a stream drop resumes from the exact accumulated offset (no gap, no overlap)")
    func reconnectResumesFromCurrentOffset() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 1_000)
        _ = session.handle(.output(bytes("abcde"))) // offset -> 1005
        _ = session.handle(.output(bytes("fg")))    // offset -> 1007

        let action = session.streamClosed()
        #expect(action == .reconnect(since: 1007, after: .milliseconds(500)))
        #expect(session.offset == 1007)
    }

    // MARK: - Event interpretation: output

    @Test("output event yields a feed action with the exact bytes and flips phase to live")
    func handleOutputFeedsExactBytes() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)
        let payload = bytes("\u{1B}[2Jsome pty bytes")
        let actions = session.handle(.output(payload))
        #expect(actions == [.feed(payload)])
        #expect(session.phase == .live)
    }

    // MARK: - Event interpretation: connected

    @Test("connected while connecting resolves to live with no actions (idle-agent spinner fix)")
    func connectedWhileConnectingGoesLive() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)
        #expect(session.phase == .connecting)
        // An open-but-silent stream: no `.output` will ever arrive, yet the UI
        // must leave the "connecting" spinner the moment the channel is up.
        let actions = session.handle(.connected)
        #expect(actions == [])
        #expect(session.phase == .live)
    }

    @Test("connected while reconnecting resolves to live with no actions")
    func connectedWhileReconnectingGoesLive() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)
        _ = session.streamClosed()          // -> .reconnecting
        #expect(session.phase == .reconnecting)
        // A connected can land while still nominally reconnecting (e.g. a
        // rerender-exit reconnect whose retry validated before any frame).
        let actions = session.handle(.connected)
        #expect(actions == [])
        #expect(session.phase == .live)
    }

    @Test("connected resets the backoff to baseDelay (an accepted connection is healthy)")
    func connectedResetsBackoff() {
        var session = TerminalStreamSession(baseDelay: .milliseconds(100),
                                            maxDelay: .seconds(5),
                                            multiplier: 2.0)
        _ = session.start(tailTotal: 0)
        // A failed dial grows the internal pending delay.
        #expect(session.streamClosed() == .reconnect(since: 0, after: .milliseconds(100)))
        session.streamOpening()             // -> .connecting
        // This attempt's response validates (connected) with no frames: the
        // healthy connection must reset the schedule back to baseDelay.
        _ = session.handle(.connected)
        #expect(session.phase == .live)
        // A subsequent independent drop reconnects promptly at baseDelay, proving
        // the grown delay was discarded.
        #expect(session.streamClosed() == .reconnect(since: 0, after: .milliseconds(100)))
    }

    @Test("connected after the session ended stays ended (never revives a dead session)")
    func connectedAfterEndedStaysEnded() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)
        _ = session.handle(.exit(rerendering: false))
        #expect(session.phase == .ended)
        let actions = session.handle(.connected)
        #expect(actions == [])
        #expect(session.phase == .ended)
    }

    @Test("tail-then-connected-then-frames: connected leaves the offset untouched")
    func connectedDoesNotAffectOffset() {
        var session = TerminalStreamSession()
        // Seed a tail cursor, then connected (no frame), then a live frame.
        let tail = bytes("scrollback tail")
        _ = session.start(tailTotal: 100, tailData: tail)
        #expect(session.offset == 100)

        _ = session.handle(.connected)
        #expect(session.phase == .live)
        #expect(session.offset == 100) // connected advances nothing

        _ = session.handle(.output(bytes("live")))
        #expect(session.offset == 104) // only real bytes advance the cursor
    }

    // MARK: - Event interpretation: exit

    @Test("exit(rerendering: true) reconnects from the current offset and stays reconnecting")
    func exitRerenderingTrueReconnects() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)
        _ = session.handle(.output(bytes("hi"))) // offset -> 2, phase live

        let actions = session.handle(.exit(rerendering: true))
        #expect(actions == [.reconnect(since: 2, after: .milliseconds(500))])
        #expect(session.phase == .reconnecting)
    }

    @Test("exit(rerendering: false) ends the session")
    func exitRerenderingFalseEnds() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)
        _ = session.handle(.output(bytes("hi")))

        let actions = session.handle(.exit(rerendering: false))
        #expect(actions == [.ended])
        #expect(session.phase == .ended)
    }

    @Test("exit-driven reconnect always uses baseDelay (prompt first retry), never a carried-over grown delay")
    func exitReconnectAlwaysUsesBaseDelay() {
        var session = TerminalStreamSession(baseDelay: .milliseconds(100),
                                            maxDelay: .seconds(5),
                                            multiplier: 3.0)
        _ = session.start(tailTotal: 0)
        // First unhealthy drop grows the internal pending delay...
        _ = session.streamClosed()
        // ...but a subsequent rerender exit must still reconnect promptly at
        // baseDelay, not at whatever the backoff had grown to.
        let action = session.handle(.exit(rerendering: true))
        #expect(action == [.reconnect(since: 0, after: .milliseconds(100))])
    }

    // MARK: - Event interpretation: clipboard

    @Test("clipboard event with text surfaces the exact decoded payload")
    func clipboardTextSurfacesPayload() {
        var session = TerminalStreamSession()
        let actions = session.handle(.clipboard(text: "copied text", cleared: false))
        #expect(actions == [.clipboard(text: "copied text")])
    }

    @Test("clipboard cleared surfaces a nil payload regardless of stale text")
    func clipboardClearedSurfacesNil() {
        var session = TerminalStreamSession()
        // A cleared event may still carry stale text alongside `cleared: true`
        // (mirrors the server's encodeClipboardEvent(_, cleared) shape) — the
        // session must treat `cleared` as authoritative.
        let actions = session.handle(.clipboard(text: "stale", cleared: true))
        #expect(actions == [.clipboard(text: nil)])
    }

    // MARK: - Malformed frames (decoded upstream by ArgusClient.mapTerminalEvent)

    @Test("malformed base64 output frame decodes to nil and is simply skipped upstream")
    func malformedBase64SkippedUpstream() {
        // The session never even sees this frame — mapTerminalEvent swallows
        // it — so there is nothing for the session to crash on.
        let mapped = ArgusClient.mapTerminalEvent(SSEvent(name: nil, data: "not-valid-base64!!!"))
        #expect(mapped == nil)
    }

    @Test("malformed exit JSON decodes to a non-rerendering exit (ends cleanly, no crash)")
    func malformedExitJSONDefaultsToEnded() {
        let mapped = ArgusClient.mapTerminalEvent(SSEvent(name: "exit", data: "not json at all"))
        #expect(mapped == .exit(rerendering: false))

        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)
        let actions = session.handle(mapped!)
        #expect(actions == [.ended])
        #expect(session.phase == .ended)
    }

    @Test("malformed clipboard JSON decodes to a cleared payload (no crash)")
    func malformedClipboardJSONDefaultsToCleared() {
        let mapped = ArgusClient.mapTerminalEvent(SSEvent(name: "clipboard", data: "{not json"))
        #expect(mapped == .clipboard(text: nil, cleared: true))

        var session = TerminalStreamSession()
        let actions = session.handle(mapped!)
        #expect(actions == [.clipboard(text: nil)])
    }

    @Test("ping / comment SSE lines never reach the session (SSEParser drops them before dispatch)")
    func pingCommentsNeverDispatch() {
        var parser = SSEParser()
        // A raw `:` comment line (the server's 30s keepalive) never dispatches.
        #expect(parser.feed(": keepalive") == nil)
        // Followed by a real frame on the same "turn" — still parses cleanly.
        #expect(parser.feed("data: aGVsbG8=") == nil)
        let ev = parser.feed("")
        #expect(ev == SSEvent(name: nil, data: "aGVsbG8="))
    }

    // MARK: - Backoff policy

    @Test("first unhealthy reconnect (no prior exit) uses baseDelay")
    func firstReconnectUsesBaseDelay() {
        var session = TerminalStreamSession(baseDelay: .milliseconds(250),
                                            maxDelay: .seconds(4),
                                            multiplier: 2.0)
        _ = session.start(tailTotal: 0)
        let action = session.streamClosed()
        #expect(action == .reconnect(since: 0, after: .milliseconds(250)))
        #expect(session.phase == .reconnecting)
    }

    @Test("backoff resets to baseDelay after healthy traffic, so the next drop is prompt again")
    func backoffResetsAfterHealthyTraffic() {
        var session = TerminalStreamSession(baseDelay: .milliseconds(100),
                                            maxDelay: .seconds(2),
                                            multiplier: 2.0)
        _ = session.start(tailTotal: 0)

        // Unhealthy: schedule a reconnect (this grows the *internal* pending
        // delay for a hypothetical next attempt).
        _ = session.streamClosed()
        #expect(session.phase == .reconnecting)

        // The reconnect attempt succeeds and delivers live output.
        let feedActions = session.handle(.output(bytes("back online")))
        #expect(feedActions == [.feed(bytes("back online"))])
        #expect(session.phase == .live)

        // A later, independent drop must reconnect at baseDelay again, not at
        // whatever the backoff had grown to before the healthy traffic.
        let action = session.streamClosed()
        #expect(action == .reconnect(since: 11, after: .milliseconds(100)))
    }

    @Test("streamClosed while idle/connecting/live schedules a reconnect from the current offset")
    func streamClosedFromEachPreReconnectPhaseSchedulesReconnect() {
        // .idle
        var idleSession = TerminalStreamSession(baseDelay: .milliseconds(50))
        #expect(idleSession.phase == .idle)
        #expect(idleSession.streamClosed() == .reconnect(since: 0, after: .milliseconds(50)))

        // .connecting
        var connectingSession = TerminalStreamSession(baseDelay: .milliseconds(50))
        _ = connectingSession.start(tailTotal: 7)
        #expect(connectingSession.phase == .connecting)
        #expect(connectingSession.streamClosed() == .reconnect(since: 7, after: .milliseconds(50)))

        // .live
        var liveSession = TerminalStreamSession(baseDelay: .milliseconds(50))
        _ = liveSession.start(tailTotal: 0)
        _ = liveSession.handle(.output(bytes("x")))
        #expect(liveSession.phase == .live)
        #expect(liveSession.streamClosed() == .reconnect(since: 1, after: .milliseconds(50)))
    }

    @Test("streamClosed while already ended is a no-op (a late/duplicate stream-drop signal after real end)")
    func streamClosedWhileEndedIsNoOp() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)
        _ = session.handle(.exit(rerendering: false))
        #expect(session.phase == .ended)

        #expect(session.streamClosed() == nil)
        #expect(session.streamClosed(error: ArgusError.invalidResponse("boom")) == nil)
        #expect(session.phase == .ended)
    }

    @Test("streamClosed for the stream that just delivered a rerender exit is suppressed (no double reconnect)")
    func streamClosedAfterRerenderExitDoesNotDoubleReconnect() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)

        let exitActions = session.handle(.exit(rerendering: true))
        #expect(exitActions == [.reconnect(since: 0, after: .milliseconds(500))])
        #expect(session.phase == .reconnecting)

        // The very stream that delivered the exit is now finishing (its
        // `for try await` loop ends). Because a reconnect is already
        // scheduled, this must NOT emit a second one.
        #expect(session.streamClosed() == nil)
    }

    @Test("a stream failure with the reconnect still PENDING is swallowed; once the attempt opens, failures keep retrying with grown, capped backoff")
    func failedReconnectAttemptsKeepRetryingWithGrowingBackoff() {
        var session = TerminalStreamSession(baseDelay: .milliseconds(100),
                                            maxDelay: .milliseconds(500),
                                            multiplier: 2.0)
        _ = session.start(tailTotal: 0)

        let first = session.streamClosed()
        #expect(first == .reconnect(since: 0, after: .milliseconds(100)))
        #expect(session.phase == .reconnecting)

        // A close arriving while the reconnect is still pending (the OLD
        // stream winding down) must not double-book a second reconnect.
        #expect(session.streamClosed() == nil)
        #expect(session.phase == .reconnecting)

        // The scheduled attempt begins dialing (streamOpening), then fails:
        // that must schedule the NEXT attempt with the grown delay — a daemon
        // outage longer than one retry window must not stop reconnecting.
        session.streamOpening()
        #expect(session.phase == .connecting)
        let second = session.streamClosed(error: ArgusError.invalidResponse("still down"))
        #expect(second == .reconnect(since: 0, after: .milliseconds(200)))

        // Growth saturates at maxDelay across further failed attempts...
        session.streamOpening()
        #expect(session.streamClosed() == .reconnect(since: 0, after: .milliseconds(400)))
        session.streamOpening()
        #expect(session.streamClosed() == .reconnect(since: 0, after: .milliseconds(500)))
        session.streamOpening()
        #expect(session.streamClosed() == .reconnect(since: 0, after: .milliseconds(500)))

        // ...and healthy traffic resets the schedule back to baseDelay.
        session.streamOpening()
        _ = session.handle(.output(Data("ok".utf8)))
        #expect(session.phase == .live)
        #expect(session.streamClosed() == .reconnect(since: 2, after: .milliseconds(100)))
    }

    // MARK: - Edge cases

    @Test("a duplicate exit(rerendering: false) after the session already ended stays ended, no crash")
    func doubleExitStaysEnded() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)

        #expect(session.handle(.exit(rerendering: false)) == [.ended])
        #expect(session.phase == .ended)

        // A duplicate/late exit frame (e.g. a retried SSE delivering the same
        // terminal event twice) must not crash and must report ended again.
        #expect(session.handle(.exit(rerendering: false)) == [.ended])
        #expect(session.phase == .ended)
    }

    @Test("two rerendering exits in a row each reconnect promptly at baseDelay")
    func doubleRerenderExitEachReconnects() {
        var session = TerminalStreamSession(baseDelay: .milliseconds(75))
        _ = session.start(tailTotal: 0)

        #expect(session.handle(.exit(rerendering: true)) == [.reconnect(since: 0, after: .milliseconds(75))])
        #expect(session.phase == .reconnecting)

        // A second kick-restart fires before the first ever reconnected
        // (e.g. two rapid resizes each triggering KickRerender).
        #expect(session.handle(.exit(rerendering: true)) == [.reconnect(since: 0, after: .milliseconds(75))])
        #expect(session.phase == .reconnecting)
    }

    @Test("a frame arriving after the session ended still advances the offset and revives phase to live")
    func frameAfterEndedRevivesPhase() {
        // Documents current behavior: `.output` is handled unconditionally,
        // regardless of phase. A stray/buffered frame delivered after an
        // `ended` exit (e.g. a slow SSE tail on a channel that raced the
        // exit event) advances the cursor and flips the phase back to
        // `.live` rather than being ignored.
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)
        _ = session.handle(.exit(rerendering: false))
        #expect(session.phase == .ended)

        let actions = session.handle(.output(bytes("late bytes")))
        #expect(actions == [.feed(bytes("late bytes"))])
        #expect(session.phase == .live)
        #expect(session.offset == UInt64(bytes("late bytes").count))
    }

    @Test("clipboard events never touch phase or offset")
    func clipboardDoesNotAffectPhaseOrOffset() {
        var session = TerminalStreamSession()
        _ = session.start(tailTotal: 0)
        _ = session.handle(.output(bytes("abc")))
        let phaseBefore = session.phase
        let offsetBefore = session.offset

        _ = session.handle(.clipboard(text: "hi", cleared: false))
        #expect(session.phase == phaseBefore)
        #expect(session.offset == offsetBefore)
    }
}

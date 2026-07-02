import Foundation

/// A pure, UI-free state machine for driving a live terminal stream over the
/// argus daemon's SSE API. It owns the byte-offset bookkeeping, the
/// ``TerminalEvent`` interpretation, and the reconnect/backoff policy so the
/// whole streaming protocol can be unit-tested without a network or a terminal
/// emulator.
///
/// The value semantics are deliberate: feed it inputs (``start(tailTotal:tailData:)``,
/// ``handle(_:)``, ``streamClosed(error:)``) and it returns ``Action`` values
/// the caller executes (feed bytes to the emulator, (re)open the SSE stream,
/// write the clipboard, show the session-ended state). It never performs I/O.
///
/// ## Protocol replicated (matches `internal/api/handlers.go` + the web SPA)
/// 1. GET `/output` → `X-Output-Total: N` (the byte cursor) + the tail bytes.
///    Call ``start(tailTotal:tailData:)`` with those: it seeds ``offset`` = N,
///    emits `.feed(tail)` then `.openStream(since: N)`.
/// 2. Open `/stream?since=N`. Unnamed frames are base64 PTY bytes → `.feed`,
///    advancing ``offset`` by the byte count for a gapless resume.
/// 3. `exit {"rerendering":true}` → a kick-restart is in flight: reconnect from
///    the current ``offset`` (with backoff, since the new session may 404 for a
///    beat while it spawns).
/// 4. `exit {"rerendering":false}` (or `{}`) → the session ended: `.ended`.
/// 5. A stream that drops WITHOUT an `exit` (network blip, daemon bounce) →
///    reconnect from ``offset`` with exponential backoff.
/// 6. `clipboard` → `.clipboard(text:)` (nil == cleared).
public struct TerminalStreamSession: Sendable, Equatable {

    // MARK: - Backoff policy (injectable so tests can pin the schedule)

    /// The delay before the first reconnect attempt (and the value the backoff
    /// resets to on healthy traffic / an expected rerender).
    public var baseDelay: Duration
    /// The ceiling the exponential backoff saturates at.
    public var maxDelay: Duration
    /// The per-attempt growth factor applied to the pending delay.
    public var multiplier: Double

    // MARK: - Observable state

    /// The lifecycle phase, for the UI to render (spinner / live / reconnecting
    /// / ended). Purely derived from the inputs fed so far.
    public enum Phase: Sendable, Equatable {
        /// Nothing started yet.
        case idle
        /// A stream open is pending or in-flight; no bytes seen yet.
        case connecting
        /// Live output is flowing.
        case live
        /// Waiting out a backoff delay before the next reconnect.
        case reconnecting
        /// The session exited for good (non-rerender). Terminal, until an
        /// external Restart re-drives ``start(tailTotal:tailData:)``.
        case ended
    }

    /// The absolute byte cursor into the task's output log. Seeded from
    /// `X-Output-Total`, advanced by every fed frame; passed back as `since`
    /// on reconnect for a gapless resume.
    public private(set) var offset: UInt64 = 0
    public private(set) var phase: Phase = .idle

    /// The delay the *next* reconnect will use; grows on each unhealthy
    /// reconnect, resets to ``baseDelay`` on healthy traffic.
    private var pendingDelay: Duration

    public init(baseDelay: Duration = .milliseconds(500),
                maxDelay: Duration = .seconds(10),
                multiplier: Double = 2.0) {
        self.baseDelay = baseDelay
        self.maxDelay = maxDelay
        self.multiplier = multiplier
        self.pendingDelay = baseDelay
    }

    // MARK: - Actions the caller executes

    public enum Action: Sendable, Equatable {
        /// Write raw PTY bytes to the terminal emulator.
        case feed(Data)
        /// Open the SSE stream at `GET /stream?since=<offset>`.
        case openStream(since: UInt64)
        /// Re-open the SSE stream, but only after waiting `after`.
        case reconnect(since: UInt64, after: Duration)
        /// The session ended (non-rerender). Show the ended / Restart state.
        case ended
        /// Agent-staged clipboard changed. `text == nil` means it was cleared.
        case clipboard(text: String?)
    }

    // MARK: - Inputs

    /// Begins (or restarts) streaming from a freshly-fetched output tail. Seeds
    /// the cursor to `tailTotal`, resets the backoff, feeds any tail bytes, then
    /// opens the stream. Call this both for the initial attach and after a
    /// Restart (`POST /restart`) re-fetches the tail.
    public mutating func start(tailTotal: UInt64, tailData: Data = Data()) -> [Action] {
        offset = tailTotal
        pendingDelay = baseDelay
        phase = .connecting
        var actions: [Action] = []
        if !tailData.isEmpty { actions.append(.feed(tailData)) }
        actions.append(.openStream(since: tailTotal))
        return actions
    }

    /// Interprets one decoded ``TerminalEvent`` from the live stream.
    public mutating func handle(_ event: TerminalEvent) -> [Action] {
        switch event {
        case .output(let data):
            // Healthy traffic: advance the cursor and reset the backoff so a
            // later drop reconnects promptly.
            offset &+= UInt64(data.count)
            pendingDelay = baseDelay
            phase = .live
            return [.feed(data)]

        case .exit(let rerendering):
            if rerendering {
                // Kick-restart in flight. The replacement session may 404 for a
                // beat while it spawns, so reconnect through the backoff path
                // (reset first for a prompt first retry). The stream that
                // delivered this exit is about to finish; streamClosed() will
                // see .reconnecting and stay quiet — no double reconnect.
                pendingDelay = baseDelay
                return [scheduleReconnect()]
            }
            phase = .ended
            return [.ended]

        case .clipboard(let text, let cleared):
            return [.clipboard(text: cleared ? nil : text)]
        }
    }

    /// Signals that a scheduled reconnect attempt is now actually opening the
    /// stream (the backoff delay elapsed and the caller is dialing). Flips
    /// `.reconnecting` → `.connecting` so that a FAILURE of this attempt
    /// re-enters ``streamClosed(error:)``'s backoff path with the grown delay
    /// instead of being swallowed — otherwise a daemon outage longer than one
    /// retry window would stop reconnecting forever. Call it at the top of
    /// every stream-consume attempt; it is a no-op in every other phase.
    public mutating func streamOpening() {
        if phase == .reconnecting { phase = .connecting }
    }

    /// Signals that the SSE stream finished (cleanly or with `error`) WITHOUT an
    /// `exit` event having driven the phase to `.ended`/`.reconnecting`. Returns
    /// a reconnect action, or nil when no reconnect is warranted (the session
    /// already ended, or a rerender reconnect is already scheduled and its
    /// attempt has not begun — the old stream's own close must not double-book
    /// a reconnect; once the attempt begins, ``streamOpening()`` re-arms this).
    ///
    /// `error` is accepted for the caller's logging; the reconnect decision is
    /// phase-based, identical for a clean finish and a transport failure.
    public mutating func streamClosed(error: Error? = nil) -> Action? {
        switch phase {
        case .ended, .reconnecting:
            // Terminal, or a reconnect is already pending (rerender path).
            return nil
        case .idle, .connecting, .live:
            return scheduleReconnect()
        }
    }

    // MARK: - Backoff helper

    /// Emits a reconnect for the current cursor after ``pendingDelay``, then
    /// grows the delay (capped at ``maxDelay``) for the next attempt and flips
    /// the phase to `.reconnecting`.
    private mutating func scheduleReconnect() -> Action {
        let delay = pendingDelay
        let grown = pendingDelay * multiplier
        pendingDelay = grown > maxDelay ? maxDelay : grown
        phase = .reconnecting
        return .reconnect(since: offset, after: delay)
    }
}

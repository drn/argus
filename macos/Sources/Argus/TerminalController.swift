import AppKit
import ArgusKit
import Foundation
import Observation
import SwiftTerm
import os

/// Per-task controller binding a pure ``ArgusKit/TerminalStreamSession`` to a
/// live SwiftTerm ``SwiftTerm/TerminalView`` and the argus daemon's REST + SSE
/// endpoints. Cached by task ID in ``AppState`` so switching tasks in the
/// sidebar and back preserves scrollback (the same `TerminalView` and its
/// buffer are reused, and a dropped stream resumes from the tracked offset).
///
/// Responsibilities:
/// - Initial attach: GET `/size` → resize the emulator, GET `/output` → feed
///   the tail, then open `/stream?since=<total>` for a gapless live feed.
/// - Interpret stream events through the state machine (feed / reconnect /
///   ended / clipboard) and reflect the phase in ``viewState``.
/// - User input → POST `/input` (FIFO-ordered, chunked at 64 KiB).
/// - Geometry-driven resize → debounced POST `/resize`.
/// - Agent-staged clipboard (SSE `clipboard` + terminal OSC 52) →
///   `NSPasteboard` + a transient toast.
@MainActor
@Observable
final class TerminalController {

    /// What the Terminal tab should render.
    enum ViewState: Equatable {
        /// Initial connect in flight; no bytes yet.
        case connecting
        /// Live output flowing.
        case live
        /// Stream dropped; auto-reconnecting with backoff (terminal stays up).
        case reconnecting
        /// The session ended for good — offer Restart.
        case ended
        /// The initial attach failed hard — offer Retry.
        case error(String)
    }

    // MARK: - Observable UI state

    private(set) var viewState: ViewState = .connecting
    /// Non-nil while a "copied to clipboard" toast should show.
    private(set) var clipboardToast: String?

    // MARK: - Collaborators

    let taskID: String
    /// The reused SwiftTerm view. `@ObservationIgnored` — it is not a value the
    /// UI diffs on; it is embedded once via ``TerminalSurface``.
    @ObservationIgnored let terminalView: TerminalView

    @ObservationIgnored private let client: ArgusClient
    @ObservationIgnored private let coordinator = TerminalCoordinator()
    @ObservationIgnored private var session = TerminalStreamSession()

    // MARK: - Task bookkeeping

    @ObservationIgnored private var started = false
    @ObservationIgnored private var initialTask: _Concurrency.Task<Void, Never>?
    @ObservationIgnored private var streamTask: _Concurrency.Task<Void, Never>?
    @ObservationIgnored private var resizeTask: _Concurrency.Task<Void, Never>?
    @ObservationIgnored private var toastTask: _Concurrency.Task<Void, Never>?
    @ObservationIgnored private var inputPump: _Concurrency.Task<Void, Never>?
    @ObservationIgnored private let inputContinuation: AsyncStream<Data>.Continuation
    @ObservationIgnored private var lastSentSize: (cols: Int, rows: Int)?

    private static let log = Logger(subsystem: "com.argus.mac", category: "terminal")

    init(taskID: String, client: ArgusClient) {
        self.taskID = taskID
        self.client = client
        self.terminalView = FocusTakingTerminalView(frame: CGRect(x: 0, y: 0, width: 640, height: 400))

        var cont: AsyncStream<Data>.Continuation!
        let stream = AsyncStream<Data> { cont = $0 }
        self.inputContinuation = cont

        coordinator.controller = self
        terminalView.terminalDelegate = coordinator
        configureAppearance()

        inputPump = _Concurrency.Task { [weak self] in
            await self?.runInputPump(stream)
        }
    }

    private func configureAppearance() {
        terminalView.font = NSFont.monospacedSystemFont(ofSize: 12, weight: .regular)
        // Match the OS light/dark appearance for fg/bg.
        terminalView.configureNativeColors()
        terminalView.translatesAutoresizingMaskIntoConstraints = false
    }

    // MARK: - Lifecycle

    /// Called when the Terminal tab appears. Starts the initial connect exactly
    /// once; subsequent appears are no-ops so scrollback and the live stream
    /// survive tab switches.
    func attach() {
        guard !started else { return }
        started = true
        beginInitial()
    }

    /// Restart affordance shown in the `.ended` state: re-spawn the agent
    /// session via POST `/restart`, then re-run the initial attach so the fresh
    /// output tail streams from scratch.
    func restart() {
        viewState = .connecting
        initialTask?.cancel()
        initialTask = _Concurrency.Task { [weak self] in
            guard let self else { return }
            do {
                _ = try await self.client.restartTask(id: self.taskID)
            } catch {
                Self.log.error("[terminal] restart failed task=\(self.taskID, privacy: .public) err=\(String(describing: error), privacy: .public)")
                self.viewState = .error(AppState.describe(error))
                return
            }
            Self.log.info("[terminal] restart ok task=\(self.taskID, privacy: .public)")
            self.runInitialConnect()
        }
    }

    /// Retry affordance for the `.error` state: re-run the initial attach.
    func retry() {
        viewState = .connecting
        beginInitial()
    }

    /// Immediately reconnect the live stream, bypassing the backoff wait (the
    /// "Reconnect now" affordance in the reconnecting banner).
    func reconnectNow() {
        openStream(since: session.offset, after: nil)
    }

    private func beginInitial() {
        viewState = .connecting
        initialTask?.cancel()
        initialTask = _Concurrency.Task { [weak self] in
            self?.runInitialConnect()
        }
    }

    /// GET `/size` → resize emulator, GET `/output` → feed tail, open stream.
    /// Sizing precedes the tail feed so replayed content lays out at the PTY's
    /// width and doesn't reflow. Not a `Task` itself — call from one.
    private func runInitialConnect() {
        initialTask?.cancel()
        initialTask = _Concurrency.Task { [weak self] in
            guard let self else { return }
            // Size first (best-effort) so the tail replays without reflow.
            if let size = try? await self.client.terminalSize(taskID: self.taskID),
               size.cols > 0, size.rows > 0 {
                self.terminalView.resize(cols: size.cols, rows: size.rows)
            }
            do {
                let tail = try await self.client.output(taskID: self.taskID)
                Self.log.info("[terminal] tail task=\(self.taskID, privacy: .public) bytes=\(tail.data.count) total=\(tail.total) source=\(tail.source, privacy: .public)")
                self.apply(self.session.start(tailTotal: tail.total, tailData: tail.data))
            } catch {
                Self.log.error("[terminal] tail failed task=\(self.taskID, privacy: .public) err=\(String(describing: error), privacy: .public)")
                self.viewState = .error(AppState.describe(error))
            }
        }
    }

    /// Cancels every in-flight task. Called by ``AppState`` when the task is
    /// deleted or the client is rebuilt.
    func teardown() {
        initialTask?.cancel()
        streamTask?.cancel()
        resizeTask?.cancel()
        toastTask?.cancel()
        inputPump?.cancel()
        inputContinuation.finish()
    }

    // MARK: - Streaming

    private func openStream(since: UInt64, after delay: Duration?) {
        streamTask?.cancel()
        streamTask = _Concurrency.Task { [weak self] in
            if let delay {
                try? await _Concurrency.Task.sleep(for: delay)
            }
            if _Concurrency.Task.isCancelled { return }
            await self?.consume(since: since)
        }
    }

    private func consume(since: UInt64) async {
        // Mark a scheduled reconnect attempt as in-flight so a failure of THIS
        // dial re-enters the backoff path (grown delay) instead of being
        // swallowed as the old stream's close.
        session.streamOpening()
        syncViewStateFromPhase()
        do {
            for try await ev in client.terminalStream(taskID: taskID, since: since) {
                if _Concurrency.Task.isCancelled { return }
                apply(session.handle(ev))
            }
            // Stream finished with no `exit` event (e.g. daemon bounce).
            if let action = session.streamClosed() {
                Self.log.info("[terminal] stream drop task=\(self.taskID, privacy: .public) reconnecting from offset=\(self.session.offset)")
                apply([action])
            }
        } catch is CancellationError {
            return
        } catch {
            Self.log.error("[terminal] stream error task=\(self.taskID, privacy: .public) err=\(String(describing: error), privacy: .public)")
            if let action = session.streamClosed(error: error) {
                apply([action])
            } else {
                syncViewStateFromPhase()
            }
        }
    }

    // MARK: - Action application

    private func apply(_ actions: [TerminalStreamSession.Action]) {
        for action in actions {
            switch action {
            case .feed(let data):
                let bytes = [UInt8](data)
                terminalView.feed(byteArray: bytes[...])
            case .openStream(let since):
                openStream(since: since, after: nil)
            case .reconnect(let since, let after):
                openStream(since: since, after: after)
            case .ended:
                Self.log.info("[terminal] session ended task=\(self.taskID, privacy: .public)")
            case .clipboard(let text):
                stageClipboard(text)
            }
        }
        syncViewStateFromPhase()
    }

    /// Maps the state-machine phase onto the UI state — but never clobbers a
    /// hard `.error` (that path is owned by the initial-connect failure).
    private func syncViewStateFromPhase() {
        if case .error = viewState { return }
        switch session.phase {
        case .idle, .connecting: viewState = .connecting
        case .live: viewState = .live
        case .reconnecting: viewState = .reconnecting
        case .ended: viewState = .ended
        }
    }

    // MARK: - Input (called from the delegate)

    /// Enqueues user input. A single serial pump drains the queue so bytes reach
    /// the PTY in FIFO order (multiple concurrent POSTs could otherwise arrive
    /// out of order and corrupt input).
    func enqueueInput(_ data: Data) {
        guard !data.isEmpty else { return }
        inputContinuation.yield(data)
    }

    private func runInputPump(_ stream: AsyncStream<Data>) async {
        let maxChunk = 64 * 1024
        for await data in stream {
            var idx = 0
            while idx < data.count {
                let end = min(idx + maxChunk, data.count)
                let chunk = data.subdata(in: idx..<end)
                do {
                    try await client.sendInput(taskID: taskID, chunk)
                } catch {
                    Self.log.error("[terminal] input failed task=\(self.taskID, privacy: .public) err=\(String(describing: error), privacy: .public)")
                    break
                }
                idx = end
            }
        }
    }

    // MARK: - Resize (called from the delegate)

    /// Debounces geometry-driven size changes (~250 ms) then POSTs `/resize`.
    /// Skips a POST when the size matches the last one sent.
    func terminalSizeChanged(cols: Int, rows: Int) {
        resizeTask?.cancel()
        resizeTask = _Concurrency.Task { [weak self] in
            try? await _Concurrency.Task.sleep(for: .milliseconds(250))
            if _Concurrency.Task.isCancelled { return }
            guard let self else { return }
            if let last = self.lastSentSize, last.cols == cols, last.rows == rows { return }
            self.lastSentSize = (cols, rows)
            do {
                _ = try await self.client.resize(taskID: self.taskID,
                                                 rows: UInt16(clamping: rows),
                                                 cols: UInt16(clamping: cols))
                Self.log.info("[terminal] resize task=\(self.taskID, privacy: .public) cols=\(cols) rows=\(rows)")
            } catch {
                Self.log.error("[terminal] resize failed task=\(self.taskID, privacy: .public) err=\(String(describing: error), privacy: .public)")
            }
        }
    }

    // MARK: - Terminal-safe chord actions (add-mac-keybinding-parity Stage 5)

    /// Small per-press line scroll for Shift+Up/Down (spec.md "Scroll via
    /// Shift chords without PTY leak"). Chosen over a 1-line bump so a
    /// single keypress reads as a deliberate nudge; still small relative
    /// to ``scrollPageUp()``/``scrollPageDown()``'s full-viewport jump.
    private static let lineScrollStep = 3

    /// All five scroll actions go through SwiftTerm's `scrollUp`/
    /// `scrollDown`/`scroll(toPosition:)` — deliberately NOT its
    /// `pageUp()`/`pageDown()` convenience methods, which fall back to
    /// sending a raw escape sequence to the PTY (via the terminal
    /// delegate's `send`, i.e. `POST /input`) whenever the alt screen is
    /// active (no scrollback to move through). That fallback would leak
    /// bytes to the agent for a chord this change promises never reaches
    /// it (spec.md's non-regression scenario). `scrollUp`/`scrollDown`/
    /// `scroll(toPosition:)` have no such fallback — in alt-screen mode
    /// they are simply clamped no-ops, which is the correct behavior here.
    func scrollLineUp() {
        terminalView.scrollUp(lines: Self.lineScrollStep)
    }

    func scrollLineDown() {
        terminalView.scrollDown(lines: Self.lineScrollStep)
    }

    /// A full-viewport-height scroll for Shift+PageUp/PageDown.
    func scrollPageUp() {
        terminalView.scrollUp(lines: terminalView.getTerminal().rows)
    }

    func scrollPageDown() {
        terminalView.scrollDown(lines: terminalView.getTerminal().rows)
    }

    /// Scrolls all the way to the live/most-recent output for Shift+End.
    /// `scroll(toPosition:)` clamps `1.0` to the maximum scroll offset
    /// itself, so this needs no scrollback-length arithmetic here.
    func scrollToBottom() {
        terminalView.scroll(toPosition: 1.0)
    }

    /// Copies the terminal's currently VISIBLE viewport — not the full
    /// scrollback — to the system pasteboard (spec.md "Copy visible
    /// output via shortcut"). Reads each on-screen row directly via
    /// `Terminal.getLine(row:)` + `BufferLine.translateToString(...)`
    /// rather than SwiftTerm's selection APIs (`selectAll()` +
    /// `getSelection()`), which select the WHOLE scrollback, not just
    /// what's on screen. Despite `getLine(row:)`'s doc comment ("counted
    /// from start of scroll back, not what the terminal has visible right
    /// now" — misleading; it looks copy-pasted from the sibling
    /// `getScrollInvariantLine`), its actual implementation indexes
    /// `buffer.lines[row + buffer.yDisp]` bounded to `row < rows`: that IS
    /// the visible-row accessor, already offset by the current scroll
    /// position — so this reflects whatever's on screen right now,
    /// including after a Shift+scroll chord moved the viewport.
    func copyVisibleOutput() {
        let terminal = terminalView.getTerminal()
        var lines: [String] = []
        for row in 0..<terminal.rows {
            guard let line = terminal.getLine(row: row) else { break }
            lines.append(line.translateToString(trimRight: true))
        }
        let text = lines.joined(separator: "\n")
        guard !text.isEmpty else { return }
        writeToPasteboard(text)
        showToast("Copied visible output")
    }

    // MARK: - Clipboard

    /// Handles an agent-staged clipboard change from the SSE stream. A nil text
    /// is a clear — nothing lands on the pasteboard.
    private func stageClipboard(_ text: String?) {
        guard let text, !text.isEmpty else { return }
        writeToPasteboard(text)
        showToast("Copied to clipboard")
    }

    /// Handles a terminal-originated copy (OSC 52) from the delegate.
    func terminalRequestedCopy(_ content: Data) {
        let text = String(decoding: content, as: UTF8.self)
        guard !text.isEmpty else { return }
        writeToPasteboard(text)
        showToast("Copied to clipboard")
    }

    private func writeToPasteboard(_ text: String) {
        let pb = NSPasteboard.general
        pb.clearContents()
        pb.setString(text, forType: .string)
    }

    private func showToast(_ message: String) {
        clipboardToast = message
        toastTask?.cancel()
        toastTask = _Concurrency.Task { [weak self] in
            try? await _Concurrency.Task.sleep(for: .seconds(2))
            if _Concurrency.Task.isCancelled { return }
            self?.clipboardToast = nil
        }
    }
}

/// The SwiftTerm delegate. Kept off the main actor so it can satisfy the
/// non-isolated ``SwiftTerm/TerminalViewDelegate`` requirements; every callback
/// hops onto the main actor (SwiftTerm always invokes them on the main thread)
/// to reach the ``TerminalController``.
final class TerminalCoordinator: TerminalViewDelegate {
    weak var controller: TerminalController?

    private static let log = Logger(subsystem: "com.argus.mac", category: "terminal")

    func send(source: TerminalView, data: ArraySlice<UInt8>) {
        // Ctrl+Z (0x1A) must never reach the agent PTY. It raises SIGTSTP and
        // suspends the process, but the graver problem is Claude Code specific:
        // a literal Ctrl+Z byte trips the CLI's OWN background-session
        // supervisor, which reparents the session out of argus's process tree
        // permanently and invisibly (an orphaned session argus's stop path can
        // never signal again). Strip it here — the single outbound chokepoint
        // for keyboard input — mirroring the TUI, which likewise never forwards
        // Ctrl+Z to the PTY. See ArgusKit/TerminalInput + gotchas/macos-app.md.
        let raw = Data(data)
        let bytes = TerminalInput.sanitize(raw)
        if bytes.count != raw.count {
            Self.log.info("[terminal] dropped Ctrl+Z (0x1A) from keyboard input — would suspend/orphan the session")
        }
        guard !bytes.isEmpty else { return }
        let controller = self.controller
        MainActor.assumeIsolated { controller?.enqueueInput(bytes) }
    }

    func sizeChanged(source: TerminalView, newCols: Int, newRows: Int) {
        let controller = self.controller
        MainActor.assumeIsolated { controller?.terminalSizeChanged(cols: newCols, rows: newRows) }
    }

    func clipboardCopy(source: TerminalView, content: Data) {
        let controller = self.controller
        MainActor.assumeIsolated { controller?.terminalRequestedCopy(content) }
    }

    func requestOpenLink(source: TerminalView, link: String, params: [String: String]) {
        guard let url = URL(string: link), let scheme = url.scheme,
              scheme == "http" || scheme == "https" else { return }
        MainActor.assumeIsolated { _ = NSWorkspace.shared.open(url) }
    }

    // Unused callbacks — required by the protocol (no default macOS impls).
    func setTerminalTitle(source: TerminalView, title: String) {}
    func hostCurrentDirectoryUpdate(source: TerminalView, directory: String?) {}
    func scrolled(source: TerminalView, position: Double) {}
    func bell(source: TerminalView) {}
    func iTermContent(source: TerminalView, content: ArraySlice<UInt8>) {}
    func rangeChanged(source: TerminalView, startY: Int, endY: Int) {}
}

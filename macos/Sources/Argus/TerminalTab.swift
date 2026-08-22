import AppKit
import ArgusKit
import SwiftTerm
import SwiftUI

/// Embeds a reused SwiftTerm ``SwiftTerm/TerminalView`` (owned by a
/// ``TerminalController``) in SwiftUI. The view is pinned to the container's
/// edges so SwiftUI geometry drives its frame; SwiftTerm recomputes cols/rows
/// on every frame change and reports them through the delegate, which the
/// controller debounces into a POST `/resize`.
struct TerminalSurface: NSViewRepresentable {
    let controller: TerminalController

    func makeNSView(context: Context) -> NSView {
        let container = NSView()
        let tv = controller.terminalView
        // The controller instance is cached and reused across tab switches, so
        // the same TerminalView may still be parented to an old container.
        tv.removeFromSuperview()
        tv.translatesAutoresizingMaskIntoConstraints = false
        container.addSubview(tv)
        NSLayoutConstraint.activate([
            tv.topAnchor.constraint(equalTo: container.topAnchor),
            tv.bottomAnchor.constraint(equalTo: container.bottomAnchor),
            tv.leadingAnchor.constraint(equalTo: container.leadingAnchor),
            tv.trailingAnchor.constraint(equalTo: container.trailingAnchor),
        ])
        return container
    }

    func updateNSView(_ nsView: NSView, context: Context) {
        // Focus is handled by FocusTakingTerminalView (click + mount) — hosted
        // AppKit views get no first responder from SwiftUI on their own.
    }
}

/// SwiftTerm's `TerminalView.mouseDown` only drives selection/mouse-reporting —
/// it never takes first responder, which a plain AppKit window compensates for
/// but a SwiftUI-hosted view does not. Without this subclass, keystrokes never
/// reach `keyDown` and the terminal is read-only. `mouseDown` is `public` (not
/// `open`) in SwiftTerm so it cannot be overridden (release-mode enforces it);
/// a local left-click event monitor reclaims focus instead, and
/// `viewDidMoveToWindow` (inherited open from NSView) grabs it on mount.
final class FocusTakingTerminalView: TerminalView {
    private var clickMonitor: Any?
    private var keyMonitor: Any?

    /// Back-references wired by `AppState.terminalController(for:)` right
    /// after constructing this view's owning controller
    /// (add-mac-keybinding-parity Stage 5) — needed by the key monitor
    /// below to dispatch task-switch/tab-cycle/open-PR actions (`appState`)
    /// and scroll/copy actions (`controller`).
    weak var controller: TerminalController?
    weak var appState: AppState?

    /// Cmd+Shift+U ("Open PR") is a chrome-level `.keyboardShortcut`
    /// (`TaskActions.swift`) that ALSO needs to fire while this terminal
    /// has focus (spec.md "Open PR via shortcut from either context") —
    /// per an earlier stage's review finding, the same D2 risk that
    /// motivates ``TerminalChords`` could apply to it too (SwiftTerm may
    /// swallow the chord before a SwiftUI Command ever sees it).
    /// Deliberately a SEPARATE constant from ``TerminalChords/intercepted``
    /// — that set is exactly design.md D2's fixed 10 chords (pinned by
    /// `ChromeShortcutCollisionTests`, tasks.md 5.4) and must never absorb
    /// a chrome-level shortcut's fallback path.
    private static let openPRFallbackChord = KeyChord(.character("u"), [.command, .shift])

    override func viewDidMoveToWindow() {
        super.viewDidMoveToWindow()
        if let clickMonitor {
            NSEvent.removeMonitor(clickMonitor)
            self.clickMonitor = nil
        }
        if let keyMonitor {
            NSEvent.removeMonitor(keyMonitor)
            self.keyMonitor = nil
        }
        guard window != nil else { return }
        // Grab focus when (re)mounted so typing works without a click. Async:
        // the SwiftUI mount may not have finished wiring the window's responder
        // plumbing synchronously.
        DispatchQueue.main.async { [weak self] in
            guard let self, let window = self.window else { return }
            if window.firstResponder !== self {
                window.makeFirstResponder(self)
            }
        }
        // Reclaim focus on any click landing inside the terminal (the monitor
        // fires before normal dispatch; returning the event lets SwiftTerm's
        // own selection handling proceed untouched).
        clickMonitor = NSEvent.addLocalMonitorForEvents(matching: [.leftMouseDown]) { [weak self] event in
            guard let self, let window = self.window, event.window === window else { return event }
            let point = self.convert(event.locationInWindow, from: nil)
            if self.bounds.contains(point), window.firstResponder !== self {
                window.makeFirstResponder(self)
            }
            return event
        }
        // Terminal-safe chord interception (design.md D2): swallow the
        // fixed allowlist (`TerminalChords.intercepted`) — plus the one
        // Cmd+Shift+U defensive fallback above — before SwiftTerm's own
        // `keyDown` ever runs, so they never reach `send(source:data:)`
        // and therefore never reach `POST /input`. This is a SEPARATE
        // monitor from `clickMonitor` above (different event mask,
        // different concern) so neither's logic has to be threaded through
        // the other. Every other keystroke returns the event completely
        // unmodified — the non-regression guarantee (spec.md "Unclaimed
        // keystrokes still reach the PTY unchanged").
        keyMonitor = NSEvent.addLocalMonitorForEvents(matching: [.keyDown]) { [weak self] event in
            guard let self, let window = self.window, event.window === window else { return event }
            // A local `.keyDown` monitor sees every keystroke in the whole
            // app, regardless of which view is focused — only act while
            // THIS terminal itself is the first responder.
            guard window.firstResponder === self else { return event }
            guard let chord = KeyChord(event) else { return event }

            // These dispatch methods reach into `@MainActor` types
            // (`AppState`/`TerminalController`) from this nonisolated
            // `NSEvent` monitor closure — `MainActor.assumeIsolated` is
            // safe because AppKit always invokes local event monitors on
            // the main thread, mirroring `TerminalCoordinator.send`'s own
            // `MainActor.assumeIsolated { controller?.enqueueInput(...) }`
            // just below in `TerminalController.swift`.
            if TerminalChords.isIntercepted(chord) {
                MainActor.assumeIsolated { self.dispatchInterceptedChord(chord) }
                return nil
            }
            if chord == Self.openPRFallbackChord {
                MainActor.assumeIsolated { self.dispatchOpenPRFallback() }
                return nil
            }
            return event
        }
    }

    @MainActor
    private func dispatchInterceptedChord(_ chord: KeyChord) {
        // Each case optional-chains only the back-reference it actually
        // needs (`appState` for task-switch/tab-cycle, `controller` for
        // scroll/copy) rather than bailing the whole dispatch out on a nil
        // `controller` — the two references are set together in practice
        // (`AppState.terminalController(for:)`), but there's no reason to
        // couple an appState-only action's availability to controller's.
        switch chord {
        case KeyChord(.up, [.command]):
            appState?.selectPreviousTask()
        case KeyChord(.down, [.command]):
            appState?.selectNextTask()
        case KeyChord(.left, [.command]):
            appState?.cycleDetailTab(forward: false)
        case KeyChord(.right, [.command]):
            appState?.cycleDetailTab(forward: true)
        case KeyChord(.up, [.shift]):
            controller?.scrollLineUp()
        case KeyChord(.down, [.shift]):
            controller?.scrollLineDown()
        case KeyChord(.pageUp, [.shift]):
            controller?.scrollPageUp()
        case KeyChord(.pageDown, [.shift]):
            controller?.scrollPageDown()
        case KeyChord(.end, [.shift]):
            controller?.scrollToBottom()
        case KeyChord(.character("c"), [.command, .shift]):
            controller?.copyVisibleOutput()
        default:
            break // unreachable: only called once `isIntercepted` matched.
        }
    }

    @MainActor
    private func dispatchOpenPRFallback() {
        guard let controller, let appState, let task = appState.task(withID: controller.taskID) else { return }
        _Concurrency.Task { await appState.openPR(for: task) }
    }

    // No deinit cleanup needed (and Swift 6 forbids touching the non-Sendable
    // monitor from a nonisolated deinit): leaving a window always fires
    // viewDidMoveToWindow with window == nil, which removes the monitors above.
}

// MARK: - NSEvent → KeyChord conversion

/// The only piece of the Terminal tab's chord-interception mechanism
/// (design.md D2) that has to touch `NSEvent`, so it lives here in the App
/// target rather than in ArgusKit (which can't import AppKit). The harder,
/// more error-prone half — the virtual-keycode table — is pure Foundation
/// and lives in ArgusKit's `KeyChordDecoding`, where `KeyChordDecodingTests`
/// pins it; this initializer only layers on the AppKit-specific
/// modifier-flag mapping. Untestable glue (no App-target test harness
/// exists in this repo), but small and easy to eyeball-verify.
private extension KeyChord {
    /// `nil` for a non-keyDown event, or one whose key resolves to nothing
    /// (e.g. a bare modifier-flags-changed event, or a dead key with no
    /// committed character).
    init?(_ event: NSEvent) {
        guard event.type == .keyDown else { return nil }
        guard let key = KeyChordDecoding.key(forKeyCode: event.keyCode,
                                              charactersIgnoringModifiers: event.charactersIgnoringModifiers) else {
            return nil
        }
        let flags = event.modifierFlags
        var modifiers: Set<Modifier> = []
        if flags.contains(.command) { modifiers.insert(.command) }
        if flags.contains(.shift) { modifiers.insert(.shift) }
        if flags.contains(.option) { modifiers.insert(.option) }
        if flags.contains(.control) { modifiers.insert(.control) }
        self.init(key, modifiers)
    }
}

/// The Terminal tab: a live streaming terminal, or the connecting / ended /
/// error states around it, with a transient clipboard toast overlay.
struct TerminalTab: View {
    let task: ArgusTask
    @Environment(AppState.self) private var app

    var body: some View {
        Group {
            if let controller = app.terminalController(for: task.id) {
                TerminalContent(controller: controller)
                    .id(task.id)
            } else {
                ContentUnavailableView {
                    Label("Not connected", systemImage: "bolt.horizontal.circle")
                } description: {
                    Text("Connect to the argus daemon to view this task's terminal.")
                }
            }
        }
    }
}

/// The controller-bound content. Split out so `controller` is a concrete value
/// SwiftUI can observe (`@Observable`), driving state re-renders.
private struct TerminalContent: View {
    let controller: TerminalController

    var body: some View {
        ZStack {
            // The terminal itself is always mounted (so scrollback persists);
            // overlays cover it for non-live states.
            TerminalSurface(controller: controller)

            switch controller.viewState {
            case .connecting:
                overlay {
                    VStack(spacing: 10) {
                        ProgressView()
                        Text("Connecting\u{2026}").foregroundStyle(.secondary)
                    }
                }
            case .reconnecting:
                // Non-blocking banner; the terminal stays visible underneath.
                VStack {
                    HStack(spacing: 8) {
                        ProgressView().controlSize(.small)
                        Text("Reconnecting\u{2026}").font(.callout)
                        Button("Reconnect now") { controller.reconnectNow() }
                            .buttonStyle(.link)
                    }
                    .padding(.horizontal, 12).padding(.vertical, 6)
                    .background(.thinMaterial, in: Capsule())
                    .padding(.top, 8)
                    Spacer()
                }
            case .ended:
                overlay {
                    ContentUnavailableView {
                        Label("Session ended", systemImage: "stop.circle")
                    } description: {
                        Text("The agent session has exited.")
                    } actions: {
                        Button("Restart") { controller.restart() }
                            .buttonStyle(.borderedProminent)
                    }
                }
            case .error(let message):
                overlay {
                    ContentUnavailableView {
                        Label("Terminal unavailable", systemImage: "exclamationmark.triangle")
                    } description: {
                        Text(message)
                    } actions: {
                        Button("Retry") { controller.retry() }
                            .buttonStyle(.borderedProminent)
                    }
                }
            case .live:
                EmptyView()
            }

            if let toast = controller.clipboardToast {
                VStack {
                    Spacer()
                    Label(toast, systemImage: "doc.on.clipboard")
                        .font(.callout)
                        .padding(.horizontal, 14).padding(.vertical, 8)
                        .background(.thinMaterial, in: Capsule())
                        .padding(.bottom, 16)
                }
                .transition(.opacity)
            }
        }
        .animation(.easeInOut(duration: 0.2), value: controller.clipboardToast)
        .onAppear { controller.attach() }
    }

    /// A dimmed full-cover overlay for blocking states (connecting / ended /
    /// error) so the raw terminal doesn't distract behind them.
    private func overlay<Content: View>(@ViewBuilder _ content: () -> Content) -> some View {
        ZStack {
            Rectangle().fill(.background.opacity(0.85))
            content()
        }
    }
}

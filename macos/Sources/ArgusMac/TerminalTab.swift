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
        // Clicking the terminal focuses it (SwiftTerm becomes first responder
        // on mouseDown); make it the first responder on mount too so keystrokes
        // flow immediately without a click.
        let tv = controller.terminalView
        if let window = tv.window, window.firstResponder !== tv {
            DispatchQueue.main.async { window.makeFirstResponder(tv) }
        }
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

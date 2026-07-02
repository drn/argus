import AppKit
import SwiftUI

/// ArgusMac — a Conductor-style native GUI for the argus daemon.
///
/// This is the app shell only: it renders the task list and a task-detail tab
/// view whose Terminal / Diff / Files tabs are placeholders that later phases
/// fill in (Terminal will embed SwiftTerm). The Info tab and the whole
/// connection / polling / settings surface are real.
@main
struct ArgusMacApp: App {
    // The delegate exists purely to make a window appear and grab focus when
    // the binary is launched OUTSIDE an .app bundle (i.e. via `swift run`),
    // where the default activation policy is `.prohibited`/`.accessory`.
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    @State private var appState = AppState()

    var body: some Scene {
        WindowGroup {
            ContentView()
                .environment(appState)
                .frame(minWidth: 820, minHeight: 520)
                .task {
                    // Kick off the first connect + polling loop once the window
                    // is on screen. Idempotent: re-entrancy cancels the prior
                    // loop before starting a new one.
                    appState.connect()
                }
        }
        .windowToolbarStyle(.unified)

        // Provides the standard Cmd+, menu item automatically.
        Settings {
            SettingsView()
                .environment(appState)
        }
    }
}

/// Makes the window appear and activate when run as a bare executable.
///
/// `swift run ArgusMac` produces a plain Mach-O binary, not a bundled `.app`,
/// so AppKit starts it as a background (`.accessory`) process with no menu bar
/// or focused window. Forcing `.regular` + `activate` is the standard fix.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApplication.shared.setActivationPolicy(.regular)
        NSApplication.shared.activate(ignoringOtherApps: true)
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }
}

import SwiftUI

/// The root window: a sidebar of tasks + a detail pane, with a connection
/// indicator in the toolbar and non-blocking error banners overlaid on top.
/// Also owns the New Task / Rename sheets and the single app-wide
/// confirmation dialog for Stop/Delete — see ``TaskActions.swift``.
struct ContentView: View {
    @Environment(AppState.self) private var app
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        @Bindable var app = app
        NavigationSplitView {
            Sidebar()
                .navigationSplitViewColumnWidth(min: 240, ideal: 300, max: 420)
        } detail: {
            if app.showingHera {
                HeraTab()
            } else {
                DetailView()
            }
        }
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    app.isPresentingNewTask = true
                } label: {
                    Label("New Task", systemImage: "plus")
                }
                .keyboardShortcut("n", modifiers: .command)
                .help("New Task (\u{2318}N)")
            }
            ToolbarItem(placement: .primaryAction) {
                Button {
                    app.showingHera.toggle()
                } label: {
                    Label("Hera", systemImage: "point.3.filled.connected.trianglepath.dotted")
                }
                .help("Toggle the Hera roster")
            }
            ToolbarItem(placement: .primaryAction) {
                Button {
                    openWindow(id: "schedules")
                } label: {
                    Label("Schedules", systemImage: "clock")
                }
                .keyboardShortcut("s", modifiers: [.command, .shift])
                .help("Schedules (\u{21E7}\u{2318}S)")
            }
            ToolbarItem(placement: .status) {
                ConnectionIndicator()
            }
        }
        .overlay(alignment: .top) {
            VStack(spacing: 8) {
                ConnectionBanner()
                ActionErrorBanner()
            }
            .padding(.horizontal)
            .padding(.top, 8)
        }
        .sheet(isPresented: $app.isPresentingNewTask) {
            NewTaskSheet()
        }
        .sheet(item: $app.renamingTask) { task in
            RenameSheet(task: task)
        }
        .taskConfirmationDialog(app: app)
    }
}

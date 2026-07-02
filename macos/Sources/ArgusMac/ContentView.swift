import SwiftUI

/// The root window: a sidebar of tasks + a detail pane, with a connection
/// indicator in the toolbar and non-blocking error banners overlaid on top.
/// Also owns the New Task / Rename sheets and the single app-wide
/// confirmation dialog for Stop/Delete — see ``TaskActions.swift``.
struct ContentView: View {
    @Environment(AppState.self) private var app
    @State private var showingNewTaskSheet = false

    var body: some View {
        @Bindable var app = app
        NavigationSplitView {
            Sidebar()
                .navigationSplitViewColumnWidth(min: 240, ideal: 300, max: 420)
        } detail: {
            DetailView()
        }
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    showingNewTaskSheet = true
                } label: {
                    Label("New Task", systemImage: "plus")
                }
                .keyboardShortcut("n", modifiers: .command)
                .help("New Task (\u{2318}N)")
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
        .sheet(isPresented: $showingNewTaskSheet) {
            NewTaskSheet()
        }
        .sheet(item: $app.renamingTask) { task in
            RenameSheet(task: task)
        }
        .taskConfirmationDialog(app: app)
    }
}

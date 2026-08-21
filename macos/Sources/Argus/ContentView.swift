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
                    Label("Projects", systemImage: "point.3.filled.connected.trianglepath.dotted")
                }
                .help("Toggle the Projects roster")
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
            ToolbarItem(placement: .primaryAction) {
                overflowMenu
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
        .background(globalShortcuts)
        .sheet(isPresented: $app.isPresentingNewTask) {
            NewTaskSheet()
        }
        .sheet(item: $app.renamingTask) { task in
            RenameSheet(task: task)
        }
        .sheet(isPresented: $app.isPresentingShortcutsHelp) {
            ShortcutsHelpSheet()
        }
        .taskConfirmationDialog(app: app)
    }

    /// The toolbar's "…" overflow menu — a genuinely new, app-chrome-level
    /// menu (distinct from the per-task "Actions" menu in
    /// ``TaskDetailTabs``) for global/daemon-wide maintenance actions that
    /// aren't scoped to any one task. Currently holds only "Prune Stale
    /// Worktrees" (design.md D6: low-frequency maintenance action, no
    /// shortcut needed — reachability, not speed).
    private var overflowMenu: some View {
        Menu {
            Button("Prune Stale Worktrees") {
                app.pendingConfirmation = .pruneCompleted
            }
        } label: {
            Label("More", systemImage: "ellipsis.circle")
        }
        .help("More actions")
    }

    /// Chrome-level shortcuts that don't require a task to be selected, so
    /// they're attached here (always mounted for the whole window) rather
    /// than inside the detail pane. Hidden, zero-size buttons rather than a
    /// centralized dispatch table (design.md D1).
    private var globalShortcuts: some View {
        Group {
            Button("") { app.isPresentingShortcutsHelp = true }
                .keyboardShortcut("/", modifiers: [.command, .shift])
            Button("") { app.jumpToNextNeedsInput() }
                .keyboardShortcut("j", modifiers: [.command, .shift])
        }
        .opacity(0)
        .frame(width: 0, height: 0)
    }
}

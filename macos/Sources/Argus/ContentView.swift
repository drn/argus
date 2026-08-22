import ArgusKit
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
            } else if app.sidebarMode == .hera {
                // TEMPORARY: Stage 5 (add-mac-hera-rail-toggle) replaces this
                // with the real dual-pane HeraDetailView.
                HeraTreeDetailPlaceholder()
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

/// TEMPORARY: Stage 5 (`add-mac-hera-rail-toggle`) replaces this with the
/// real dual-pane `HeraDetailView` (coordinator pane + selected-role pane, or
/// a roster region when the selection is itself a coordinator). Shown only
/// while the sidebar is in Hera-tree mode (``AppState/sidebarMode``) and the
/// old toolbar roster (``AppState/showingHera``) isn't also active.
private struct HeraTreeDetailPlaceholder: View {
    @Environment(AppState.self) private var app

    private var selectedRole: HeraRole? {
        guard let roleID = app.selectedHeraRoleID, let roster = app.currentHeraRoster else { return nil }
        return (roster.orchestrators.flatMap(\.roles) + roster.freelance)
            .first { $0.roleID == roleID }
    }

    var body: some View {
        if let role = selectedRole {
            VStack(alignment: .leading, spacing: 8) {
                Text(role.name)
                    .font(.title2.bold())
                Text("\(role.kind) · \(role.status.isEmpty ? "no status yet" : role.status)")
                    .foregroundStyle(.secondary)
                if role.live, !role.taskID.isEmpty {
                    Text("Bound to \(role.taskName.isEmpty ? role.taskID : role.taskName)")
                        .foregroundStyle(.secondary)
                } else {
                    Text("Unbound")
                        .foregroundStyle(.tertiary)
                }
            }
            .padding()
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        } else {
            ContentUnavailableView {
                Label("No role selected", systemImage: "point.3.filled.connected.trianglepath.dotted")
            } description: {
                Text("Select a role from the Projects tree to see its details.")
            }
        }
    }
}

import SwiftUI

/// The detail pane: an empty state when nothing is selected, otherwise a
/// tabbed view of the selected task.
struct DetailView: View {
    @Environment(AppState.self) private var app

    var body: some View {
        if let task = app.selectedTask {
            TaskDetailTabs(task: task)
        } else {
            EmptyStateView()
        }
    }
}

/// The per-task tab view. Terminal is a live SwiftTerm stream; Diff / Files are
/// placeholders for later phases; Info is real.
struct TaskDetailTabs: View {
    let task: ArgusTask
    @Environment(AppState.self) private var app

    var body: some View {
        @Bindable var app = app
        VStack(spacing: 0) {
            DetailHeaderChips(task: task)
            TabView(selection: $app.activeDetailTab) {
                TerminalTab(task: task)
                    .tabItem { Label("Terminal", systemImage: "terminal") }
                    .tag(AppState.DetailTab.terminal)

                DiffTab(task: task)
                    .tabItem { Label("Diff", systemImage: "plusminus") }
                    .tag(AppState.DetailTab.diff)

                FilesTab(task: task)
                    .tabItem { Label("Files", systemImage: "folder") }
                    .tag(AppState.DetailTab.files)

                InfoTab(task: task)
                    .tabItem { Label("Info", systemImage: "info.circle") }
                    .tag(AppState.DetailTab.info)
            }
            .padding()
        }
        .navigationTitle(task.name)
        .navigationSubtitle(task.project)
        // Belt-and-braces alongside TaskRow's own `.task(id:)`: the overflow
        // menu below reads `AppState.isPinned(_:)` (a client-side-only cache)
        // when built, so make sure it's fresh for the selected task even if
        // its sidebar row hasn't rendered yet (e.g. a filtered/collapsed
        // section).
        .task(id: task.id) {
            await app.refreshPinnedState(taskID: task.id)
        }
        .toolbar {
            // Only meaningful on the Terminal tab, and only for a
            // Claude-backed task (client-side pre-filter — see
            // AppState.isLikelyClaudeBacked; the daemon's 400 is the
            // authoritative check either way, surfaced via ActionErrorBanner
            // by openClaudeSessionPicker(for:) for anything this misses).
            if app.activeDetailTab == .terminal && AppState.isLikelyClaudeBacked(task) {
                ToolbarItem(placement: .primaryAction) {
                    Button {
                        _Concurrency.Task { await app.openClaudeSessionPicker(for: task) }
                    } label: {
                        Label("Switch Claude Session", systemImage: "clock.arrow.circlepath")
                    }
                    .help("Switch Claude Session")
                }
            }
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    TaskActionMenuItems(task: task, showShortcuts: true)
                } label: {
                    Label("Actions", systemImage: "ellipsis.circle")
                }
            }
        }
        .background(tabSwitchShortcuts)
    }

    /// Cmd+1/2/3/4 tab-switch shortcuts (design.md's Cmd+digit framing).
    /// Hidden, zero-size buttons rather than a centralized dispatch table
    /// (D1) — attached to this persistently-mounted view (rather than, say,
    /// the sidebar) so they're only live while a task's detail pane is
    /// actually showing tabs.
    private var tabSwitchShortcuts: some View {
        Group {
            Button("") { app.activeDetailTab = .terminal }
                .keyboardShortcut("1", modifiers: .command)
            Button("") { app.activeDetailTab = .diff }
                .keyboardShortcut("2", modifiers: .command)
            Button("") { app.activeDetailTab = .files }
                .keyboardShortcut("3", modifiers: .command)
            Button("") { app.activeDetailTab = .info }
                .keyboardShortcut("4", modifiers: .command)
        }
        .opacity(0)
        .frame(width: 0, height: 0)
    }
}

/// Shown when no task is selected.
struct EmptyStateView: View {
    var body: some View {
        ContentUnavailableView {
            Label("No task selected", systemImage: "sidebar.left")
        } description: {
            Text("Select a task from the sidebar to see its details.")
        }
    }
}

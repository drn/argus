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
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    TaskActionMenuItems(task: task, showShortcuts: true)
                } label: {
                    Label("Actions", systemImage: "ellipsis.circle")
                }
            }
        }
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

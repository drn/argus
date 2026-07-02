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

/// The per-task tab view. Terminal / Diff / Files are placeholders for later
/// phases; Info is real.
struct TaskDetailTabs: View {
    let task: ArgusTask

    var body: some View {
        TabView {
            PlaceholderTab(
                title: "Terminal",
                systemImage: "terminal",
                message: "The live agent terminal will render here in a later phase (SwiftTerm)."
            )
            .tabItem { Label("Terminal", systemImage: "terminal") }

            PlaceholderTab(
                title: "Diff",
                systemImage: "plusminus",
                message: "The task's git diff will render here in a later phase."
            )
            .tabItem { Label("Diff", systemImage: "plusminus") }

            PlaceholderTab(
                title: "Files",
                systemImage: "folder",
                message: "The task's changed files will render here in a later phase."
            )
            .tabItem { Label("Files", systemImage: "folder") }

            InfoTab(task: task)
                .tabItem { Label("Info", systemImage: "info.circle") }
        }
        .padding()
        .navigationTitle(task.name)
        .navigationSubtitle(task.project)
    }
}

/// A labelled placeholder for a not-yet-built tab.
struct PlaceholderTab: View {
    let title: String
    let systemImage: String
    let message: String

    var body: some View {
        ContentUnavailableView {
            Label(title, systemImage: systemImage)
        } description: {
            Text(message)
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

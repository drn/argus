import SwiftUI

/// The task list, grouped into Active / In Review / Complete / Archived.
/// Archived is collapsed by default.
struct Sidebar: View {
    @Environment(AppState.self) private var app
    @State private var archivedExpanded = false

    var body: some View {
        @Bindable var app = app
        List(selection: $app.selectedTaskID) {
            taskSection("Active", app.activeTasks)
            taskSection("In Review", app.inReviewTasks)
            taskSection("Complete", app.completeTasks)

            if !app.archivedTasks.isEmpty {
                Section(isExpanded: $archivedExpanded) {
                    ForEach(app.archivedTasks) { task in
                        TaskRow(task: task).tag(task.id)
                    }
                } header: {
                    Text("Archived (\(app.archivedTasks.count))")
                }
            }
        }
        .listStyle(.sidebar)
        .navigationTitle("Argus")
        // Focus-scoped: only fires while the list itself has keyboard focus,
        // so a plain Delete/Backspace typed into the terminal pane (which is
        // most of what Delete is for, in a shell) never reaches this.
        .onDeleteCommand {
            guard let task = app.selectedTask else { return }
            app.pendingConfirmation = .delete(task)
        }
    }

    @ViewBuilder
    private func taskSection(_ title: String, _ items: [ArgusTask]) -> some View {
        if !items.isEmpty {
            Section(title) {
                ForEach(items) { task in
                    TaskRow(task: task).tag(task.id)
                }
            }
        }
    }
}

import SwiftUI
import ArgusKit

/// The task list, grouped into project folders (mirroring the TUI's task
/// list — see `internal/tui/taskview/tasklist.go`'s `groupByProject`/`buildRows`):
/// one `Section` per project, holding tasks of every non-archived status.
/// Archived tasks live in their own collapsed section at the bottom, outside
/// the folders, matching the TUI's separate Archive section.
///
/// Parity gap: the TUI additionally floats `Pinned` tasks (across all
/// projects) above everything else. `pinned` isn't part of the REST
/// `GET /api/tasks` wire shape (see `ArgusKit.Task`), so there is no signal
/// to reproduce that section here — see `context/knowledge/gotchas/macos-app.md`.
struct Sidebar: View {
    @Environment(AppState.self) private var app
    @State private var archivedExpanded = false

    var body: some View {
        @Bindable var app = app
        List(selection: $app.selectedTaskID) {
            ForEach(app.tasksByFolder) { folder in
                Section(folder.project) {
                    ForEach(folder.tasks) { task in
                        TaskRow(task: task).tag(task.id)
                    }
                }
            }

            if !app.archivedTasks.isEmpty {
                Section(isExpanded: $archivedExpanded) {
                    ForEach(app.archivedTasksByFolder) { folder in
                        Section(folder.project) {
                            ForEach(folder.tasks) { task in
                                TaskRow(task: task).tag(task.id)
                            }
                        }
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
}

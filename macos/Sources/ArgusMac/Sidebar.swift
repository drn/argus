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

import SwiftUI

/// A single task row: status icon + name, with a needs-input marker. No
/// project badge — the sidebar now groups tasks into per-project folder
/// Sections (see ``Sidebar``), so the folder header carries the project
/// name and the icon alone carries the status signal the old status-based
/// sections used to.
struct TaskRow: View {
    @Environment(AppState.self) private var app
    let task: ArgusTask

    /// Whether this task is in the app-wide needs-input set (the same source the
    /// dock badge + menu-bar count read), so the sidebar marker can never drift
    /// from them.
    private var needsInput: Bool { app.needsInputTaskIDs.contains(task.id) }

    var body: some View {
        HStack(spacing: 8) {
            TaskStatusIcon(task: task)
                .frame(width: 18)
            Text(task.name)
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer(minLength: 6)
            if needsInput {
                Image(systemName: "exclamationmark.circle.fill")
                    .foregroundStyle(.orange)
                    .symbolRenderingMode(.hierarchical)
                    .help("Needs input")
                    .accessibilityLabel("Needs input")
            }
        }
        .padding(.vertical, 2)
        .contextMenu {
            TaskActionMenuItems(task: task)
        }
    }
}

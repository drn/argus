import SwiftUI

/// A single task row: status icon + name, with a needs-input marker and a
/// project badge.
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
            if !task.project.isEmpty {
                ProjectBadge(project: task.project)
            }
        }
        .padding(.vertical, 2)
        .contextMenu {
            TaskActionMenuItems(task: task)
        }
    }
}

/// A small pill showing the task's project.
struct ProjectBadge: View {
    let project: String

    var body: some View {
        Text(project)
            .font(.caption2)
            .lineLimit(1)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(.quaternary, in: Capsule())
            .foregroundStyle(.secondary)
    }
}

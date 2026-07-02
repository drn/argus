import SwiftUI

/// A single task row: status icon + name, with a project badge.
struct TaskRow: View {
    let task: ArgusTask

    var body: some View {
        HStack(spacing: 8) {
            TaskStatusIcon(task: task)
                .frame(width: 18)
            Text(task.name)
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer(minLength: 6)
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

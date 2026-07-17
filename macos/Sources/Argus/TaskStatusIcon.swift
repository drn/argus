import SwiftUI

/// Maps a task's status (plus its runtime idle / needs-input / archived flags)
/// to an SF Symbol + colour. Mirrors the TUI's status classifier semantics:
/// a task waiting on the user outranks its workflow status.
struct TaskStatusIcon: View {
    let task: ArgusTask

    var body: some View {
        Image(systemName: descriptor.symbol)
            .foregroundStyle(descriptor.color)
            .symbolRenderingMode(.hierarchical)
            .help(descriptor.label)
    }

    struct Descriptor {
        let symbol: String
        let color: Color
        let label: String
    }

    var descriptor: Descriptor {
        // Needs-input is the loudest signal — an agent blocked on the user.
        if task.needsInput {
            return .init(symbol: "questionmark.circle.fill", color: .orange, label: "Needs input")
        }
        if task.archived {
            return .init(symbol: "archivebox", color: .secondary, label: "Archived")
        }
        switch task.taskStatus {
        case .pending:
            return .init(symbol: "circle.dashed", color: .secondary, label: "Pending")
        case .inProgress:
            if task.idle {
                return .init(symbol: "pause.circle.fill", color: .yellow, label: "Idle")
            }
            return .init(symbol: "play.circle.fill", color: .green, label: "Running")
        case .inReview:
            return .init(symbol: "eye.circle.fill", color: .blue, label: "In review")
        case .complete:
            return .init(symbol: "checkmark.circle.fill", color: .green, label: "Complete")
        case .other(let raw):
            return .init(symbol: "circle", color: .secondary, label: raw)
        }
    }
}

import SwiftUI

/// The menu-bar extra's dropdown: the top ~10 active tasks (with a status icon
/// and a needs-input marker), a New Task item, and Quit. Clicking a task brings
/// the app forward and selects it.
struct MenuBarContent: View {
    @Environment(AppState.self) private var app

    /// The daemon caps how many rows are useful in a menu; show the most
    /// relevant active tasks (needs-input first).
    private static let maxRows = 10

    private var rows: [ArgusTask] {
        let active = app.activeTasks
        let sorted = active.sorted { lhs, rhs in
            let l = app.needsInputTaskIDs.contains(lhs.id)
            let r = app.needsInputTaskIDs.contains(rhs.id)
            if l != r { return l } // needs-input tasks bubble up
            return false
        }
        return Array(sorted.prefix(Self.maxRows))
    }

    var body: some View {
        if rows.isEmpty {
            Text("No active tasks")
        } else {
            ForEach(rows) { task in
                Button {
                    app.selectTask(task.id)
                } label: {
                    let needsInput = app.needsInputTaskIDs.contains(task.id)
                    Label(task.name,
                          systemImage: needsInput ? "exclamationmark.circle.fill"
                                                   : Self.symbol(for: task))
                }
            }
        }

        Divider()

        Button("New Task…") { app.requestNewTask() }
            .keyboardShortcut("n", modifiers: .command)

        Divider()

        Button("Quit Argus") { NSApplication.shared.terminate(nil) }
            .keyboardShortcut("q", modifiers: .command)
    }

    /// A menu-friendly status glyph (independent of the needs-input override).
    private static func symbol(for task: ArgusTask) -> String {
        switch task.taskStatus {
        case .pending: return "circle.dashed"
        case .inProgress: return task.idle ? "pause.circle.fill" : "play.circle.fill"
        case .inReview: return "eye.circle.fill"
        case .complete: return "checkmark.circle.fill"
        case .other: return "circle"
        }
    }
}

/// The menu-bar label: an eye glyph (Argus, the many-eyed watcher) plus the
/// needs-input count when any task is waiting.
struct MenuBarLabel: View {
    let app: AppState

    var body: some View {
        let count = app.needsInputTaskIDs.count
        if count > 0 {
            Label("\(count)", systemImage: "eye.fill")
        } else {
            Image(systemName: "eye")
        }
    }
}

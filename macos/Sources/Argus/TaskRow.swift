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
            statusMenuItems
            Divider()
            TaskActionMenuItems(task: task)
        }
        // Lazily backfills the client-side pinned cache as this row comes on
        // screen, so TaskActionMenuItems' Pin/Unpin label (and its shortcut's
        // toggle target) reflect the task's real state without an eager bulk
        // fetch of every row's raw representation. See
        // `AppState.pinnedTaskIDs`'s doc comment for why this can't come from
        // the lossy `/api/tasks` snapshot already driving `task`.
        .task(id: task.id) {
            await app.refreshPinnedState(taskID: task.id)
        }
    }

    /// Status-advance/status-revert context-menu items — the mac equivalent
    /// of the TUI's `s`/`S` keys (design.md D4). Disabled rather than left as
    /// a silent no-op at the ladder's ends (`.complete` for advance,
    /// `.pending` for revert) or for an unrecognized `.other` status, which
    /// clamps in both directions — the more polished choice, and it mirrors
    /// what a TUI status-bar hint would show.
    @ViewBuilder
    private var statusMenuItems: some View {
        Button("Advance Status") {
            _Concurrency.Task { await app.setStatus(task, to: task.taskStatus.advanced()) }
        }
        .disabled(task.taskStatus.advanced() == task.taskStatus)

        Button("Revert Status") {
            _Concurrency.Task { await app.setStatus(task, to: task.taskStatus.reverted()) }
        }
        .disabled(task.taskStatus.reverted() == task.taskStatus)
    }
}

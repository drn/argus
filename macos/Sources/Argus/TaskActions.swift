import ArgusKit
import SwiftUI

/// The shared set of per-task lifecycle actions (Stop, Restart, Resume,
/// Archive/Unarchive, Rename, Fork, Delete), enabled per the task's current
/// status. Used both as a sidebar row's `.contextMenu` content
/// (``TaskRow``) and the detail pane's toolbar overflow menu
/// (``TaskDetailTabs``).
///
/// Stop and Delete only *request* confirmation (via
/// ``AppState/pendingConfirmation``) — the actual confirmation dialog is
/// mounted once, app-wide, by ``taskConfirmationDialog(app:)``. Every other
/// action fires immediately; failures surface through ``ActionErrorBanner``.
struct TaskActionMenuItems: View {
    let task: ArgusTask
    /// Only the persistently-mounted detail-pane instance should register
    /// keyboard shortcuts. A `.contextMenu`'s content is only built while the
    /// menu is actually open, so a shortcut there wouldn't fire reliably —
    /// worse, one instance per visible sidebar row would fight over the same
    /// key equivalent.
    var showShortcuts: Bool = false

    @Environment(AppState.self) private var app

    /// Mirrors the daemon's own 409 guard on `/restart` and `/resume`: both
    /// only refuse while the agent is actively (non-idle) in progress.
    private var canRunAgain: Bool {
        !(task.taskStatus == .inProgress && !task.idle)
    }

    var body: some View {
        Group {
            Button("Stop") {
                app.pendingConfirmation = .stop(task)
            }
            .disabled(task.taskStatus != .inProgress)

            Button("Restart") {
                _Concurrency.Task { await app.restart(task) }
            }
            .disabled(!canRunAgain)

            Button("Resume") {
                _Concurrency.Task { await app.resume(task) }
            }
            .disabled(!canRunAgain)

            Divider()

            renameButton

            Button("Fork") {
                _Concurrency.Task { await app.fork(task) }
            }

            if task.archived {
                Button("Unarchive") {
                    _Concurrency.Task { await app.unarchive(task) }
                }
            } else {
                Button("Archive") {
                    _Concurrency.Task { await app.archive(task) }
                }
            }

            Divider()

            Button("Delete", role: .destructive) {
                app.pendingConfirmation = .delete(task)
            }
        }
    }

    @ViewBuilder
    private var renameButton: some View {
        let button = Button("Rename") { app.renamingTask = task }
        if showShortcuts {
            button.keyboardShortcut("r", modifiers: .command)
        } else {
            button
        }
    }
}

// MARK: - Confirmation dialog

extension View {
    /// Attaches the single, app-wide confirmation dialog for pending
    /// destructive/interrupting task actions (Stop, Delete).
    /// ``AppState/pendingConfirmation`` is the single source of truth for
    /// what's showing — mount this once, high in the hierarchy
    /// (``ContentView``), rather than once per menu.
    func taskConfirmationDialog(app: AppState) -> some View {
        modifier(TaskConfirmationDialogModifier(app: app))
    }
}

private struct TaskConfirmationDialogModifier: ViewModifier {
    let app: AppState

    func body(content: Content) -> some View {
        content.confirmationDialog(
            title,
            isPresented: isPresented,
            titleVisibility: .visible
        ) {
            buttons
        } message: {
            Text(message)
        }
    }

    private var isPresented: Binding<Bool> {
        Binding(
            get: { app.pendingConfirmation != nil },
            set: { if !$0 { app.pendingConfirmation = nil } }
        )
    }

    private var title: String {
        switch app.pendingConfirmation {
        case .stop(let task): return "Stop \u{201C}\(task.name)\u{201D}?"
        case .delete(let task): return "Delete \u{201C}\(task.name)\u{201D}?"
        case nil: return ""
        }
    }

    private var message: String {
        switch app.pendingConfirmation {
        case .stop:
            return "The agent session will be stopped and the task moved to In Review."
        case .delete:
            return "This permanently removes the task, its worktree, and its git branch " +
                "(including the remote branch, if any). This cannot be undone."
        case nil:
            return ""
        }
    }

    @ViewBuilder
    private var buttons: some View {
        switch app.pendingConfirmation {
        case .stop(let task):
            Button("Stop", role: .destructive) {
                _Concurrency.Task { await app.stop(task) }
            }
            Button("Cancel", role: .cancel) {}
        case .delete(let task):
            Button("Delete", role: .destructive) {
                _Concurrency.Task { await app.delete(task) }
            }
            Button("Cancel", role: .cancel) {}
        case nil:
            EmptyView()
        }
    }
}

// MARK: - Action error toast

/// A transient, dismissible error toast for a failed task action (stop,
/// restart, resume, archive, rename, fork, delete). Visually mirrors
/// ``ConnectionBanner`` but is action-scoped and self-dismissing rather than
/// tied to the daemon connection state.
struct ActionErrorBanner: View {
    @Environment(AppState.self) private var app

    var body: some View {
        if let message = app.actionError {
            HStack(spacing: 10) {
                Image(systemName: "exclamationmark.triangle.fill")
                    .foregroundStyle(.red)
                Text(message)
                    .lineLimit(2)
                    .font(.callout)
                Spacer(minLength: 8)
                Button {
                    app.dismissActionError()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 8))
            .overlay(
                RoundedRectangle(cornerRadius: 8)
                    .strokeBorder(.red.opacity(0.4))
            )
            .shadow(radius: 2, y: 1)
            .transition(.move(edge: .top).combined(with: .opacity))
        }
    }
}

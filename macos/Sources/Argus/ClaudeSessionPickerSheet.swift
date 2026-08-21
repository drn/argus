import ArgusKit
import SwiftUI

/// The "Switch Claude Session" picker sheet: lists a task's available Claude
/// Code conversation sessions (newest first, per the daemon's contract) with
/// the currently-active one checkmarked, mirroring the TUI's `ctrl+r` session
/// switcher (`internal/tui/sessionpicker.go`) in intent — same ordering, same
/// current-session marker — with no TUI code shared (there is none to
/// share). Presented from ``TaskDetailTabs``'s toolbar via
/// ``AppState/openClaudeSessionPicker(for:)``, which fetches up front so this
/// view never has to render a loading/empty-vs-broken state itself.
struct ClaudeSessionPickerSheet: View {
    let state: AppState.ClaudeSessionPickerState

    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss

    /// Set while a tapped row's switch request is in flight, so a second tap
    /// (or the Cancel button) can't race it.
    @State private var switchingSessionID: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Switch Claude Session")
                .font(.title3.bold())
                .padding([.top, .horizontal])
                .padding(.bottom, 4)
            Text(state.task.name)
                .font(.callout)
                .foregroundStyle(.secondary)
                .padding(.horizontal)
                .padding(.bottom, 8)

            Divider()

            if state.sessions.isEmpty {
                ContentUnavailableView {
                    Label("No sessions found", systemImage: "clock.arrow.circlepath")
                } description: {
                    Text("This task has no other Claude sessions to switch to.")
                }
                .frame(maxHeight: .infinity)
            } else {
                List(state.sessions) { session in
                    ClaudeSessionRow(session: session,
                                     isCurrent: session.id == state.currentSessionID,
                                     isSwitching: switchingSessionID == session.id)
                        .contentShape(Rectangle())
                        .onTapGesture { select(session) }
                }
                .listStyle(.inset)
            }

            Divider()

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                    .disabled(switchingSessionID != nil)
            }
            .padding()
        }
        .frame(width: 460, height: 420)
    }

    private func select(_ session: ClaudeSession) {
        guard switchingSessionID == nil else { return }
        switchingSessionID = session.id
        _Concurrency.Task {
            await app.selectClaudeSession(session, for: state.task)
            switchingSessionID = nil
        }
    }
}

/// A single session row: title + a subtitle line (relative activity time,
/// branch, linked PR), a trailing checkmark for the current session, and a
/// spinner while a switch to this row is in flight.
private struct ClaudeSessionRow: View {
    let session: ClaudeSession
    let isCurrent: Bool
    let isSwitching: Bool

    var body: some View {
        HStack(spacing: 10) {
            VStack(alignment: .leading, spacing: 2) {
                Text(session.title)
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 8)
            if isSwitching {
                ProgressView().controlSize(.small)
            } else if isCurrent {
                Image(systemName: "checkmark.circle.fill")
                    .foregroundStyle(Color.accentColor)
                    .accessibilityLabel("Current session")
            }
        }
        .padding(.vertical, 2)
    }

    private var subtitle: String {
        var parts: [String] = [session.modTime.formatted(.relative(presentation: .named))]
        if !session.branch.isEmpty { parts.append(session.branch) }
        if !session.prRef.isEmpty { parts.append(session.prRef) }
        return parts.joined(separator: " \u{2022} ")
    }
}

import ArgusKit
import SwiftUI

/// The read-only Hera orchestration roster (`GET /api/hera`,
/// `internal/api/hera.go`): one section per orchestrator (coordinator +
/// worker roles) plus a separate Freelance section, mirroring the web SPA's
/// Hera tab (`internal/api/static/index.html`, `loadHera()`). Hera is
/// read-only over REST by design — there is no mutation UI here.
///
/// Toggled from the main window's toolbar in place of ``DetailView`` (see
/// ``AppState/showingHera``). Auto-refreshes every ~5s while on screen; the
/// poll loop lives in a `.task` modifier, which SwiftUI cancels automatically
/// when the view leaves the hierarchy, so there is nothing bespoke to tear
/// down on hide.
struct HeraTab: View {
    @Environment(AppState.self) private var app

    @State private var roster: HeraRoster?
    @State private var isLoading = true
    @State private var errorMessage: String?

    private static let pollInterval: Duration = .seconds(5)

    var body: some View {
        content
            .navigationTitle("Projects")
            .task {
                await load()
                while !_Concurrency.Task.isCancelled {
                    try? await _Concurrency.Task.sleep(for: Self.pollInterval)
                    if _Concurrency.Task.isCancelled { break }
                    await load(silent: true)
                }
            }
    }

    @ViewBuilder
    private var content: some View {
        if let errorMessage, roster == nil {
            ContentUnavailableView {
                Label("Couldn't load Hera roster", systemImage: "exclamationmark.triangle")
            } description: {
                Text(errorMessage)
            } actions: {
                Button("Retry") { _Concurrency.Task { await load() } }
            }
        } else if isLoading && roster == nil {
            ProgressView("Loading roster…")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let roster, roster.orchestrators.isEmpty && roster.freelance.isEmpty {
            ContentUnavailableView {
                Label("No active orchestrations", systemImage: "point.3.filled.connected.trianglepath.dotted")
            }
        } else if let roster {
            List {
                ForEach(roster.orchestrators) { orch in
                    Section {
                        ForEach(orch.roles) { role in
                            HeraRoleRow(role: role)
                        }
                    } header: {
                        HeraOrchestratorHeader(orch: orch)
                    }
                }
                if !roster.freelance.isEmpty {
                    Section("Freelance") {
                        ForEach(roster.freelance) { role in
                            HeraRoleRow(role: role)
                        }
                    }
                }
            }
            .listStyle(.inset)
        }
    }

    private func load(silent: Bool = false) async {
        if !silent { isLoading = true }
        do {
            roster = try await app.heraRoster()
            errorMessage = nil
        } catch is CancellationError {
            return
        } catch {
            // Never blank out an already-loaded roster on a transient poll
            // failure — only the initial load's error state gates the
            // full-screen error view (see `roster == nil` above).
            errorMessage = AppState.describe(error)
        }
        isLoading = false
    }
}

/// An orchestrator section header: the coordinator's name/bound task, pinned
/// marker, and role count. Internal (not private) so ``HeraDetailView`` can
/// reuse it for the dual-pane view's coordinator roster-list region
/// (`add-mac-hera-rail-toggle`, Stage 5).
struct HeraOrchestratorHeader: View {
    let orch: HeraOrchestrator

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "point.3.filled.connected.trianglepath.dotted")
            Text(orch.name)
            if orch.pinned {
                Image(systemName: "pin.fill")
                    .foregroundStyle(.secondary)
                    .font(.caption2)
            }
            if orch.archived {
                Text("archived")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Text("\(orch.roles.count)")
                .foregroundStyle(.secondary)
                .font(.caption)
        }
    }
}

/// One role row: kind badge, role name, bound task (if any) with a status
/// icon, and needs-input / ready-to-close markers. Clicking a row whose role
/// has a live binding selects that task in the main task list. Internal (not
/// private) so ``HeraDetailView`` can reuse it for the dual-pane view's
/// coordinator roster-list region (`add-mac-hera-rail-toggle`, Stage 5).
struct HeraRoleRow: View {
    let role: HeraRole

    @Environment(AppState.self) private var app

    private var boundTask: ArgusTask? {
        guard role.live, !role.taskID.isEmpty else { return nil }
        return app.tasks.first { $0.id == role.taskID }
    }

    var body: some View {
        Button {
            guard role.live, !role.taskID.isEmpty else { return }
            app.selectHeraTask(role.taskID)
        } label: {
            HStack(spacing: 8) {
                KindBadge(kind: role.kind)
                Text(role.name)
                    .lineLimit(1)
                if role.live {
                    Image(systemName: "chevron.right")
                        .font(.caption2)
                        .foregroundStyle(.tertiary)
                    if let boundTask {
                        TaskStatusIcon(task: boundTask)
                    } else {
                        RoleStatusIcon(role: role)
                    }
                    Text(role.taskName.isEmpty ? role.taskID : role.taskName)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                } else {
                    Text("unbound")
                        .foregroundStyle(.tertiary)
                        .font(.caption)
                }
                Spacer()
                if role.readyToClose {
                    Text("ready")
                        .font(.caption2)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(.green.opacity(0.15), in: Capsule())
                        .foregroundStyle(.green)
                }
                if role.status == "blocked" {
                    Image(systemName: "questionmark.circle.fill")
                        .foregroundStyle(.orange)
                        .help("Needs input")
                }
            }
        }
        .buttonStyle(.plain)
        .disabled(!role.live || role.taskID.isEmpty)
    }
}

/// Small kind badge (coordinator / worker / freelance).
private struct KindBadge: View {
    let kind: String

    var body: some View {
        Text(kind)
            .font(.caption2.weight(.medium))
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .background(color.opacity(0.15), in: Capsule())
            .foregroundStyle(color)
    }

    private var color: Color {
        switch kind {
        case "coordinator": return .purple
        case "worker": return .blue
        case "freelance": return .teal
        default: return .secondary
        }
    }
}

/// Fallback status icon for a role with no matching entry in the local task
/// list (e.g. its bound task fell outside the current snapshot) — derived
/// from the role's own `status` field (`idle` / `working` / `blocked` /
/// `done`) rather than ``TaskStatusIcon``'s richer task-based classifier.
private struct RoleStatusIcon: View {
    let role: HeraRole

    var body: some View {
        Image(systemName: descriptor.symbol)
            .foregroundStyle(descriptor.color)
            .symbolRenderingMode(.hierarchical)
            .help(descriptor.label)
    }

    private var descriptor: (symbol: String, color: Color, label: String) {
        switch role.status {
        case "blocked": return ("questionmark.circle.fill", .orange, "Blocked")
        case "working": return ("play.circle.fill", .green, "Working")
        case "done": return ("checkmark.circle.fill", .green, "Done")
        case "idle": return ("pause.circle.fill", .yellow, "Idle")
        default: return ("circle", .secondary, role.taskStatus.isEmpty ? "Unknown" : role.taskStatus)
        }
    }
}

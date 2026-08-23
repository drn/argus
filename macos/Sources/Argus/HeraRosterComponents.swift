import ArgusKit
import SwiftUI

/// An orchestrator section header: the coordinator's name/bound task, pinned
/// marker, and role count. Shared by ``HeraDetailView``'s coordinator
/// roster-list details region (`add-mac-hera-rail-toggle`, Stage 5).
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
/// icon, and needs-input / ready-to-close markers. Shared by
/// ``HeraDetailView``'s coordinator roster-list details region
/// (`add-mac-hera-rail-toggle`, Stage 5). Clicking a row selects that role in
/// the Hera-tree sidebar mode via ``AppState/selectHeraRole(_:)`` — including
/// an unbound role, which ``HeraDetailPaneResolver`` renders as "Unbound"
/// rather than requiring a live binding to be selectable.
struct HeraRoleRow: View {
    let role: HeraRole

    @Environment(AppState.self) private var app

    private var boundTask: ArgusTask? {
        guard role.live, !role.taskID.isEmpty else { return nil }
        return app.tasks.first { $0.id == role.taskID }
    }

    var body: some View {
        Button {
            app.selectHeraRole(role.roleID)
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
    }
}

/// Small kind badge (coordinator / worker / freelance). Shared by
/// ``HeraRoleRow`` and the Hera-tree sidebar's own role rows.
struct KindBadge: View {
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

import ArgusKit
import SwiftUI

/// The sidebar's nested Hera-tree mode (`add-mac-hera-rail-toggle`): top-level
/// orchestrators grouped by kanban status, with bridge-nested sub-orchestrators
/// folded under their parent's bridging role — mirroring the TUI rail's
/// structure, unlike ``HeraTab``'s flat all-orchestrators roster.
///
/// Mounted by ``Sidebar`` in place of the flat task list while
/// ``AppState/sidebarMode`` is `.hera`. Mirrors ``HeraTab``'s fetch-call and
/// poll-loop pattern (same `app.heraRoster()`-backed call via
/// ``AppState/refreshHeraRoster()``, same ~5s interval) rather than sharing
/// its private state, since `HeraTab` itself stays untouched until Stage 6
/// retires it.
struct HeraTreeSidebar: View {
    @Environment(AppState.self) private var app

    @State private var tree: HeraTree?
    @State private var isLoading = true
    @State private var errorMessage: String?

    private static let pollInterval: Duration = .seconds(5)

    var body: some View {
        content
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
        if let errorMessage, tree == nil {
            ContentUnavailableView {
                Label("Couldn't load Hera roster", systemImage: "exclamationmark.triangle")
            } description: {
                Text(errorMessage)
            } actions: {
                Button("Retry") { _Concurrency.Task { await load() } }
            }
        } else if isLoading && tree == nil {
            ProgressView("Loading roster…")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let tree, tree.sections.isEmpty && tree.freelance.isEmpty {
            ContentUnavailableView {
                Label("No active orchestrations", systemImage: "point.3.filled.connected.trianglepath.dotted")
            }
        } else if let tree {
            @Bindable var app = app
            List {
                ForEach(tree.sections) { section in
                    Section(Self.sectionTitle(section.status)) {
                        ForEach(section.orchestrators) { node in
                            HeraTreeNodeRow(node: node, foldState: $app.heraFoldState)
                        }
                    }
                }
                if !tree.freelance.isEmpty {
                    Section("Freelance") {
                        ForEach(tree.freelance) { role in
                            HeraTreeRoleRow(role: role)
                        }
                    }
                }
            }
            .listStyle(.sidebar)
        }
    }

    private static func sectionTitle(_ status: String) -> String {
        switch status {
        case "active": return "Active"
        case "backlog": return "Backlog"
        case "blocked": return "Blocked"
        case "done": return "Done"
        default: return status.capitalized
        }
    }

    private func load(silent: Bool = false) async {
        if !silent { isLoading = true }
        do {
            let roster = try await app.refreshHeraRoster()
            tree = HeraTreeBuilder.build(roster)
            errorMessage = nil
        } catch is CancellationError {
            return
        } catch {
            // Never blank an already-loaded tree on a transient poll failure —
            // only the initial load's error gates the full-screen error view.
            errorMessage = AppState.describe(error)
        }
        isLoading = false
    }
}

/// One row in the tree: recurses via ``DisclosureGroup`` for nodes with
/// children (an orchestrator's roles, or a role's bridged sub-orchestrator),
/// backed by ``HeraFoldState`` rather than SwiftUI's own implicit disclosure
/// state, so fold/expand is an explicit, testable, `AppState`-owned value
/// that survives a roster refresh via the node's stable id.
private struct HeraTreeNodeRow: View {
    let node: HeraTreeNode
    @Binding var foldState: HeraFoldState

    var body: some View {
        if node.children.isEmpty {
            rowLabel
        } else {
            DisclosureGroup(isExpanded: isExpandedBinding) {
                ForEach(node.children) { child in
                    HeraTreeNodeRow(node: child, foldState: $foldState)
                }
            } label: {
                rowLabel
            }
        }
    }

    private var isExpandedBinding: Binding<Bool> {
        Binding(
            get: { !foldState.isCollapsed(node.id) },
            set: { foldState.setCollapsed(node.id, !$0) }
        )
    }

    @ViewBuilder
    private var rowLabel: some View {
        switch node.kind {
        case .orchestrator(let orch):
            HeraTreeOrchestratorHeader(orch: orch)
        case .role(let role):
            HeraTreeRoleRow(role: role)
        }
    }
}

/// An orchestrator header row: name, pinned/archived markers, and a
/// subtree-needs-input indicator. Non-interactive, matching the TUI rail's
/// plain coordinator headers — only the disclosure triangle toggles fold state.
private struct HeraTreeOrchestratorHeader: View {
    let orch: HeraOrchestrator

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "point.3.filled.connected.trianglepath.dotted")
                .foregroundStyle(.purple)
            Text(orch.name)
                .fontWeight(.medium)
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
            if orch.subtreeNeedsInput {
                Image(systemName: "questionmark.circle.fill")
                    .foregroundStyle(.orange)
                    .font(.caption)
                    .help("A role in this orchestrator's subtree needs input")
            }
            Spacer()
            Text("\(orch.roles.count)")
                .foregroundStyle(.secondary)
                .font(.caption)
        }
    }
}

/// One role row: kind badge, name, bound-task status (if live), and
/// needs-input / ready-to-close markers. Tapping selects the role via
/// ``AppState/selectHeraRole(_:)`` — a separate selection channel from the
/// flat task list's ``AppState/selectedTaskID``, so switching sidebar modes
/// never disturbs either selection.
private struct HeraTreeRoleRow: View {
    let role: HeraRole

    @Environment(AppState.self) private var app

    private var boundTask: ArgusTask? {
        guard role.live, !role.taskID.isEmpty else { return nil }
        return app.tasks.first { $0.id == role.taskID }
    }

    private var isSelected: Bool {
        app.selectedHeraRoleID == role.roleID
    }

    var body: some View {
        Button {
            app.selectHeraRole(role.roleID)
        } label: {
            HStack(spacing: 8) {
                HeraKindBadge(kind: role.kind)
                Text(role.name)
                    .lineLimit(1)
                if role.live {
                    if let boundTask {
                        TaskStatusIcon(task: boundTask)
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
                if role.needsInput {
                    Image(systemName: "questionmark.circle.fill")
                        .foregroundStyle(.orange)
                        .help("Needs input")
                }
            }
        }
        .buttonStyle(.plain)
        .listRowBackground(isSelected ? Color.accentColor.opacity(0.15) : Color.clear)
    }
}

/// Small kind badge (coordinator / worker / freelance) — a copy of `HeraTab`'s
/// `KindBadge` (kept private there, and `HeraTab.swift` is untouched until
/// Stage 6 retires it).
private struct HeraKindBadge: View {
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

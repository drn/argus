import ArgusKit
import SwiftUI

/// The Hera-tree sidebar mode's detail area (`add-mac-hera-rail-toggle`,
/// Stage 5): mirrors the TUI's real geometry — the active orchestrator's own
/// coordinator pane alongside a second pane for the selected role, rather
/// than a single-task view. Mounted by ``ContentView`` in place of
/// ``DetailView`` only while ``AppState/sidebarMode`` is `.hera`; `DetailView`
/// itself is untouched.
///
/// The pane-selection DECISION lives in ``HeraDetailPaneResolver`` (pure,
/// unit-tested in `ArgusKitTests` — this view has no SwiftUI/AppState test
/// harness to exercise it directly) — this view only resolves the panes'
/// task/orchestrator ids to concrete ``ArgusTask``/``HeraOrchestrator``
/// values and renders them.
///
/// No dedicated poll loop: ``HeraTreeSidebar`` is always mounted alongside
/// this view (the sidebar + detail slots of the same `NavigationSplitView`)
/// and already polls ``AppState/refreshHeraRoster()`` on its own ~5s cadence,
/// keeping ``AppState/currentHeraRoster`` fresh — a second poll here would
/// just duplicate that fetch.
struct HeraDetailView: View {
    @Environment(AppState.self) private var app

    private var panes: HeraDetailPanes? {
        guard let roster = app.currentHeraRoster, let roleID = app.selectedHeraRoleID else { return nil }
        return HeraDetailPaneResolver.resolve(roster: roster, selectedRoleID: roleID)
    }

    var body: some View {
        if let panes {
            HStack(spacing: 0) {
                leftPane(taskID: panes.leftTaskID)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                Divider()
                rightPane(panes.right)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        } else {
            ContentUnavailableView {
                Label("No role selected", systemImage: "point.3.filled.connected.trianglepath.dotted")
            } description: {
                Text("Select a role from the Projects tree to see its details.")
            }
        }
    }

    @ViewBuilder
    private func leftPane(taskID: String?) -> some View {
        if let taskID, let task = app.tasks.first(where: { $0.id == taskID }) {
            TerminalTab(task: task)
        } else {
            unboundPlaceholder(title: "Coordinator unavailable")
        }
    }

    @ViewBuilder
    private func rightPane(_ pane: HeraDetailPanes.RightPane) -> some View {
        switch pane {
        case .none:
            unboundPlaceholder(title: "Unbound")
        case .terminal(let taskID):
            if let task = app.tasks.first(where: { $0.id == taskID }) {
                TerminalTab(task: task)
            } else {
                unboundPlaceholder(title: "Unbound")
            }
        case .roster(let orchID):
            if let orch = app.currentHeraRoster?.orchestrators.first(where: { $0.id == orchID }) {
                HeraRosterPane(orch: orch)
            } else {
                unboundPlaceholder(title: "Unbound")
            }
        }
    }

    private func unboundPlaceholder(title: String) -> some View {
        VStack(spacing: 8) {
            Text(title)
                .font(.title3)
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// The read-only roster-list details region shown in place of a terminal when
/// the selected role is itself a coordinator (design.md D4 / task 5.3).
/// Reuses ``HeraOrchestratorHeader``/``HeraRoleRow`` (`HeraRosterComponents.swift`)
/// rather than rebuilding them — scoped to this one orchestrator's own roles,
/// matching the TUI's per-orchestrator roster region. Purely a navigation
/// view: no Hera mutation control is presented here or anywhere else in this
/// file.
private struct HeraRosterPane: View {
    let orch: HeraOrchestrator

    var body: some View {
        List {
            Section {
                ForEach(orch.roles) { role in
                    HeraRoleRow(role: role)
                }
            } header: {
                HeraOrchestratorHeader(orch: orch)
            }
        }
        .listStyle(.inset)
    }
}

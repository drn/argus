import SwiftUI
import ArgusKit

/// The sidebar: a mode picker (``AppState/sidebarMode``,
/// `add-mac-hera-rail-toggle`) switching between the flat task list and
/// ``HeraTreeSidebar``'s nested Hera tree.
///
/// The flat task list (``taskList``) groups tasks into project folders
/// (mirroring the TUI's task list — see
/// `internal/tui/taskview/tasklist.go`'s `groupByProject`/`buildRows`): one
/// `Section` per project, holding tasks of every non-archived status.
/// Archived tasks live in their own collapsed section at the bottom, outside
/// the folders, matching the TUI's separate Archive section.
///
/// Parity gap: the TUI additionally floats `Pinned` tasks (across all
/// projects) above everything else. `pinned` isn't part of the REST
/// `GET /api/tasks` wire shape (see `ArgusKit.Task`), so there is no signal
/// to reproduce that section here — see `context/knowledge/gotchas/macos-app.md`.
struct Sidebar: View {
    @Environment(AppState.self) private var app
    @State private var archivedExpanded = false

    /// The sidebar's filter bar (see ``filterBar``): free-text substring
    /// filter over task name, view-local like ``archivedExpanded`` since
    /// neither needs to survive past this view's lifetime. Only shown in the
    /// flat task-list mode — filtering by task name has no meaning in the
    /// Hera tree.
    @State private var filterText = ""
    /// Whether hera-managed tasks (live Hera worker/coordinator bindings,
    /// ``AppState/heraManagedTaskIDs``) are shown. Defaults to `true` — every
    /// task visible, matching today's behavior with no regression — mirroring
    /// the TUI's `H` toggle default (see `internal/tui/taskview/tasklist.go`).
    @State private var showHeraManaged = true
    /// Moved into on Cmd+F via the hidden "Focus Filter" button in
    /// ``filterBar``.
    @FocusState private var filterFieldFocused: Bool

    var body: some View {
        @Bindable var app = app
        VStack(spacing: 0) {
            Picker("Sidebar Mode", selection: $app.sidebarMode) {
                Text("Tasks").tag(AppState.SidebarMode.tasks)
                Text("Projects").tag(AppState.SidebarMode.hera)
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .padding(.horizontal, 10)
            .padding(.vertical, 6)

            switch app.sidebarMode {
            case .tasks:
                taskList
            case .hera:
                HeraTreeSidebar()
            }
        }
        .navigationTitle("Argus")
        // Focus-scoped: only fires while the list itself has keyboard focus,
        // so a plain Delete/Backspace typed into the terminal pane (which is
        // most of what Delete is for, in a shell) never reaches this.
        .onDeleteCommand {
            guard let task = app.selectedTask else { return }
            app.pendingConfirmation = .delete(task)
        }
    }

    private var taskList: some View {
        @Bindable var app = app
        return VStack(spacing: 0) {
            filterBar
            List(selection: $app.selectedTaskID) {
                ForEach(filteredFolders(app.tasksByFolder)) { folder in
                    Section(folder.project) {
                        ForEach(folder.tasks) { task in
                            TaskRow(task: task).tag(task.id)
                        }
                    }
                }

                if !app.archivedTasks.isEmpty {
                    let archivedFolders = filteredFolders(app.archivedTasksByFolder)
                    Section(isExpanded: $archivedExpanded) {
                        ForEach(archivedFolders) { folder in
                            Section(folder.project) {
                                ForEach(folder.tasks) { task in
                                    TaskRow(task: task).tag(task.id)
                                }
                            }
                        }
                    } header: {
                        Text("Archived (\(app.archivedTasks.count))")
                    }
                }
            }
            .listStyle(.sidebar)
        }
    }

    /// The sidebar's filter bar: a free-text filter field (Cmd+F-focusable)
    /// plus a persistent, always-visible toggle for hera-managed task
    /// visibility — see spec.md's "Task rail filter access" requirement.
    private var filterBar: some View {
        HStack(spacing: 6) {
            HStack(spacing: 4) {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(.secondary)
                    .imageScale(.small)
                TextField("Filter tasks", text: $filterText)
                    .textFieldStyle(.plain)
                    .focused($filterFieldFocused)
                if !filterText.isEmpty {
                    Button {
                        filterText = ""
                    } label: {
                        Image(systemName: "xmark.circle.fill")
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                    .help("Clear filter")
                }
            }
            .padding(.horizontal, 6)
            .padding(.vertical, 4)
            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 6))

            Toggle(isOn: $showHeraManaged) {
                Image(systemName: "point.3.filled.connected.trianglepath.dotted")
            }
            .toggleStyle(.button)
            .controlSize(.small)
            .help(showHeraManaged ? "Hera-managed tasks shown (click to hide)"
                                   : "Hera-managed tasks hidden (click to show)")
            .accessibilityLabel("Show hera-managed tasks")

            // Cmd+F focuses the filter field above. A hidden button rather
            // than a `.commands` scene shortcut: it needs no menu-bar
            // presence and stays scoped to (and travels with) this view, the
            // same pattern already used for the toolbar's Cmd+N/Cmd+R
            // buttons — just visually hidden since there's no icon that
            // belongs in the filter bar for "focus the field above".
            Button("Focus Filter") {
                filterFieldFocused = true
            }
            .keyboardShortcut("f", modifiers: .command)
            .hidden()
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
    }

    /// Thin view-state adapter over ``ArgusKit/TaskFiltering/filteredFolders(_:filterText:showHeraManaged:heraManagedTaskIDs:)``
    /// — the actual filter predicate is a pure ArgusKit function (unit-tested
    /// in `TaskFilteringTests.swift`) so it can be exercised without
    /// instantiating this view's `@State`/`@Environment`.
    private func filteredFolders(_ folders: [TaskFolder]) -> [TaskFolder] {
        TaskFiltering.filteredFolders(folders, filterText: filterText,
                                       showHeraManaged: showHeraManaged,
                                       heraManagedTaskIDs: app.heraManagedTaskIDs)
    }
}

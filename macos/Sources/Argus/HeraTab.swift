import ArgusKit
import SwiftUI

/// The Hera orchestration roster (`GET /api/hera`, `internal/api/hera.go`):
/// one section per orchestrator (coordinator + worker roles) plus a separate
/// Freelance section, mirroring the web SPA's Hera tab
/// (`internal/api/static/index.html`, `loadHera()`). Also exposes the three
/// REST mutation surfaces (add-hera-mutation-rest-api) — spawn worker, send
/// message, plan-DAG authoring — reachable from an orchestrator with a live
/// coordinator, matching the web SPA's gating.
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

    // Mutation sheets — one `Identifiable` item per surface, matching the
    // `.sheet(item:)` pattern ``ContentView`` uses for renamingTask. Each
    // sheet closes over the *orchestrator at the moment the sheet opened*;
    // a mutation inside the sheet re-fetches the roster (`load(silent:)`)
    // but does not live-update the sheet's own pickers — closing and
    // reopening shows the fresh roster, which is an acceptable tradeoff for
    // this single-orchestrator-scoped surface.
    @State private var spawningOrch: HeraOrchestrator?
    @State private var sendingOrch: HeraOrchestrator?
    @State private var planningOrch: HeraOrchestrator?

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
            .sheet(item: $spawningOrch) { orch in
                HeraSpawnWorkerSheet(orch: orch) { await load(silent: true) }
            }
            .sheet(item: $sendingOrch) { orch in
                HeraSendMessageSheet(orch: orch) { await load(silent: true) }
            }
            .sheet(item: $planningOrch) { orch in
                HeraPlanSheet(orch: orch) { await load(silent: true) }
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
                        HeraOrchestratorHeader(orch: orch,
                            onSpawnWorker: { spawningOrch = orch },
                            onSendMessage: { sendingOrch = orch },
                            onPlan: { planningOrch = orch })
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
/// marker, role count, and — when the orchestrator has a live coordinator —
/// a menu exposing the three Hera mutation surfaces (add-hera-mutation-rest-api).
/// Mutation controls only make sense (and only succeed server-side) when the
/// orchestrator has a live coordinator; an archived orchestrator never does,
/// so gating on that also naturally hides the menu there — mirrors the web
/// SPA's `hasLiveCoordinator` gate in `renderHeraOrch`.
private struct HeraOrchestratorHeader: View {
    let orch: HeraOrchestrator
    var onSpawnWorker: () -> Void
    var onSendMessage: () -> Void
    var onPlan: () -> Void

    private var hasLiveCoordinator: Bool {
        orch.roles.contains { $0.kind == "coordinator" && $0.live }
    }

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
            if hasLiveCoordinator {
                Menu {
                    Button("Spawn Worker…", action: onSpawnWorker)
                    Button("Send Message…", action: onSendMessage)
                    Button("Plan…", action: onPlan)
                } label: {
                    Image(systemName: "ellipsis.circle")
                }
                .menuIndicator(.hidden)
                .buttonStyle(.plain)
                .fixedSize()
            }
        }
    }
}

/// One role row: kind badge, role name, bound task (if any) with a status
/// icon, and needs-input / ready-to-close markers. Clicking a row whose role
/// has a live binding selects that task in the main task list.
private struct HeraRoleRow: View {
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

// MARK: - Hera mutation sheets (add-hera-mutation-rest-api)
//
// Three surfaces — spawn worker, send message, plan-DAG authoring — each a
// sheet reachable from ``HeraOrchestratorHeader``'s menu. Every request acts
// as the target orchestrator's coordinator server-side; no sender/actor
// field is ever collected here. `onSuccess` re-fetches the roster (the same
// path the poll loop uses) so a mutation's effect is visible on the next
// tick without a manual reload.

/// `POST /api/hera/orchestrators/{orch_id}/workers` — spawn a born-bound
/// worker under `orch`, acting as its live coordinator.
private struct HeraSpawnWorkerSheet: View {
    let orch: HeraOrchestrator
    let onSuccess: () async -> Void

    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss

    @State private var prompt = ""
    @State private var roleName = ""
    @State private var project = ""
    @State private var branch = ""
    @State private var backend = ""
    @State private var model = ""
    @State private var isSubmitting = false
    @State private var errorMessage: String?

    private var promptIsBlank: Bool {
        prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Prompt (required)") {
                    TextEditor(text: $prompt).frame(minHeight: 80)
                }
                Section("Optional") {
                    TextField("Role name — derived from the prompt if omitted", text: $roleName)
                    TextField("Project — defaults to the coordinator's own", text: $project)
                    TextField("Branch", text: $branch)
                    TextField("Backend", text: $backend)
                    TextField("Model", text: $model)
                }
                if let errorMessage {
                    Text(errorMessage).foregroundStyle(.red)
                }
            }
            .formStyle(.grouped)
            .navigationTitle("Spawn worker — \(orch.name)")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Spawn") { _Concurrency.Task { await submit() } }
                        .disabled(promptIsBlank || isSubmitting)
                }
            }
        }
        .frame(minWidth: 420, minHeight: 420)
    }

    private func submit() async {
        isSubmitting = true
        errorMessage = nil
        do {
            _ = try await app.spawnHeraWorker(orchID: orch.id, HeraSpawnWorkerRequest(
                prompt: prompt,
                roleName: roleName.isEmpty ? nil : roleName,
                project: project.isEmpty ? nil : project,
                branch: branch.isEmpty ? nil : branch,
                backend: backend.isEmpty ? nil : backend,
                model: model.isEmpty ? nil : model))
            await onSuccess()
            dismiss()
        } catch {
            errorMessage = AppState.describe(error)
        }
        isSubmitting = false
    }
}

/// `POST /api/hera/orchestrators/{orch_id}/messages` — send from `orch`'s
/// coordinator to another role in the same orchestrator. No sender-role
/// picker is offered — sends are always attributed to the coordinator (the
/// REST send verb's coordinator-as-sender-only design).
private struct HeraSendMessageSheet: View {
    let orch: HeraOrchestrator
    let onSuccess: () async -> Void

    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss

    @State private var toRoleID: Int64?
    @State private var tldr = ""
    @State private var messageBody = ""
    @State private var isSubmitting = false
    @State private var errorMessage: String?

    private var recipients: [HeraRole] { orch.roles.filter { $0.kind != "coordinator" } }

    var body: some View {
        NavigationStack {
            Form {
                Picker("To", selection: $toRoleID) {
                    ForEach(recipients) { role in
                        Text("\(role.kind): \(role.name)").tag(Optional(role.roleID))
                    }
                }
                TextField("TLDR (required, ≤120 chars)", text: $tldr)
                    .onChange(of: tldr) { _, newValue in
                        if newValue.count > 120 { tldr = String(newValue.prefix(120)) }
                    }
                Section("Body") {
                    TextEditor(text: $messageBody).frame(minHeight: 100)
                }
                if let errorMessage {
                    Text(errorMessage).foregroundStyle(.red)
                }
            }
            .formStyle(.grouped)
            .navigationTitle("Send message — \(orch.name)")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Send") { _Concurrency.Task { await submit() } }
                        .disabled(toRoleID == nil || tldr.isEmpty || messageBody.isEmpty || isSubmitting)
                }
            }
        }
        .frame(minWidth: 420, minHeight: 380)
        .onAppear { if toRoleID == nil { toRoleID = recipients.first?.roleID } }
    }

    private func submit() async {
        guard let toRoleID else { return }
        isSubmitting = true
        errorMessage = nil
        do {
            _ = try await app.sendHeraMessage(orchID: orch.id,
                HeraSendMessageRequest(to: toRoleID, tldr: tldr, body: messageBody))
            await onSuccess()
            dismiss()
        } catch {
            errorMessage = AppState.describe(error)
        }
        isSubmitting = false
    }
}

/// Plan-DAG authoring for `orch` (add-hera-mutation-rest-api): create a
/// planned node, edit/cancel an existing one, and add/remove a blocking edge
/// — all addressed by `role_id` (unlike the MCP `hera_plan*` tools, which
/// address roles by name). Cancel and edge-removal are destructive-ish and
/// gated behind a confirmation dialog, matching this app's existing
/// stop/delete confirmation pattern (see `TaskActions.swift`).
private struct HeraPlanSheet: View {
    let orch: HeraOrchestrator
    let onSuccess: () async -> Void

    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss

    @State private var newName = ""
    @State private var newKind = "worker"
    @State private var newPromptOrGoal = ""
    @State private var newProject = ""

    @State private var editRoleID: Int64?
    @State private var editPrompt = ""
    @State private var editProject = ""
    @State private var showCancelConfirm = false

    @State private var blockedRoleID: Int64?
    @State private var blockerRoleID: Int64?
    @State private var showRemoveConfirm = false

    @State private var statusMessage: String?
    @State private var errorMessage: String?

    private var roles: [HeraRole] { orch.roles }

    var body: some View {
        NavigationStack {
            Form {
                Section("New planned node") {
                    TextField("Name", text: $newName)
                    Picker("Kind", selection: $newKind) {
                        Text("worker").tag("worker")
                        Text("subcoord").tag("subcoord")
                    }
                    TextField(newKind == "subcoord" ? "Goal" : "Prompt", text: $newPromptOrGoal)
                    TextField("Project (optional)", text: $newProject)
                    Button("Create node") { _Concurrency.Task { await createNode() } }
                        .disabled(newName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }

                Section("Edit / cancel a node") {
                    Picker("Node", selection: $editRoleID) {
                        Text("—").tag(Int64?.none)
                        ForEach(roles) { role in
                            Text("\(role.kind): \(role.name)").tag(Optional(role.roleID))
                        }
                    }
                    TextField("New prompt (optional)", text: $editPrompt)
                    TextField("New project (optional)", text: $editProject)
                    HStack {
                        Button("Cancel node", role: .destructive) { showCancelConfirm = true }
                            .disabled(editRoleID == nil)
                        Spacer()
                        Button("Save") { _Concurrency.Task { await updateNode() } }
                            .disabled(editRoleID == nil || (editPrompt.isEmpty && editProject.isEmpty))
                    }
                }

                Section("Blocking edges") {
                    Picker("Blocked (waits)", selection: $blockedRoleID) {
                        Text("—").tag(Int64?.none)
                        ForEach(roles) { role in
                            Text("\(role.kind): \(role.name)").tag(Optional(role.roleID))
                        }
                    }
                    Picker("Blocker (must finish first)", selection: $blockerRoleID) {
                        Text("—").tag(Int64?.none)
                        ForEach(roles) { role in
                            Text("\(role.kind): \(role.name)").tag(Optional(role.roleID))
                        }
                    }
                    HStack {
                        Button("Remove edge", role: .destructive) { showRemoveConfirm = true }
                            .disabled(blockedRoleID == nil || blockerRoleID == nil)
                        Spacer()
                        Button("Add edge") { _Concurrency.Task { await addEdge() } }
                            .disabled(blockedRoleID == nil || blockerRoleID == nil)
                    }
                }

                if let statusMessage {
                    Text(statusMessage).foregroundStyle(.secondary)
                }
                if let errorMessage {
                    Text(errorMessage).foregroundStyle(.red)
                }
            }
            .formStyle(.grouped)
            .navigationTitle("Plan — \(orch.name)")
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Close") { dismiss() }
                }
            }
            .confirmationDialog("Cancel this planned node? It will no longer materialize.",
                                 isPresented: $showCancelConfirm, titleVisibility: .visible) {
                Button("Cancel node", role: .destructive) { _Concurrency.Task { await cancelNode() } }
            }
            .confirmationDialog("Remove this blocking edge?",
                                 isPresented: $showRemoveConfirm, titleVisibility: .visible) {
                Button("Remove edge", role: .destructive) { _Concurrency.Task { await removeEdge() } }
            }
        }
        .frame(minWidth: 460, minHeight: 560)
    }

    private func createNode() async {
        errorMessage = nil
        do {
            let req = newKind == "subcoord"
                ? HeraPlanNodeCreateRequest(name: newName, kind: "subcoord", goal: newPromptOrGoal,
                                            project: newProject.isEmpty ? nil : newProject)
                : HeraPlanNodeCreateRequest(name: newName, prompt: newPromptOrGoal,
                                            project: newProject.isEmpty ? nil : newProject)
            let resp = try await app.createHeraPlanNode(orchID: orch.id, req)
            statusMessage = "Created \(resp.name)"
            newName = ""
            newPromptOrGoal = ""
            newProject = ""
            await onSuccess()
        } catch {
            errorMessage = AppState.describe(error)
        }
    }

    private func updateNode() async {
        guard let editRoleID else { return }
        errorMessage = nil
        do {
            _ = try await app.updateHeraPlanNode(orchID: orch.id, roleID: editRoleID, HeraPlanNodeUpdateRequest(
                prompt: editPrompt.isEmpty ? nil : editPrompt,
                project: editProject.isEmpty ? nil : editProject))
            statusMessage = "Node updated"
            await onSuccess()
        } catch {
            errorMessage = AppState.describe(error)
        }
    }

    private func cancelNode() async {
        guard let roleID = editRoleID else { return }
        errorMessage = nil
        do {
            _ = try await app.cancelHeraPlanNode(orchID: orch.id, roleID: roleID)
            statusMessage = "Node cancelled"
            editRoleID = nil
            await onSuccess()
        } catch {
            errorMessage = AppState.describe(error)
        }
    }

    private func addEdge() async {
        guard let blockedRoleID, let blockerRoleID else { return }
        errorMessage = nil
        do {
            _ = try await app.addHeraBlock(orchID: orch.id,
                HeraBlockCreateRequest(blockedRoleID: blockedRoleID, blockerRoleID: blockerRoleID))
            statusMessage = "Edge added"
            await onSuccess()
        } catch {
            errorMessage = AppState.describe(error)
        }
    }

    private func removeEdge() async {
        guard let blockedRoleID, let blockerRoleID else { return }
        errorMessage = nil
        do {
            _ = try await app.removeHeraBlock(orchID: orch.id, blockedRoleID: blockedRoleID, blockerRoleID: blockerRoleID)
            statusMessage = "Edge removed"
            await onSuccess()
        } catch {
            errorMessage = AppState.describe(error)
        }
    }
}

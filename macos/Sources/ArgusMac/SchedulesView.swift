import ArgusKit
import SwiftUI

/// The Schedules window (opened from the toolbar or ⇧⌘S): lists scheduled
/// tasks (`GET /api/schedules`) with Run Now / Delete per row, and a minimal
/// create/edit form sheet since ``ArgusClient`` already exposes
/// create/update endpoints. Mirrors the web SPA's Schedules block
/// (`internal/api/schedules.go`).
struct SchedulesView: View {
    @Environment(AppState.self) private var app

    @State private var schedules: [Schedule] = []
    @State private var isLoading = true
    @State private var errorMessage: String?
    @State private var actionError: String?
    @State private var isPresentingCreate = false
    @State private var editingSchedule: Schedule?
    @State private var pendingDelete: Schedule?

    var body: some View {
        NavigationStack {
            content
                .navigationTitle("Schedules")
                .toolbar {
                    ToolbarItem(placement: .primaryAction) {
                        Button {
                            isPresentingCreate = true
                        } label: {
                            Label("New Schedule", systemImage: "plus")
                        }
                    }
                }
        }
        .frame(minWidth: 560, minHeight: 420)
        .task { await load() }
        .sheet(isPresented: $isPresentingCreate) {
            ScheduleFormSheet(mode: .create) { req in
                try await app.createSchedule(req)
                await load()
            }
        }
        .sheet(item: $editingSchedule) { schedule in
            ScheduleFormSheet(mode: .edit(schedule)) { req in
                try await app.updateSchedule(id: schedule.id, req)
                await load()
            }
        }
        .confirmationDialog(
            "Delete \u{201C}\(pendingDelete?.name ?? "")\u{201D}?",
            isPresented: Binding(
                get: { pendingDelete != nil },
                set: { if !$0 { pendingDelete = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("Delete", role: .destructive) {
                if let schedule = pendingDelete {
                    _Concurrency.Task { await delete(schedule) }
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This cannot be undone.")
        }
    }

    @ViewBuilder
    private var content: some View {
        if let errorMessage, schedules.isEmpty {
            ContentUnavailableView {
                Label("Couldn't load schedules", systemImage: "exclamationmark.triangle")
            } description: {
                Text(errorMessage)
            } actions: {
                Button("Retry") { _Concurrency.Task { await load() } }
            }
        } else if isLoading && schedules.isEmpty {
            ProgressView("Loading schedules…")
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if schedules.isEmpty {
            ContentUnavailableView {
                Label("No schedules", systemImage: "clock")
            } description: {
                Text("Create a recurring or one-shot scheduled task.")
            }
        } else {
            List {
                if let actionError {
                    ScheduleActionErrorRow(message: actionError) { self.actionError = nil }
                }
                ForEach(schedules) { schedule in
                    ScheduleRow(
                        schedule: schedule,
                        onRunNow: { _Concurrency.Task { await runNow(schedule) } },
                        onEdit: { editingSchedule = schedule },
                        onDelete: { pendingDelete = schedule }
                    )
                }
            }
        }
    }

    private func load() async {
        isLoading = true
        do {
            schedules = try await app.schedules()
            errorMessage = nil
        } catch is CancellationError {
            return
        } catch {
            errorMessage = AppState.describe(error)
        }
        isLoading = false
    }

    private func runNow(_ schedule: Schedule) async {
        do {
            try await app.runSchedule(id: schedule.id)
            await load()
        } catch {
            actionError = "Failed to run \u{201C}\(schedule.name)\u{201D}: \(AppState.describe(error))"
        }
    }

    private func delete(_ schedule: Schedule) async {
        pendingDelete = nil
        do {
            try await app.deleteSchedule(id: schedule.id)
            await load()
        } catch {
            actionError = "Failed to delete \u{201C}\(schedule.name)\u{201D}: \(AppState.describe(error))"
        }
    }
}

/// One schedule row: name, project, cadence, backend/model, and last/next
/// run, with a Run Now button and a context menu for Edit/Delete.
private struct ScheduleRow: View {
    let schedule: Schedule
    let onRunNow: () -> Void
    let onEdit: () -> Void
    let onDelete: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(schedule.name)
                        .fontWeight(.medium)
                    if !schedule.enabled {
                        Text("disabled")
                            .font(.caption2)
                            .padding(.horizontal, 6)
                            .padding(.vertical, 1)
                            .background(.secondary.opacity(0.15), in: Capsule())
                            .foregroundStyle(.secondary)
                    }
                }
                Text(cadenceLabel)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .fontDesign(.monospaced)
                Text("\(schedule.project)\(backendSuffix)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                if let lastError = nonEmpty(schedule.lastError) {
                    Label(lastError, systemImage: "exclamationmark.triangle.fill")
                        .font(.caption2)
                        .foregroundStyle(.red)
                        .lineLimit(1)
                }
            }
            Spacer()
            VStack(alignment: .trailing, spacing: 3) {
                if let next = nonEmpty(schedule.nextRunAt) {
                    LabeledContent("Next", value: next).font(.caption2)
                }
                if let last = nonEmpty(schedule.lastRunAt) {
                    LabeledContent("Last", value: last).font(.caption2)
                }
            }
            .foregroundStyle(.secondary)
            Button("Run Now", action: onRunNow)
                .controlSize(.small)
        }
        .padding(.vertical, 2)
        .contextMenu {
            Button("Edit", action: onEdit)
            Button("Delete", role: .destructive, action: onDelete)
        }
    }

    private var cadenceLabel: String {
        if let runOnceAt = nonEmpty(schedule.runOnceAt) {
            return "once @ \(runOnceAt)"
        }
        return schedule.schedule
    }

    private var backendSuffix: String {
        var parts: [String] = []
        if let backend = nonEmpty(schedule.backend) { parts.append(backend) }
        if let model = nonEmpty(schedule.model) { parts.append(model) }
        return parts.isEmpty ? "" : " · \(parts.joined(separator: " / "))"
    }

    private func nonEmpty(_ value: String?) -> String? {
        guard let value, !value.isEmpty else { return nil }
        return value
    }
}

/// A transient, dismissible error row shown inline above the schedule list
/// (Run Now / Delete failures) — mirrors ``ActionErrorBanner``'s styling in a
/// list-row shape since this view has no overlay banner slot.
private struct ScheduleActionErrorRow: View {
    let message: String
    let dismiss: () -> Void

    var body: some View {
        HStack {
            Image(systemName: "exclamationmark.triangle.fill")
                .foregroundStyle(.red)
            Text(message)
                .font(.callout)
                .lineLimit(2)
            Spacer()
            Button {
                dismiss()
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .foregroundStyle(.secondary)
            }
            .buttonStyle(.plain)
        }
    }
}

/// Minimal create/edit form: name, project/backend pickers, cron expression,
/// and prompt — the fields ``ArgusKit/ScheduleRequest`` needs. One-shot
/// (`run_once_at`) schedules aren't editable here; only their cron-driven
/// siblings are.
private struct ScheduleFormSheet: View {
    enum Mode {
        case create
        case edit(Schedule)
    }

    let mode: Mode
    let onSubmit: (ScheduleRequest) async throws -> Void

    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss

    @State private var name = ""
    @State private var project = ""
    @State private var backend = ""
    @State private var cron = ""
    @State private var prompt = ""
    @State private var isSubmitting = false
    @State private var errorMessage: String?

    private var isEdit: Bool {
        if case .edit = mode { return true }
        return false
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(isEdit ? "Edit Schedule" : "New Schedule")
                .font(.title2.bold())
                .padding([.top, .horizontal])
                .padding(.bottom, 4)

            Form {
                Section {
                    TextField("Name", text: $name)
                    Picker("Project", selection: $project) {
                        if app.projectNames.isEmpty {
                            Text("No projects configured").tag("")
                        } else {
                            ForEach(app.projectNames, id: \.self) { Text($0).tag($0) }
                        }
                    }
                    Picker("Backend", selection: $backend) {
                        Text(defaultBackendLabel).tag("")
                        ForEach(app.backendNames, id: \.self) { Text($0).tag($0) }
                    }
                    TextField("Cron expression", text: $cron, prompt: Text("e.g. 0 9 * * 1-5"))
                        .fontDesign(.monospaced)
                }

                Section("Prompt") {
                    TextEditor(text: $prompt)
                        .frame(minHeight: 140)
                }

                if let errorMessage {
                    Label(errorMessage, systemImage: "exclamationmark.triangle.fill")
                        .foregroundStyle(.red)
                        .font(.callout)
                }
            }
            .formStyle(.grouped)

            Divider()

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button {
                    submit()
                } label: {
                    if isSubmitting {
                        ProgressView().controlSize(.small).frame(width: 70)
                    } else {
                        Text(isEdit ? "Save" : "Create").frame(width: 70)
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(!canSubmit || isSubmitting)
            }
            .padding()
        }
        .frame(width: 480, height: 480)
        .onAppear(perform: prefill)
    }

    private var defaultBackendLabel: String {
        app.defaultBackendName.isEmpty ? "Default" : "Default (\(app.defaultBackendName))"
    }

    private var canSubmit: Bool {
        !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !project.isEmpty
            && !cron.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func prefill() {
        guard case .edit(let schedule) = mode else {
            if project.isEmpty { project = app.projectNames.first ?? "" }
            return
        }
        name = schedule.name
        project = schedule.project
        backend = schedule.backend ?? ""
        cron = schedule.schedule
        prompt = schedule.prompt
    }

    private func submit() {
        guard canSubmit else { return }
        errorMessage = nil
        isSubmitting = true
        let req = ScheduleRequest(
            name: name.trimmingCharacters(in: .whitespacesAndNewlines),
            project: project,
            prompt: prompt,
            backend: backend.isEmpty ? nil : backend,
            schedule: cron.trimmingCharacters(in: .whitespacesAndNewlines)
        )
        _Concurrency.Task {
            do {
                try await onSubmit(req)
                isSubmitting = false
                dismiss()
            } catch {
                isSubmitting = false
                errorMessage = AppState.describe(error)
            }
        }
    }
}

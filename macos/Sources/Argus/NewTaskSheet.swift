import ArgusKit
import SwiftUI

/// The "New Task" sheet: a project + backend picker, a required multi-line
/// prompt, and an optional name (auto-generated server-side when left
/// blank). Presented from the toolbar `+` button and Cmd+N.
///
/// On success, ``AppState/createTask(_:)`` itself refreshes the task list,
/// selects the new task, and switches the detail pane to Terminal — this
/// view only needs to dismiss. On failure the error is shown inline and the
/// fields are left untouched so the user can retry.
struct NewTaskSheet: View {
    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss

    @State private var project = ""
    @State private var backend = ""
    @State private var account = ""
    /// The name the user last picked; survives backend changes, reset on project change.
    @State private var accountPicked = ""
    @State private var projectAccounts: [Account]?
    @State private var name = ""
    @State private var prompt = ""
    @State private var isSubmitting = false
    @State private var errorMessage: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("New Task")
                .font(.title2.bold())
                .padding([.top, .horizontal])
                .padding(.bottom, 4)

            Form {
                Section {
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

                    if showAccountPicker {
                        Picker("Account", selection: Binding(
                            get: { account },
                            set: { account = $0; accountPicked = $0 })) {
                            ForEach(usableAccounts) { Text($0.displayLabel).tag($0.name) }
                        }
                    }

                    TextField("Name (optional — auto-generated if blank)", text: $name)
                }

                Section("Prompt") {
                    TextEditor(text: $prompt)
                        .frame(minHeight: 140)
                        .font(.body)
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
                        ProgressView().controlSize(.small).frame(width: 90)
                    } else {
                        Text("Create & Start").frame(width: 90)
                    }
                }
                .keyboardShortcut(.defaultAction)
                .disabled(!canSubmit || isSubmitting)
            }
            .padding()
        }
        .frame(width: 480, height: 460)
        .onAppear {
            if project.isEmpty { project = app.projectNames.first ?? "" }
            reselectAccount()
        }
        .task(id: project) {
            accountPicked = ""
            projectAccounts = await app.accounts(forProject: project)
            reselectAccount()
        }
        .onChange(of: backend) { reselectAccount() }
    }

    private var allAccounts: [Account] { projectAccounts ?? app.accounts }

    private func reselectAccount() {
        account = NewTaskAccount.preselect(allAccounts, backend: effectiveBackend, picked: accountPicked)
    }

    private var effectiveBackend: String { backend.isEmpty ? app.defaultBackendName : backend }

    private var usableAccounts: [Account] {
        NewTaskAccount.accounts(allAccounts, forBackend: effectiveBackend)
    }

    private var showAccountPicker: Bool { usableAccounts.count > 1 }

    private var defaultBackendLabel: String {
        app.defaultBackendName.isEmpty ? "Default" : "Default (\(app.defaultBackendName))"
    }

    private var canSubmit: Bool {
        !project.isEmpty && !prompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func submit() {
        guard canSubmit else { return }
        errorMessage = nil
        isSubmitting = true
        let req = CreateTaskRequest(
            name: name.trimmingCharacters(in: .whitespacesAndNewlines),
            prompt: prompt,
            project: project,
            backend: backend.isEmpty ? nil : backend,
            account: NewTaskAccount.requestValue(
                selected: account, accounts: allAccounts, backend: effectiveBackend)
        )
        _Concurrency.Task {
            do {
                _ = try await app.createTask(req)
                isSubmitting = false
                dismiss()
            } catch {
                isSubmitting = false
                errorMessage = AppState.describe(error)
            }
        }
    }
}

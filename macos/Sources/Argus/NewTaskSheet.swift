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
        }
    }

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
            backend: backend.isEmpty ? nil : backend
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

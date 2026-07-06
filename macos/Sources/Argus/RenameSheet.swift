import ArgusKit
import SwiftUI

/// Inline rename sheet, pre-filled with the task's current name. Presented
/// from the sidebar row's context menu, the detail pane's overflow menu, and
/// Cmd+R. Fires ``AppState/rename(_:to:)`` and dismisses immediately — like
/// the other lifecycle actions, a failure surfaces via the non-blocking
/// ``ActionErrorBanner`` rather than blocking this sheet open.
struct RenameSheet: View {
    let task: ArgusTask

    @Environment(AppState.self) private var app
    @Environment(\.dismiss) private var dismiss

    @State private var name: String

    init(task: ArgusTask) {
        self.task = task
        _name = State(initialValue: task.name)
    }

    private var trimmedName: String {
        name.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text("Rename Task")
                .font(.title3.bold())

            TextField("Name", text: $name)
                .textFieldStyle(.roundedBorder)
                .onSubmit(submit)

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .keyboardShortcut(.cancelAction)
                Button("Rename") { submit() }
                    .keyboardShortcut(.defaultAction)
                    .disabled(trimmedName.isEmpty)
            }
        }
        .padding(20)
        .frame(width: 360)
    }

    private func submit() {
        guard !trimmedName.isEmpty else { return }
        let newName = trimmedName
        dismiss()
        _Concurrency.Task { await app.rename(task, to: newName) }
    }
}

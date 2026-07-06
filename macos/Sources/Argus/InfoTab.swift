import SwiftUI
import ArgusKit

/// The real Info tab: task fields plus clickable PR / branch links.
struct InfoTab: View {
    let task: ArgusTask

    @Environment(AppState.self) private var app
    @State private var links: [ArgusKit.Link] = []

    var body: some View {
        Form {
            Section("Task") {
                LabeledContent("Name", value: task.name)
                LabeledContent("Project", value: task.project)
                LabeledContent("Status", value: statusLabel)
                if let branch = nonEmpty(task.branch) {
                    LabeledContent("Branch", value: branch)
                }
                if let worktree = nonEmpty(task.worktreePath) {
                    LabeledContent("Worktree", value: worktree)
                }
                if let backend = nonEmpty(task.backend) {
                    LabeledContent("Backend", value: backend)
                }
                if let elapsed = nonEmpty(task.elapsed) {
                    LabeledContent("Elapsed", value: elapsed)
                }
                LabeledContent("Created", value: task.createdAt)
                // The REST task shape does not expose the session id; the task
                // id is the stable handle a later phase uses to attach a PTY.
                LabeledContent("Task ID", value: task.id)
                    .textSelection(.enabled)
            }

            if let prompt = nonEmpty(task.prompt) {
                Section("Prompt") {
                    Text(prompt)
                        .textSelection(.enabled)
                        .foregroundStyle(.secondary)
                }
            }

            Section("Links") {
                if links.isEmpty {
                    Text("No links found in this task's output.")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(links, id: \.url) { link in
                        LinkRow(link: link)
                    }
                }
            }
        }
        .formStyle(.grouped)
        .task(id: task.id) {
            links = await app.fetchLinks(taskID: task.id)
        }
    }

    private var statusLabel: String {
        var s = task.status
        if task.needsInput { s += " · needs input" }
        else if task.idle { s += " · idle" }
        if let pr = nonEmpty(task.prState) { s += " · PR: \(pr)" }
        return s
    }

    private func nonEmpty(_ value: String?) -> String? {
        guard let value, !value.isEmpty else { return nil }
        return value
    }
}

/// A single clickable link row, PR-badged when appropriate.
struct LinkRow: View {
    let link: ArgusKit.Link

    var body: some View {
        HStack(spacing: 8) {
            Image(systemName: link.isPR ? "arrow.triangle.pull" : "link")
                .foregroundStyle(link.isPR ? .purple : .secondary)
                .frame(width: 18)
            if let url = link.webURL {
                SwiftUI.Link(link.label.isEmpty ? link.url : link.label, destination: url)
                    .lineLimit(1)
                    .truncationMode(.middle)
            } else {
                Text(link.label.isEmpty ? link.url : link.label)
                    .lineLimit(1)
            }
        }
    }
}

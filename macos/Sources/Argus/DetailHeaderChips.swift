import ArgusKit
import SwiftUI

/// A compact chip row shown above the detail tabs so a task's branch and PR are
/// one click away from any tab (not just Info). PR chips are clickable links;
/// the branch chip copies the branch name.
struct DetailHeaderChips: View {
    let task: ArgusTask
    @Environment(AppState.self) private var app
    @State private var links: [ArgusKit.Link] = []

    var body: some View {
        Group {
            if hasContent {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        if let branch = branchName {
                            BranchChip(branch: branch)
                        }
                        ForEach(prLinks, id: \.url) { link in
                            PRChip(link: link, state: task.prState)
                        }
                    }
                    .padding(.horizontal, 12)
                    .padding(.vertical, 6)
                }
            }
        }
        .task(id: task.id) {
            links = await app.fetchLinks(taskID: task.id)
        }
    }

    private var branchName: String? {
        guard let b = task.branch, !b.isEmpty else { return nil }
        return b
    }

    private var prLinks: [ArgusKit.Link] {
        links.filter { $0.isPR && URL(string: $0.url) != nil }
    }

    private var hasContent: Bool { branchName != nil || !prLinks.isEmpty }
}

/// Non-link informational chip for the branch, with a copy-on-click affordance.
private struct BranchChip: View {
    let branch: String

    var body: some View {
        Button {
            let pb = NSPasteboard.general
            pb.clearContents()
            pb.setString(branch, forType: .string)
        } label: {
            chipBody(icon: "arrow.triangle.branch", text: branch, tint: .secondary)
        }
        .buttonStyle(.plain)
        .help("Copy branch name")
    }
}

/// Clickable chip that opens a pull request, badged with its review state when
/// known.
private struct PRChip: View {
    let link: ArgusKit.Link
    let state: String?

    var body: some View {
        if let url = link.webURL {
            SwiftUI.Link(destination: url) {
                chipBody(icon: "arrow.triangle.pull", text: label, tint: .purple)
            }
            .buttonStyle(.plain)
            .help(link.url)
        }
    }

    private var label: String {
        if let s = state, !s.isEmpty { return "\(shortLabel) · \(s)" }
        return shortLabel
    }

    /// Prefer a "#123"-style label if the link text carries one, else "PR".
    private var shortLabel: String {
        let l = link.label.trimmingCharacters(in: .whitespaces)
        return l.isEmpty ? "PR" : l
    }
}

/// Shared chip visual — a tinted, capsule-bordered icon + text pill.
@ViewBuilder
private func chipBody(icon: String, text: String, tint: Color) -> some View {
    HStack(spacing: 5) {
        Image(systemName: icon)
        Text(text)
            .lineLimit(1)
            .truncationMode(.middle)
    }
    .font(.caption.weight(.medium))
    .foregroundStyle(tint)
    .padding(.horizontal, 9)
    .padding(.vertical, 4)
    .background(tint.opacity(0.12), in: Capsule())
    .overlay(Capsule().stroke(tint.opacity(0.25)))
}

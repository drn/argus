import AppKit
import ArgusKit
import SwiftUI

/// The Files tab: the worktree's changed-file tree (from the `/files`
/// endpoint), rendered as a disclosure outline. There is no file-content
/// endpoint, so rows offer Copy Path / Reveal in Finder rather than a preview.
struct FilesTab: View {
    let task: ArgusTask
    @Environment(AppState.self) private var app
    @State private var model = FilesTabModel()

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            content
        }
        .task(id: task.id) {
            await model.load(app: app, taskID: task.id)
        }
    }

    private var header: some View {
        HStack(spacing: 12) {
            Label("Changed files", systemImage: "folder")
                .font(.callout.weight(.medium))
            if model.fileCount > 0 {
                Text("\(model.fileCount)")
                    .font(.caption.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            Spacer()
            Button {
                _Concurrency.Task { await model.load(app: app, taskID: task.id) }
            } label: {
                if model.isRefreshing {
                    ProgressView().controlSize(.small)
                } else {
                    Image(systemName: "arrow.clockwise")
                }
            }
            .buttonStyle(.borderless)
            .help("Refresh files")
            .disabled(model.isRefreshing)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    @ViewBuilder
    private var content: some View {
        switch model.phase {
        case .idle, .loading:
            Spacer()
            ProgressView("Loading files\u{2026}").controlSize(.large)
            Spacer()
        case .failed(let message):
            Spacer()
            ContentUnavailableView {
                Label("Couldn't load files", systemImage: "exclamationmark.triangle")
            } description: {
                Text(message)
            } actions: {
                Button("Retry") { _Concurrency.Task { await model.load(app: app, taskID: task.id) } }
                    .buttonStyle(.borderedProminent)
            }
            Spacer()
        case .loaded where model.nodes.isEmpty:
            Spacer()
            ContentUnavailableView("No changed files", systemImage: "folder",
                                   description: Text("This worktree has no modified or untracked files."))
            Spacer()
        case .loaded:
            List {
                OutlineGroup(model.nodes, children: \.outlineChildren) { node in
                    FileRow(node: node, worktreePath: task.worktreePath)
                }
            }
            .listStyle(.sidebar)
        }
    }
}

/// One row in the file outline: an icon, the name, and (for files) a status
/// badge, with a copy/reveal context menu.
private struct FileRow: View {
    let node: FileTreeNode
    let worktreePath: String?

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: node.isDirectory ? "folder" : symbol(for: node.name))
                .foregroundStyle(node.isDirectory ? Color.accentColor : .secondary)
                .frame(width: 16)
            Text(node.name)
                .lineLimit(1)
                .truncationMode(.middle)
            Spacer(minLength: 4)
            if let status = node.status {
                Text(status)
                    .font(.caption2.weight(.bold).monospaced())
                    .foregroundStyle(GitColors.status(status))
            }
        }
        .contextMenu {
            Button("Copy Path") { copy(fullPath ?? node.path) }
            Button("Copy Relative Path") { copy(node.path) }
            if fullPath != nil {
                Button("Reveal in Finder") { reveal() }
            }
        }
    }

    /// Absolute path when the worktree root is known and absolute.
    private var fullPath: String? {
        guard let wt = worktreePath, !wt.isEmpty, (wt as NSString).isAbsolutePath else { return nil }
        return (wt as NSString).appendingPathComponent(node.path)
    }

    private func copy(_ string: String) {
        let pb = NSPasteboard.general
        pb.clearContents()
        pb.setString(string, forType: .string)
    }

    private func reveal() {
        guard let full = fullPath else { return }
        NSWorkspace.shared.selectFile(full, inFileViewerRootedAtPath: "")
    }

    /// A best-effort SF Symbol for a file, keyed off its extension.
    private func symbol(for name: String) -> String {
        let ext = (name as NSString).pathExtension.lowercased()
        switch ext {
        case "swift", "go", "rs", "c", "h", "cpp", "cc", "m", "mm",
             "js", "ts", "jsx", "tsx", "py", "rb", "java", "kt", "sh", "zsh":
            return "chevron.left.forwardslash.chevron.right"
        case "json", "yaml", "yml", "toml", "xml", "plist", "ini", "cfg", "conf":
            return "curlybraces"
        case "md", "markdown", "txt", "rst":
            return "doc.text"
        case "png", "jpg", "jpeg", "gif", "svg", "webp", "heic", "icns", "tiff":
            return "photo"
        case "pdf":
            return "doc.richtext"
        case "zip", "gz", "tar", "tgz", "bz2", "xz", "7z":
            return "doc.zipper"
        case "lock", "sum", "mod":
            return "lock.doc"
        default:
            return "doc"
        }
    }
}

// MARK: - Model

/// Owns the Files tab's fetch → tree-build pipeline and loading/error state.
@MainActor
@Observable
final class FilesTabModel {
    enum Phase: Equatable {
        case idle
        case loading
        case loaded
        case failed(String)
    }

    private(set) var phase: Phase = .idle
    private(set) var nodes: [FileTreeNode] = []
    private(set) var fileCount = 0
    private(set) var isRefreshing = false

    private var generation = 0

    func load(app: AppState, taskID: String) async {
        generation += 1
        let gen = generation
        isRefreshing = true
        if nodes.isEmpty { phase = .loading }

        do {
            // "." lists the worktree root recursively — the endpoint returns an
            // empty set for an empty `dir`.
            let tree = try await app.fileTree(taskID: taskID, dir: ".")
            guard gen == generation else { return }
            nodes = FileTreeBuilder.build(tree.files)
            fileCount = tree.files.count
            phase = .loaded
        } catch {
            guard gen == generation else { return }
            phase = .failed(AppState.describe(error))
        }
        isRefreshing = false
    }
}

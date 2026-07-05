import ArgusKit
import SwiftUI

/// The Diff tab: a git status strip, a summary row, and per-file collapsible
/// sections of the parsed unified diff. Fetches `git status` once to learn the
/// changed paths, then fans out per-file `git diff` requests, concatenates them,
/// and parses the result with ``DiffParser``.
struct DiffTab: View {
    let task: ArgusTask
    @Environment(AppState.self) private var app
    @State private var model = DiffTabModel()
    /// File ids the user has collapsed (default: everything expanded).
    @State private var collapsed: Set<String> = []

    var body: some View {
        VStack(spacing: 0) {
            GitStatusStrip(task: task, status: model.status,
                           summary: model.summary,
                           isRefreshing: model.isRefreshing) {
                _Concurrency.Task { await model.load(app: app, taskID: task.id) }
            }
            Divider()
            content
        }
        .task(id: task.id) {
            await model.load(app: app, taskID: task.id)
        }
    }

    @ViewBuilder
    private var content: some View {
        switch model.phase {
        case .idle, .loading:
            Spacer()
            ProgressView("Loading diff\u{2026}").controlSize(.large)
            Spacer()
        case .failed(let message):
            Spacer()
            ContentUnavailableView {
                Label("Couldn't load diff", systemImage: "exclamationmark.triangle")
            } description: {
                Text(message)
            } actions: {
                Button("Retry") { _Concurrency.Task { await model.load(app: app, taskID: task.id) } }
                    .buttonStyle(.borderedProminent)
            }
            Spacer()
        case .loaded where model.files.isEmpty:
            Spacer()
            ContentUnavailableView("No changes", systemImage: "checkmark.circle",
                                   description: Text("This worktree has no diff against its base."))
            Spacer()
        case .loaded:
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 10) {
                    ForEach(model.files) { file in
                        FileDiffSection(
                            file: file,
                            isExpanded: Binding(
                                get: { !collapsed.contains(file.id) },
                                set: { expand in
                                    if expand { collapsed.remove(file.id) }
                                    else { collapsed.insert(file.id) }
                                }))
                    }
                }
                .padding(12)
            }
        }
    }
}

// MARK: - Git status strip

/// The strip above the diff: branch, working-tree state, and the change
/// summary, with a refresh button.
private struct GitStatusStrip: View {
    let task: ArgusTask
    let status: GitStatus?
    let summary: DiffSummary
    let isRefreshing: Bool
    let onRefresh: () -> Void

    var body: some View {
        HStack(spacing: 12) {
            Label(branchName, systemImage: "arrow.triangle.branch")
                .font(.callout.weight(.medium))
                .lineLimit(1)
                .truncationMode(.middle)

            if let dirty = dirtyLabel {
                Text(dirty)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            if summary.fileCount > 0 {
                DiffSummaryBadge(summary: summary)
            }

            Button(action: onRefresh) {
                if isRefreshing {
                    ProgressView().controlSize(.small)
                } else {
                    Image(systemName: "arrow.clockwise")
                }
            }
            .buttonStyle(.borderless)
            .help("Refresh diff")
            .disabled(isRefreshing)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private var branchName: String {
        if let b = task.branch, !b.isEmpty { return b }
        return "(no branch)"
    }

    /// git status --short is empty when the working tree is clean.
    private var dirtyLabel: String? {
        guard let status else { return nil }
        let lines = status.status.split(separator: "\n").filter { !$0.trimmingCharacters(in: .whitespaces).isEmpty }
        if lines.isEmpty { return "working tree clean" }
        return "\(lines.count) uncommitted"
    }
}

/// The compact "N files +A −R" badge.
private struct DiffSummaryBadge: View {
    let summary: DiffSummary

    var body: some View {
        HStack(spacing: 8) {
            Text("\(summary.fileCount) \(summary.fileCount == 1 ? "file" : "files")")
                .foregroundStyle(.secondary)
            Text("+\(summary.added)")
                .foregroundStyle(GitColors.marker(.added))
            Text("\u{2212}\(summary.removed)")
                .foregroundStyle(GitColors.marker(.removed))
        }
        .font(.caption.monospacedDigit())
    }
}

// MARK: - Per-file section

private struct FileDiffSection: View {
    let file: DiffFile
    @Binding var isExpanded: Bool

    var body: some View {
        DisclosureGroup(isExpanded: $isExpanded) {
            if file.isBinary {
                Text("Binary file not shown")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .padding(.vertical, 6)
                    .padding(.leading, 4)
            } else if file.hunks.isEmpty {
                Text(file.isRename ? "Renamed with no content changes"
                                   : "No textual changes")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .padding(.vertical, 6)
                    .padding(.leading, 4)
            } else {
                ScrollView(.horizontal, showsIndicators: true) {
                    LazyVStack(alignment: .leading, spacing: 0) {
                        ForEach(file.hunks) { hunk in
                            HunkHeaderRow(header: hunk.header)
                            ForEach(hunk.lines) { line in
                                DiffLineRow(line: line)
                            }
                        }
                    }
                    .padding(.vertical, 2)
                }
                .background(Color(nsColor: .textBackgroundColor))
                .clipShape(RoundedRectangle(cornerRadius: 6))
                .overlay(RoundedRectangle(cornerRadius: 6).stroke(.quaternary))
            }
        } label: {
            FileHeaderLabel(file: file)
        }
    }
}

/// A file's disclosure label: status badge, path, and per-file +/- counts.
private struct FileHeaderLabel: View {
    let file: DiffFile

    var body: some View {
        HStack(spacing: 8) {
            Text(statusCode)
                .font(.caption2.weight(.bold).monospaced())
                .frame(width: 16, height: 16)
                .foregroundStyle(.white)
                .background(GitColors.status(statusCode), in: RoundedRectangle(cornerRadius: 3))

            Text(file.displayPath)
                .font(.callout.weight(.medium))
                .lineLimit(1)
                .truncationMode(.middle)
                .help(file.displayPath)

            Spacer(minLength: 8)

            if !file.isBinary {
                Text("+\(file.addedCount)")
                    .foregroundStyle(GitColors.marker(.added))
                Text("\u{2212}\(file.removedCount)")
                    .foregroundStyle(GitColors.marker(.removed))
            }
        }
        .font(.caption.monospacedDigit())
        .contentShape(Rectangle())
    }

    private var statusCode: String {
        if file.isNew { return "A" }
        if file.isDeleted { return "D" }
        if file.isRename { return "R" }
        return "M"
    }
}

private struct HunkHeaderRow: View {
    let header: String

    var body: some View {
        Text(header)
            .font(.system(size: 11.5, design: .monospaced))
            .foregroundStyle(.secondary)
            .padding(.horizontal, 6)
            .padding(.vertical, 2)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(Color.accentColor.opacity(0.08))
    }
}

private struct DiffLineRow: View {
    let line: DiffLine

    var body: some View {
        HStack(alignment: .top, spacing: 0) {
            gutter(line.oldLineNumber)
            gutter(line.newLineNumber)
            Text(marker)
                .frame(width: 14, alignment: .center)
                .foregroundStyle(GitColors.marker(line.kind))
            Text(displayText)
                .textSelection(.enabled)
                .foregroundStyle(.primary)
            Spacer(minLength: 0)
        }
        .font(.system(size: 12, design: .monospaced))
        .padding(.vertical, 0.5)
        .background(GitColors.lineBackground(line.kind))
    }

    private func gutter(_ number: Int?) -> some View {
        Text(number.map(String.init) ?? "")
            .font(.system(size: 11, design: .monospaced))
            .foregroundStyle(.tertiary)
            .frame(width: 44, alignment: .trailing)
            .padding(.trailing, 6)
    }

    private var marker: String {
        switch line.kind {
        case .added: return "+"
        case .removed: return "\u{2212}"
        case .context: return " "
        }
    }

    private var displayText: String {
        // Surface the "no newline at end of file" marker inline so it isn't
        // silently lost.
        line.noNewlineAtEOF ? line.text + "  \u{29BF} no newline at EOF" : line.text
    }
}

// MARK: - Model

/// The summary tuple shown in the strip's badge.
struct DiffSummary: Equatable {
    var fileCount = 0
    var added = 0
    var removed = 0
}

/// Owns the Diff tab's fetch → parse pipeline and its loading/error state. A
/// per-load `generation` counter drops stale results when the user switches
/// tasks or refreshes mid-flight.
@MainActor
@Observable
final class DiffTabModel {
    enum Phase: Equatable {
        case idle
        case loading
        case loaded
        case failed(String)
    }

    private(set) var phase: Phase = .idle
    private(set) var files: [DiffFile] = []
    private(set) var status: GitStatus?
    private(set) var summary = DiffSummary()
    /// True during a refresh that is layered over already-visible content.
    private(set) var isRefreshing = false

    private var generation = 0

    /// Concurrency cap for the per-file diff fan-out — the daemon is local, but
    /// an unbounded fan-out on a worktree with hundreds of files is pointless
    /// pressure.
    private let poolSize = 6

    func load(app: AppState, taskID: String) async {
        generation += 1
        let gen = generation
        isRefreshing = true
        if files.isEmpty { phase = .loading }

        do {
            let st = try await app.gitStatus(taskID: taskID)
            guard gen == generation else { return }
            status = st

            let changed = GitChangeSummary.changedPaths(status: st.status,
                                                        branchFiles: st.branchFiles)
            if changed.isEmpty {
                files = []
                summary = DiffSummary()
                phase = .loaded
                isRefreshing = false
                return
            }

            let diffs = await fetchDiffs(app: app, taskID: taskID,
                                         paths: changed.map(\.path))
            guard gen == generation else { return }

            let parsed = DiffParser.parse(diffs.joined(separator: "\n"))
            files = parsed
            summary = DiffSummary(
                fileCount: parsed.count,
                added: parsed.reduce(0) { $0 + $1.addedCount },
                removed: parsed.reduce(0) { $0 + $1.removedCount })
            phase = .loaded
        } catch {
            guard gen == generation else { return }
            phase = .failed(AppState.describe(error))
        }
        isRefreshing = false
    }

    /// Fetches each path's diff with a bounded worker pool, preserving input
    /// order. A per-file failure degrades to an empty diff rather than failing
    /// the whole tab.
    private func fetchDiffs(app: AppState, taskID: String,
                            paths: [String]) async -> [String] {
        var result = [String](repeating: "", count: paths.count)
        await withTaskGroup(of: (Int, String).self) { group in
            var next = 0
            let initial = min(poolSize, paths.count)
            while next < initial {
                let i = next
                group.addTask { (i, await Self.diffText(app: app, taskID: taskID, path: paths[i])) }
                next += 1
            }
            for await (idx, text) in group {
                result[idx] = text
                if next < paths.count {
                    let i = next
                    group.addTask { (i, await Self.diffText(app: app, taskID: taskID, path: paths[i])) }
                    next += 1
                }
            }
        }
        return result
    }

    private static func diffText(app: AppState, taskID: String, path: String) async -> String {
        (try? await app.gitDiff(taskID: taskID, path: path))?.diff ?? ""
    }
}

import Foundation

// A pure unified-diff parser. Fed the raw `git diff` text the daemon returns
// from `GET /api/tasks/{id}/git/diff` (one file per call) — concatenating
// several such stanzas yields a valid multi-file diff, which this parser
// splits back apart on `diff --git` boundaries.
//
// Kept dependency-free and side-effect-free so it is trivially unit-tested;
// all rendering lives in the ArgusMac layer.

/// The role of a single line inside a hunk.
public enum DiffLineKind: String, Sendable, Equatable {
    case context
    case added
    case removed
}

/// One line of a hunk. `text` is the content with the leading `+`/`-`/space
/// marker stripped. `oldLineNumber` is nil for added lines, `newLineNumber` is
/// nil for removed lines.
public struct DiffLine: Sendable, Equatable, Identifiable {
    public let kind: DiffLineKind
    public let text: String
    public let oldLineNumber: Int?
    public let newLineNumber: Int?
    /// True when this line is immediately followed by a
    /// `\ No newline at end of file` marker.
    public let noNewlineAtEOF: Bool

    /// Stable within a hunk: old/new line pair is unique per line.
    public var id: String { "\(oldLineNumber.map(String.init) ?? "-")·\(newLineNumber.map(String.init) ?? "-")·\(kind.rawValue)" }

    public init(kind: DiffLineKind, text: String, oldLineNumber: Int?,
                newLineNumber: Int?, noNewlineAtEOF: Bool = false) {
        self.kind = kind
        self.text = text
        self.oldLineNumber = oldLineNumber
        self.newLineNumber = newLineNumber
        self.noNewlineAtEOF = noNewlineAtEOF
    }
}

/// A single `@@ … @@` hunk.
public struct DiffHunk: Sendable, Equatable, Identifiable {
    /// The full header line (e.g. `@@ -1,4 +1,6 @@ func foo()`), preserved so
    /// the UI can show the section context git emits after the second `@@`.
    public let header: String
    public let oldStart: Int
    public let oldCount: Int
    public let newStart: Int
    public let newCount: Int
    public let lines: [DiffLine]

    public var id: String { header + "·\(oldStart)·\(newStart)" }

    public init(header: String, oldStart: Int, oldCount: Int,
                newStart: Int, newCount: Int, lines: [DiffLine]) {
        self.header = header
        self.oldStart = oldStart
        self.oldCount = oldCount
        self.newStart = newStart
        self.newCount = newCount
        self.lines = lines
    }
}

/// One file's worth of changes.
public struct DiffFile: Sendable, Equatable, Identifiable {
    /// nil for a newly-added file (old side was `/dev/null`).
    public let oldPath: String?
    /// nil for a deleted file (new side was `/dev/null`).
    public let newPath: String?
    public let isBinary: Bool
    public let isNew: Bool
    public let isDeleted: Bool
    public let isRename: Bool
    public let hunks: [DiffHunk]

    public init(oldPath: String?, newPath: String?, isBinary: Bool,
                isNew: Bool, isDeleted: Bool, isRename: Bool, hunks: [DiffHunk]) {
        self.oldPath = oldPath
        self.newPath = newPath
        self.isBinary = isBinary
        self.isNew = isNew
        self.isDeleted = isDeleted
        self.isRename = isRename
        self.hunks = hunks
    }

    /// A human-facing label: `old → new` for renames, otherwise the surviving
    /// path.
    public var displayPath: String {
        if isRename, let o = oldPath, let n = newPath, o != n {
            return "\(o) → \(n)"
        }
        return newPath ?? oldPath ?? "(unknown)"
    }

    /// Stable identity for `ForEach` — the path pair is unique within a diff.
    public var id: String { "\(oldPath ?? "∅")→\(newPath ?? "∅")" }

    public var addedCount: Int {
        hunks.reduce(0) { $0 + $1.lines.lazy.filter { $0.kind == .added }.count }
    }

    public var removedCount: Int {
        hunks.reduce(0) { $0 + $1.lines.lazy.filter { $0.kind == .removed }.count }
    }
}

/// Parses raw unified-diff text into structured files → hunks → lines.
public enum DiffParser {
    /// Parse a (possibly multi-file) unified diff. Returns an empty array for
    /// empty or whitespace-only input.
    public static func parse(_ text: String) -> [DiffFile] {
        if text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { return [] }

        var files: [DiffFile] = []
        var cur: Accumulator?

        func flush() {
            if let c = cur { files.append(c.build()) }
            cur = nil
        }

        for rawLine in text.split(separator: "\n", omittingEmptySubsequences: false).map(String.init) {
            if rawLine.hasPrefix("diff --git ") {
                flush()
                cur = Accumulator()
                cur?.applyGitHeader(rawLine)
                continue
            }
            // Skip any preamble before the first `diff --git`.
            guard cur != nil else { continue }
            cur?.consume(rawLine)
        }
        flush()
        return files
    }

    // MARK: - Path helpers

    /// Strips a `a/`/`b/` prefix; maps `/dev/null` to nil.
    static func stripPathPrefix(_ raw: String) -> String? {
        if raw == "/dev/null" { return nil }
        if raw.hasPrefix("a/") || raw.hasPrefix("b/") { return String(raw.dropFirst(2)) }
        return raw
    }

    // MARK: - Per-file accumulator

    private final class Accumulator {
        var oldPath: String?
        var newPath: String?
        var isBinary = false
        var explicitNew = false
        var explicitDeleted = false
        var isRename = false
        var sawOldDevNull = false
        var sawNewDevNull = false

        private var hunks: [DiffHunk] = []
        // In-progress hunk state.
        private var hHeader: String?
        private var hOldStart = 0, hOldCount = 0, hNewStart = 0, hNewCount = 0
        private var hLines: [DiffLine] = []
        private var oldCursor = 0, newCursor = 0

        // `diff --git a/old b/new` — best-effort fallback for paths (renames
        // and binary stanzas that carry no `---`/`+++`). Overridden by the
        // more reliable `--- `/`+++ `/`rename from|to` lines when present.
        func applyGitHeader(_ line: String) {
            let rest = String(line.dropFirst("diff --git ".count))
            if let r = rest.range(of: " b/") {
                let a = String(rest[..<r.lowerBound])
                let b = String(rest[r.upperBound...])
                oldPath = a.hasPrefix("a/") ? String(a.dropFirst(2)) : a
                newPath = b
            }
        }

        func consume(_ line: String) {
            if line.hasPrefix("@@") {
                startHunk(line)
                return
            }
            if hHeader != nil {
                consumeHunkLine(line)
                return
            }
            // File-metadata lines (only meaningful before the first hunk).
            if line.hasPrefix("new file mode") { explicitNew = true; return }
            if line.hasPrefix("deleted file mode") { explicitDeleted = true; return }
            if line.hasPrefix("rename from ") {
                oldPath = String(line.dropFirst("rename from ".count)); isRename = true; return
            }
            if line.hasPrefix("rename to ") {
                newPath = String(line.dropFirst("rename to ".count)); isRename = true; return
            }
            if line.hasPrefix("copy from ") {
                oldPath = String(line.dropFirst("copy from ".count)); return
            }
            if line.hasPrefix("copy to ") {
                newPath = String(line.dropFirst("copy to ".count)); return
            }
            if line.hasPrefix("Binary files ") || line.hasPrefix("GIT binary patch") {
                isBinary = true; return
            }
            if line.hasPrefix("--- ") {
                let raw = String(line.dropFirst(4))
                let path = raw.split(separator: "\t", maxSplits: 1).first.map(String.init) ?? raw
                if path == "/dev/null" { sawOldDevNull = true; oldPath = nil }
                else { oldPath = DiffParser.stripPathPrefix(path) }
                return
            }
            if line.hasPrefix("+++ ") {
                let raw = String(line.dropFirst(4))
                let path = raw.split(separator: "\t", maxSplits: 1).first.map(String.init) ?? raw
                if path == "/dev/null" { sawNewDevNull = true; newPath = nil }
                else { newPath = DiffParser.stripPathPrefix(path) }
                return
            }
            // `old mode`, `new mode`, `index …`, `similarity index …` etc. are
            // ignored.
        }

        private func startHunk(_ header: String) {
            finishHunk()
            let (os, oc, ns, nc) = DiffParser.parseHunkHeader(header)
            hHeader = header
            hOldStart = os; hOldCount = oc; hNewStart = ns; hNewCount = nc
            oldCursor = os; newCursor = ns
            hLines = []
        }

        private func consumeHunkLine(_ line: String) {
            guard let first = line.first else {
                // A truly empty line (no leading space) is only the trailing
                // split artifact; ignore it.
                return
            }
            switch first {
            case "\\":
                // "\ No newline at end of file" — annotate the previous line.
                if let last = hLines.popLast() {
                    hLines.append(DiffLine(kind: last.kind, text: last.text,
                                           oldLineNumber: last.oldLineNumber,
                                           newLineNumber: last.newLineNumber,
                                           noNewlineAtEOF: true))
                }
            case "+":
                hLines.append(DiffLine(kind: .added, text: String(line.dropFirst()),
                                       oldLineNumber: nil, newLineNumber: newCursor))
                newCursor += 1
            case "-":
                hLines.append(DiffLine(kind: .removed, text: String(line.dropFirst()),
                                       oldLineNumber: oldCursor, newLineNumber: nil))
                oldCursor += 1
            case " ":
                hLines.append(DiffLine(kind: .context, text: String(line.dropFirst()),
                                       oldLineNumber: oldCursor, newLineNumber: newCursor))
                oldCursor += 1; newCursor += 1
            default:
                // Anything else terminates the hunk body (defensive; git does
                // not emit such lines mid-hunk).
                finishHunk()
            }
        }

        private func finishHunk() {
            guard let header = hHeader else { return }
            hunks.append(DiffHunk(header: header, oldStart: hOldStart, oldCount: hOldCount,
                                  newStart: hNewStart, newCount: hNewCount, lines: hLines))
            hHeader = nil
            hLines = []
        }

        func build() -> DiffFile {
            finishHunk()
            let isNew = explicitNew || sawOldDevNull || (oldPath == nil && newPath != nil)
            let isDeleted = explicitDeleted || sawNewDevNull || (newPath == nil && oldPath != nil)
            return DiffFile(oldPath: oldPath, newPath: newPath, isBinary: isBinary,
                            isNew: isNew, isDeleted: isDeleted,
                            isRename: isRename, hunks: hunks)
        }
    }

    // MARK: - Hunk header

    /// Parses `@@ -oldStart,oldCount +newStart,newCount @@ …`. A missing count
    /// defaults to 1 (unified-diff convention). Malformed headers yield zeros.
    static func parseHunkHeader(_ header: String) -> (Int, Int, Int, Int) {
        // Grab the "-a,b +c,d" span between the two "@@" fences.
        let parts = header.split(separator: " ")
        var oldStart = 0, oldCount = 1, newStart = 0, newCount = 1
        for part in parts {
            if part.hasPrefix("-") {
                (oldStart, oldCount) = splitRange(part.dropFirst())
            } else if part.hasPrefix("+") {
                (newStart, newCount) = splitRange(part.dropFirst())
            }
        }
        return (oldStart, oldCount, newStart, newCount)
    }

    private static func splitRange<S: StringProtocol>(_ s: S) -> (Int, Int) {
        let comps = s.split(separator: ",", maxSplits: 1)
        let start = comps.first.flatMap { Int($0) } ?? 0
        let count = comps.count > 1 ? (Int(comps[1]) ?? 1) : 1
        return (start, count)
    }
}

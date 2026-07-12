import Foundation

/// A node in a worktree file tree built from the flat, recursive path list the
/// `GET /api/tasks/{id}/files` endpoint returns. Directories carry `children`;
/// leaf files carry a `status` code (M/A/D/…) and no children.
public struct FileTreeNode: Sendable, Equatable, Identifiable {
    public let name: String
    /// Full worktree-relative path (also the stable `id`).
    public let path: String
    public let isDirectory: Bool
    /// Git status letter for leaf files; nil for directories.
    public let status: String?
    public let children: [FileTreeNode]

    public var id: String { path }

    /// `nil` for files so `OutlineGroup` renders them as leaves rather than
    /// empty-but-expandable rows.
    public var outlineChildren: [FileTreeNode]? { isDirectory ? children : nil }

    public init(name: String, path: String, isDirectory: Bool,
                status: String?, children: [FileTreeNode]) {
        self.name = name
        self.path = path
        self.isDirectory = isDirectory
        self.status = status
        self.children = children
    }
}

/// Builds a nested ``FileTreeNode`` tree from the endpoint's flat file list.
/// Pure and dependency-free for unit testing. Directories sort before files;
/// within a kind, entries sort case-insensitively by name.
public enum FileTreeBuilder {
    public static func build(_ files: [ChangedFile]) -> [FileTreeNode] {
        // Mutable scratch tree keyed by segment for O(path) insertion.
        final class Scratch {
            let name: String
            let path: String
            var isDir: Bool
            var status: String?
            var children: [String: Scratch] = [:]
            var order: [String] = []
            init(name: String, path: String, isDir: Bool) {
                self.name = name; self.path = path; self.isDir = isDir
            }
        }

        let root = Scratch(name: "", path: "", isDir: true)
        for f in files {
            let segs = f.path.split(separator: "/").map(String.init).filter { !$0.isEmpty }
            guard !segs.isEmpty else { continue }
            var cur = root
            var acc = ""
            for (i, seg) in segs.enumerated() {
                acc = acc.isEmpty ? seg : acc + "/" + seg
                let isLeaf = (i == segs.count - 1)
                if let existing = cur.children[seg] {
                    cur = existing
                } else {
                    let node = Scratch(name: seg, path: acc, isDir: !isLeaf)
                    cur.children[seg] = node
                    cur.order.append(seg)
                    cur = node
                }
                if isLeaf {
                    // A path component that also appears as a directory prefix of
                    // another entry stays a directory; only pure leaves get a
                    // status.
                    if cur.children.isEmpty {
                        cur.isDir = false
                        cur.status = f.status
                    }
                }
            }
        }

        func convert(_ n: Scratch) -> [FileTreeNode] {
            let nodes = n.order.compactMap { n.children[$0] }.map { child in
                FileTreeNode(name: child.name, path: child.path,
                             isDirectory: child.isDir,
                             status: child.isDir ? nil : child.status,
                             children: convert(child))
            }
            return nodes.sorted { a, b in
                if a.isDirectory != b.isDirectory { return a.isDirectory }
                return a.name.lowercased() < b.name.lowercased()
            }
        }
        return convert(root)
    }
}

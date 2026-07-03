import Foundation
import Testing
@testable import ArgusKit

@Suite("FileTreeBuilder")
struct FileTreeBuilderTests {

    private func cf(_ status: String, _ path: String) -> ChangedFile {
        // ChangedFile has no memberwise init exposed for isDir here; decode one.
        let json = #"{"Status":"\#(status)","Path":"\#(path)","IsDir":false}"#
        return try! JSONDecoder().decode(ChangedFile.self, from: Data(json.utf8))
    }

    @Test("Empty input yields no nodes")
    func empty() {
        #expect(FileTreeBuilder.build([]).isEmpty)
    }

    @Test("Flat files become leaf nodes with status and no children")
    func flat() {
        let tree = FileTreeBuilder.build([cf("M", "a.txt"), cf("A", "b.txt")])
        #expect(tree.count == 2)
        #expect(tree[0].name == "a.txt")
        #expect(tree[0].isDirectory == false)
        #expect(tree[0].status == "M")
        #expect(tree[0].outlineChildren == nil)
        #expect(tree[1].status == "A")
    }

    @Test("Nested paths build directory nodes; dirs sort before files")
    func nested() {
        let tree = FileTreeBuilder.build([
            cf("M", "src/app/main.swift"),
            cf("A", "src/app/util.swift"),
            cf("M", "README.md"),
            cf("D", "src/old.swift"),
        ])
        // Top level: "src" (dir) sorts before "README.md" (file).
        #expect(tree.count == 2)
        #expect(tree[0].name == "src")
        #expect(tree[0].isDirectory == true)
        #expect(tree[0].status == nil)
        #expect(tree[0].outlineChildren != nil)
        #expect(tree[1].name == "README.md")
        #expect(tree[1].isDirectory == false)

        // Inside src: "app" (dir) before "old.swift" (file).
        let src = tree[0]
        #expect(src.children.count == 2)
        #expect(src.children[0].name == "app")
        #expect(src.children[0].isDirectory == true)
        #expect(src.children[1].name == "old.swift")
        #expect(src.children[1].status == "D")

        // Inside app: two leaf files, alpha order.
        let app = src.children[0]
        #expect(app.children.map(\.name) == ["main.swift", "util.swift"])
        #expect(app.children[0].path == "src/app/main.swift")
    }

    @Test("Sibling files under the same directory share one directory node")
    func sharedDir() {
        let tree = FileTreeBuilder.build([
            cf("M", "pkg/a.go"),
            cf("M", "pkg/b.go"),
        ])
        #expect(tree.count == 1)
        #expect(tree[0].name == "pkg")
        #expect(tree[0].children.count == 2)
    }
}

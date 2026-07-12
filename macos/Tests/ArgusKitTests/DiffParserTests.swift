import Foundation
import Testing
@testable import ArgusKit

@Suite("DiffParser")
struct DiffParserTests {

    // MARK: - Empty / trivial

    @Test("Empty and whitespace-only input yields no files")
    func empty() {
        #expect(DiffParser.parse("").isEmpty)
        #expect(DiffParser.parse("   \n\n  ").isEmpty)
    }

    @Test("Preamble before the first `diff --git` is ignored")
    func preambleIgnored() {
        let text = """
        some log noise
        not a diff
        diff --git a/x.txt b/x.txt
        index 111..222 100644
        --- a/x.txt
        +++ b/x.txt
        @@ -1 +1 @@
        -old
        +new
        """
        let files = DiffParser.parse(text)
        #expect(files.count == 1)
        #expect(files[0].newPath == "x.txt")
    }

    // MARK: - Single modified file with line numbers

    @Test("Modified file parses hunk lines with old/new numbers")
    func modified() {
        let text = """
        diff --git a/src/a.go b/src/a.go
        index 1111111..2222222 100644
        --- a/src/a.go
        +++ b/src/a.go
        @@ -1,4 +1,5 @@ package main
         line1
        -line2
        +line2changed
        +lineNew
         line3
        """
        let files = DiffParser.parse(text)
        #expect(files.count == 1)
        let f = files[0]
        #expect(f.oldPath == "src/a.go")
        #expect(f.newPath == "src/a.go")
        #expect(f.isBinary == false)
        #expect(f.isNew == false)
        #expect(f.isDeleted == false)
        #expect(f.isRename == false)
        #expect(f.addedCount == 2)
        #expect(f.removedCount == 1)

        let h = f.hunks[0]
        #expect(h.oldStart == 1)
        #expect(h.oldCount == 4)
        #expect(h.newStart == 1)
        #expect(h.newCount == 5)
        #expect(h.header.contains("package main"))

        // Line numbering: context line1 = old1/new1; removed line2 = old2/nil;
        // added line2changed = nil/new2; added lineNew = nil/new3;
        // context line3 = old3/new4.
        let lines = h.lines
        #expect(lines.count == 5)
        #expect(lines[0].kind == .context)
        #expect(lines[0].oldLineNumber == 1)
        #expect(lines[0].newLineNumber == 1)
        #expect(lines[1].kind == .removed)
        #expect(lines[1].oldLineNumber == 2)
        #expect(lines[1].newLineNumber == nil)
        #expect(lines[1].text == "line2")
        #expect(lines[2].kind == .added)
        #expect(lines[2].oldLineNumber == nil)
        #expect(lines[2].newLineNumber == 2)
        #expect(lines[3].kind == .added)
        #expect(lines[3].newLineNumber == 3)
        #expect(lines[4].kind == .context)
        #expect(lines[4].oldLineNumber == 3)
        #expect(lines[4].newLineNumber == 4)
    }

    @Test("Single-line hunk header without counts defaults count to 1")
    func hunkNoCounts() {
        let text = """
        diff --git a/f b/f
        --- a/f
        +++ b/f
        @@ -3 +3 @@
        -a
        +b
        """
        let h = DiffParser.parse(text)[0].hunks[0]
        #expect(h.oldStart == 3)
        #expect(h.oldCount == 1)
        #expect(h.newStart == 3)
        #expect(h.newCount == 1)
    }

    // MARK: - Multi-file

    @Test("Multi-file diff splits on `diff --git` boundaries")
    func multiFile() {
        let text = """
        diff --git a/one.txt b/one.txt
        --- a/one.txt
        +++ b/one.txt
        @@ -1 +1 @@
        -a
        +b
        diff --git a/two.txt b/two.txt
        --- a/two.txt
        +++ b/two.txt
        @@ -1 +1,2 @@
         keep
        +added
        """
        let files = DiffParser.parse(text)
        #expect(files.count == 2)
        #expect(files[0].newPath == "one.txt")
        #expect(files[1].newPath == "two.txt")
        #expect(files[1].addedCount == 1)
        #expect(files[1].removedCount == 0)
    }

    // MARK: - New file via /dev/null

    @Test("New file: old side /dev/null marks isNew and nil oldPath")
    func newFileDevNull() {
        let text = """
        diff --git a/new.txt b/new.txt
        new file mode 100644
        index 0000000..abcdef0
        --- /dev/null
        +++ b/new.txt
        @@ -0,0 +1,2 @@
        +hello
        +world
        """
        let f = DiffParser.parse(text)[0]
        #expect(f.oldPath == nil)
        #expect(f.newPath == "new.txt")
        #expect(f.isNew == true)
        #expect(f.isDeleted == false)
        #expect(f.addedCount == 2)
        #expect(f.displayPath == "new.txt")
    }

    @Test("Deleted file: new side /dev/null marks isDeleted and nil newPath")
    func deletedFileDevNull() {
        let text = """
        diff --git a/gone.txt b/gone.txt
        deleted file mode 100644
        index abcdef0..0000000
        --- a/gone.txt
        +++ /dev/null
        @@ -1,2 +0,0 @@
        -bye
        -now
        """
        let f = DiffParser.parse(text)[0]
        #expect(f.oldPath == "gone.txt")
        #expect(f.newPath == nil)
        #expect(f.isDeleted == true)
        #expect(f.isNew == false)
        #expect(f.removedCount == 2)
        #expect(f.displayPath == "gone.txt")
    }

    // MARK: - Rename

    @Test("Rename with no content change: rename from/to set paths, displayPath shows arrow")
    func renamePure() {
        let text = """
        diff --git a/old/name.txt b/new/name.txt
        similarity index 100%
        rename from old/name.txt
        rename to new/name.txt
        """
        let f = DiffParser.parse(text)[0]
        #expect(f.isRename == true)
        #expect(f.oldPath == "old/name.txt")
        #expect(f.newPath == "new/name.txt")
        #expect(f.hunks.isEmpty)
        #expect(f.displayPath == "old/name.txt → new/name.txt")
    }

    @Test("Rename with edits: rename flags plus a hunk")
    func renameWithEdits() {
        let text = """
        diff --git a/a.txt b/b.txt
        similarity index 80%
        rename from a.txt
        rename to b.txt
        index 111..222 100644
        --- a/a.txt
        +++ b/b.txt
        @@ -1,2 +1,2 @@
         same
        -old
        +new
        """
        let f = DiffParser.parse(text)[0]
        #expect(f.isRename == true)
        #expect(f.oldPath == "a.txt")
        #expect(f.newPath == "b.txt")
        #expect(f.addedCount == 1)
        #expect(f.removedCount == 1)
    }

    // MARK: - Binary

    @Test("Binary file stanza marks isBinary with no hunks")
    func binary() {
        let text = """
        diff --git a/logo.png b/logo.png
        index 111..222 100644
        Binary files a/logo.png and b/logo.png differ
        """
        let f = DiffParser.parse(text)[0]
        #expect(f.isBinary == true)
        #expect(f.hunks.isEmpty)
        #expect(f.newPath == "logo.png")
    }

    @Test("GIT binary patch stanza marks isBinary")
    func binaryPatch() {
        let text = """
        diff --git a/blob.bin b/blob.bin
        new file mode 100644
        index 0000000..1111111
        GIT binary patch
        literal 4
        Mc$@junk
        """
        let f = DiffParser.parse(text)[0]
        #expect(f.isBinary == true)
        #expect(f.isNew == true)
    }

    // MARK: - No newline at EOF

    @Test("No-newline-at-eof marker annotates the preceding line")
    func noNewlineAtEOF() {
        let text = """
        diff --git a/f.txt b/f.txt
        --- a/f.txt
        +++ b/f.txt
        @@ -1 +1 @@
        -old
        \\ No newline at end of file
        +new
        \\ No newline at end of file
        """
        let f = DiffParser.parse(text)[0]
        let lines = f.hunks[0].lines
        #expect(lines.count == 2)
        #expect(lines[0].kind == .removed)
        #expect(lines[0].noNewlineAtEOF == true)
        #expect(lines[1].kind == .added)
        #expect(lines[1].noNewlineAtEOF == true)
    }

    // MARK: - Multiple hunks in one file

    @Test("Multiple hunks accumulate with independent line cursors")
    func multipleHunks() {
        let text = """
        diff --git a/big.txt b/big.txt
        --- a/big.txt
        +++ b/big.txt
        @@ -1,2 +1,2 @@
         a
        -b
        +B
        @@ -10,2 +10,3 @@
         j
        +K
         k
        """
        let f = DiffParser.parse(text)
        #expect(f[0].hunks.count == 2)
        #expect(f[0].hunks[1].oldStart == 10)
        #expect(f[0].hunks[1].newStart == 10)
        // second hunk: context j = old10/new10, added K = nil/new11, context k = old11/new12
        let l = f[0].hunks[1].lines
        #expect(l[0].oldLineNumber == 10)
        #expect(l[1].kind == .added)
        #expect(l[1].newLineNumber == 11)
        #expect(l[2].oldLineNumber == 11)
        #expect(l[2].newLineNumber == 12)
    }

    // MARK: - Path helpers

    @Test("stripPathPrefix maps /dev/null to nil and strips a//b/")
    func stripPath() {
        #expect(DiffParser.stripPathPrefix("/dev/null") == nil)
        #expect(DiffParser.stripPathPrefix("a/foo/bar.go") == "foo/bar.go")
        #expect(DiffParser.stripPathPrefix("b/foo.go") == "foo.go")
        #expect(DiffParser.stripPathPrefix("plain.go") == "plain.go")
    }
}

@Suite("GitChangeSummary")
struct GitChangeSummaryTests {

    @Test("Empty inputs yield no paths")
    func empty() {
        #expect(GitChangeSummary.changedPaths(status: "", branchFiles: "").isEmpty)
    }

    @Test("status --short normalizes X/Y flags to M/A/D/U")
    func statusFlags() {
        let status = """
         M modified.go
        A  added.go
         D deleted.go
        ?? untracked.go
        UU conflicted.go
        """
        let paths = GitChangeSummary.changedPaths(status: status, branchFiles: "")
        let byPath = Dictionary(uniqueKeysWithValues: paths.map { ($0.path, $0.status) })
        #expect(byPath["modified.go"] == "M")
        #expect(byPath["added.go"] == "A")
        #expect(byPath["deleted.go"] == "D")
        #expect(byPath["untracked.go"] == "A")
        #expect(byPath["conflicted.go"] == "U")
    }

    @Test("branchFiles name-status parses codes and rename picks new path")
    func branchFiles() {
        let branch = """
        A\tnew.go
        M\tchanged.go
        D\tremoved.go
        R100\told/x.go\tnew/x.go
        """
        let paths = GitChangeSummary.changedPaths(status: "", branchFiles: branch)
        let byPath = Dictionary(uniqueKeysWithValues: paths.map { ($0.path, $0.status) })
        #expect(byPath["new.go"] == "A")
        #expect(byPath["changed.go"] == "M")
        #expect(byPath["removed.go"] == "D")
        // rename → shown as M under the NEW path.
        #expect(byPath["new/x.go"] == "M")
        #expect(byPath["old/x.go"] == nil)
    }

    @Test("Merged sources de-duplicate; result sorted by path; status wins over branch")
    func merged() {
        let status = " M shared.go\n?? only-status.go"
        let branch = "M\tshared.go\nA\tonly-branch.go"
        let paths = GitChangeSummary.changedPaths(status: status, branchFiles: branch)
        #expect(paths.map(\.path) == ["only-branch.go", "only-status.go", "shared.go"])
        // shared.go seen first via status (no ?/A/D/U → M).
        #expect(paths.first { $0.path == "shared.go" }?.status == "M")
    }
}

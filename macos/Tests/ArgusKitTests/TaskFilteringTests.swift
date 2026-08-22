import Foundation
import Testing
@testable import ArgusKit

/// Pins `TaskFiltering.filteredFolders(_:filterText:showHeraManaged:heraManagedTaskIDs:)`
/// — the sidebar's "Task rail filter access" predicate (free-text substring
/// match on task name + hera-managed visibility), extracted as a pure
/// ArgusKit function precisely so it's testable without instantiating
/// `Sidebar`'s `@State`/`@Environment` (see `macos/Sources/Argus/Sidebar.swift`,
/// the sole caller, via `filteredFolders(_:)`).
@Suite("TaskFiltering.filteredFolders")
struct TaskFilteringTests {

    /// Builds a minimal `Task` via the memberwise initializer — only `id`,
    /// `name`, and `project` matter for these assertions.
    private func task(_ id: String, name: String? = nil, project: String = "argus") -> Task {
        Task(id: id, name: name ?? "task-\(id)", status: "pending", idle: false, needsInput: false,
             project: project, branch: nil, backend: nil, elapsed: nil,
             createdAt: "2026-08-21T00:00:00Z", archived: false, worktreePath: nil,
             prompt: nil, prState: nil)
    }

    // MARK: - Empty filter text is a no-op

    @Test("Empty filter text passes every task through unchanged")
    func emptyFilterTextIsNoOp() {
        let folders = [TaskFolder(project: "argus", tasks: [task("1", name: "alpha"), task("2", name: "beta")])]
        let result = TaskFiltering.filteredFolders(folders, filterText: "",
                                                     showHeraManaged: true, heraManagedTaskIDs: [])
        #expect(result.map(\.id) == ["argus"])
        #expect(result[0].tasks.map(\.id) == ["1", "2"])
    }

    @Test("Whitespace-only filter text is treated as empty (a no-op)")
    func whitespaceOnlyFilterTextIsNoOp() {
        let folders = [TaskFolder(project: "argus", tasks: [task("1", name: "alpha")])]
        let result = TaskFiltering.filteredFolders(folders, filterText: "   ",
                                                     showHeraManaged: true, heraManagedTaskIDs: [])
        #expect(result[0].tasks.map(\.id) == ["1"])
    }

    // MARK: - Substring match (case-insensitive)

    @Test("A matching substring (any case) keeps the task")
    func matchingSubstringKeepsTask() {
        let folders = [TaskFolder(project: "argus", tasks: [task("1", name: "Fix Login Bug")])]
        let result = TaskFiltering.filteredFolders(folders, filterText: "login",
                                                     showHeraManaged: true, heraManagedTaskIDs: [])
        #expect(result.map(\.id) == ["argus"])
        #expect(result[0].tasks.map(\.id) == ["1"])
    }

    @Test("A matching substring only filters out non-matching siblings, not the whole folder")
    func matchingSubstringFiltersSiblingsWithinFolder() {
        let folders = [TaskFolder(project: "argus", tasks: [
            task("1", name: "Fix Login Bug"),
            task("2", name: "Add Dark Mode"),
        ])]
        let result = TaskFiltering.filteredFolders(folders, filterText: "LOGIN",
                                                     showHeraManaged: true, heraManagedTaskIDs: [])
        #expect(result[0].tasks.map(\.id) == ["1"])
    }

    // MARK: - Non-matching substring drops the folder entirely

    @Test("A non-matching substring drops every task, and the now-empty folder itself")
    func nonMatchingSubstringDropsFolder() {
        let folders = [TaskFolder(project: "argus", tasks: [task("1", name: "Fix Login Bug")])]
        let result = TaskFiltering.filteredFolders(folders, filterText: "nonexistent",
                                                     showHeraManaged: true, heraManagedTaskIDs: [])
        #expect(result.isEmpty)
    }

    @Test("A non-matching substring drops only the folder left empty, not an unrelated matching one")
    func nonMatchingSubstringDropsOnlyEmptyFolder() {
        let folders = [
            TaskFolder(project: "argus", tasks: [task("1", name: "Fix Login Bug")]),
            TaskFolder(project: "other", tasks: [task("2", name: "Unrelated")]),
        ]
        let result = TaskFiltering.filteredFolders(folders, filterText: "login",
                                                     showHeraManaged: true, heraManagedTaskIDs: [])
        #expect(result.map(\.id) == ["argus"])
    }

    // MARK: - Hera-managed visibility toggle

    @Test("showHeraManaged=false hides a hera-managed task")
    func hidesHeraManagedTaskWhenToggledOff() {
        let folders = [TaskFolder(project: "argus", tasks: [
            task("1", name: "worker-task"),
            task("2", name: "plain-task"),
        ])]
        let result = TaskFiltering.filteredFolders(folders, filterText: "",
                                                     showHeraManaged: false, heraManagedTaskIDs: ["1"])
        #expect(result[0].tasks.map(\.id) == ["2"])
    }

    @Test("showHeraManaged=true (the default) shows a hera-managed task")
    func showsHeraManagedTaskWhenToggledOn() {
        let folders = [TaskFolder(project: "argus", tasks: [
            task("1", name: "worker-task"),
            task("2", name: "plain-task"),
        ])]
        let result = TaskFiltering.filteredFolders(folders, filterText: "",
                                                     showHeraManaged: true, heraManagedTaskIDs: ["1"])
        #expect(result[0].tasks.map(\.id) == ["1", "2"])
    }

    @Test("showHeraManaged=false dropping every task in a folder drops the folder itself")
    func hidingHeraManagedCanDropAWholeFolder() {
        let folders = [TaskFolder(project: "argus", tasks: [task("1", name: "worker-task")])]
        let result = TaskFiltering.filteredFolders(folders, filterText: "",
                                                     showHeraManaged: false, heraManagedTaskIDs: ["1"])
        #expect(result.isEmpty)
    }

    @Test("The hera-managed filter and the text filter combine (AND, not OR)")
    func heraManagedAndTextFilterCombine() {
        let folders = [TaskFolder(project: "argus", tasks: [
            task("1", name: "worker-login-task"),
            task("2", name: "worker-other-task"),
            task("3", name: "plain-login-task"),
        ])]
        let result = TaskFiltering.filteredFolders(folders, filterText: "login",
                                                     showHeraManaged: false,
                                                     heraManagedTaskIDs: ["1", "2"])
        // "1" matches text but is hera-managed (excluded); "2" is hera-managed
        // and doesn't match text either; only "3" satisfies both.
        #expect(result[0].tasks.map(\.id) == ["3"])
    }
}

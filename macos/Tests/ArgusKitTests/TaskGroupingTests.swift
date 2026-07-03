import Foundation
import Testing
@testable import ArgusKit

@Suite("TaskGrouping")
struct TaskGroupingTests {

    /// Builds a minimal `Task` via the memberwise initializer (the wire
    /// `init(from:)` is the only other one) — only `project` and `id` matter
    /// for grouping/order assertions.
    private func task(_ id: String, project: String) -> Task {
        Task(id: id, name: "task-\(id)", status: "pending", idle: false, needsInput: false,
             project: project, branch: nil, backend: nil, elapsed: nil,
             createdAt: "2026-07-02T00:00:00Z", archived: false, worktreePath: nil,
             prompt: nil, prState: nil)
    }

    @Test("Empty input yields no folders")
    func empty() {
        #expect(TaskGrouping.byProject([]).isEmpty)
    }

    @Test("Projects sort alphabetically regardless of input order")
    func sortsProjects() {
        let folders = TaskGrouping.byProject([
            task("1", project: "zeta"),
            task("2", project: "alpha"),
            task("3", project: "mid"),
        ])
        #expect(folders.map(\.project) == ["alpha", "mid", "zeta"])
    }

    @Test("Tasks within a folder preserve input (created_at ASC) order")
    func preservesOrderWithinFolder() {
        let folders = TaskGrouping.byProject([
            task("first", project: "argus"),
            task("second", project: "argus"),
            task("third", project: "argus"),
        ])
        #expect(folders.count == 1)
        #expect(folders[0].tasks.map(\.id) == ["first", "second", "third"])
    }

    @Test("Empty project field groups under the no-project label")
    func noProjectLabel() {
        let folders = TaskGrouping.byProject([task("1", project: "")])
        #expect(folders.map(\.project) == [TaskGrouping.noProjectLabel])
        #expect(folders[0].tasks.map(\.id) == ["1"])
    }

    @Test("Interleaved tasks across projects regroup by project, not input order")
    func interleavedTasksRegroup() {
        let folders = TaskGrouping.byProject([
            task("a1", project: "alpha"),
            task("b1", project: "beta"),
            task("a2", project: "alpha"),
        ])
        #expect(folders.map(\.project) == ["alpha", "beta"])
        #expect(folders[0].tasks.map(\.id) == ["a1", "a2"])
        #expect(folders[1].tasks.map(\.id) == ["b1"])
    }
}

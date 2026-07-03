import Foundation

/// One project folder's worth of tasks, in display order.
public struct TaskFolder: Sendable, Equatable, Identifiable {
    /// The project name, or ``TaskGrouping/noProjectLabel`` when the task's
    /// `project` field is empty.
    public let project: String
    public let tasks: [Task]

    public var id: String { project }

    public init(project: String, tasks: [Task]) {
        self.project = project
        self.tasks = tasks
    }
}

/// Pure port of the TUI's `groupByProject` (`internal/tui/taskview/tasklist.go`):
/// groups tasks by project, sorts the project names alphabetically, and
/// preserves each task's incoming relative order within its project (the TUI
/// never re-sorts within a group — the DB already yields `created_at ASC`,
/// and `GET /api/tasks` preserves that order verbatim).
public enum TaskGrouping {
    /// Label used in place of an empty `project` field, matching the TUI's
    /// `"(no project)"` placeholder.
    public static let noProjectLabel = "(no project)"

    /// Groups `tasks` into folders ordered alphabetically by project name.
    public static func byProject(_ tasks: [Task]) -> [TaskFolder] {
        var order: [String] = []
        var groups: [String: [Task]] = [:]
        for t in tasks {
            let project = t.project.isEmpty ? noProjectLabel : t.project
            if groups[project] == nil {
                groups[project] = []
                order.append(project)
            }
            groups[project]!.append(t)
        }
        return order.sorted().map { TaskFolder(project: $0, tasks: groups[$0] ?? []) }
    }
}

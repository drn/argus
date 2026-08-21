import Foundation

/// Pure task-rail filtering for the sidebar's filter bar (`add-mac-keybinding-parity`
/// "Task rail filter access"): applies the free-text substring filter
/// (case-insensitive, over `Task.name`) and the hera-managed visibility
/// toggle to a list of project folders, dropping any folder left with no
/// tasks.
///
/// Extracted as a free function over plain data — no SwiftUI `@State`/
/// `@Environment` — so it's unit-testable without instantiating view state,
/// mirroring Stage 1's other pure-logic seams (`NeedsInputNavigation`,
/// `TaskStatus.advanced()/reverted()`). The sole caller is
/// `Sidebar.filteredFolders` (Argus target), which supplies
/// `AppState.heraManagedTaskIDs` as `heraManagedTaskIDs`.
public enum TaskFiltering {
    /// - Parameters:
    ///   - folders: Project folders to filter (e.g. `AppState.tasksByFolder`).
    ///   - filterText: Free-text filter; leading/trailing whitespace is
    ///     trimmed, and an empty (post-trim) string is a no-op filter.
    ///   - showHeraManaged: When `false`, tasks whose id is in
    ///     `heraManagedTaskIDs` are excluded.
    ///   - heraManagedTaskIDs: The task ids currently bound to a live Hera
    ///     role (``AppState/heraManagedTaskIDs``).
    /// - Returns: `folders` with each folder's `tasks` filtered down; a
    ///   folder left with zero matching tasks is dropped entirely.
    public static func filteredFolders(
        _ folders: [TaskFolder],
        filterText: String,
        showHeraManaged: Bool,
        heraManagedTaskIDs: Set<String>
    ) -> [TaskFolder] {
        let needle = filterText.trimmingCharacters(in: .whitespacesAndNewlines)
        return folders.compactMap { folder in
            let tasks = folder.tasks.filter { task in
                (showHeraManaged || !heraManagedTaskIDs.contains(task.id))
                    && (needle.isEmpty || task.name.localizedCaseInsensitiveContains(needle))
            }
            return tasks.isEmpty ? nil : TaskFolder(project: folder.project, tasks: tasks)
        }
    }
}

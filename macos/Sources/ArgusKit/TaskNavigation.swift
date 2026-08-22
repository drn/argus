import Foundation

/// Pure adjacent-task stepping for the Terminal tab's Cmd+Up/Cmd+Down
/// shortcut (spec.md "Switch tasks via Cmd+Up/Down without PTY leak") —
/// moves the sidebar selection one step through `orderedIDs` (the same
/// project-folder-then-creation-order the sidebar renders,
/// `AppState.tasksByFolder` flattened, mirroring how
/// `AppState.jumpToNextNeedsInput()` sources its own ordered id list).
///
/// Unrelated to ``NeedsInputNavigation``: this is a plain adjacent move
/// over EVERY task, not one filtered to tasks needing input, and it
/// CLAMPS at the ends rather than wrapping — the opposite choice from
/// ``NeedsInputNavigation``'s wraparound, made deliberately: a flat
/// top-to-bottom rail walk reads as a bounded scan of the visible list
/// (like a table view's arrow-key navigation), not a cycle you'd want to
/// wrap past the ends of.
public enum TaskNavigation {
    public enum Direction: Sendable {
        case previous, next
    }

    /// Returns the id to select next. `current` absent from `orderedIDs`
    /// (including `nil`) starts the walk from the top of the list, so
    /// BOTH directions land on the first task — there is nothing "before"
    /// an unselected rail to move away from. An empty `orderedIDs` always
    /// yields `nil`. Already at the first task and asked for `.previous`
    /// (or at the last task and asked for `.next`) returns `current`
    /// unchanged (a no-op the caller can assign idempotently).
    public static func adjacent(orderedIDs: [String], current: String?, direction: Direction) -> String? {
        guard !orderedIDs.isEmpty else { return nil }
        guard let current, let idx = orderedIDs.firstIndex(of: current) else {
            return orderedIDs.first
        }
        switch direction {
        case .previous:
            return idx > 0 ? orderedIDs[idx - 1] : orderedIDs[idx]
        case .next:
            return idx < orderedIDs.count - 1 ? orderedIDs[idx + 1] : orderedIDs[idx]
        }
    }
}

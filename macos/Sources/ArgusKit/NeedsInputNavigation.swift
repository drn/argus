import Foundation

/// Pure cycling logic for the mac app's "jump to next task needing input"
/// shortcut (spec.md "Jump to next needs-input task via shortcut"), mirroring
/// the TUI's `Rail.NextNeedsInputTaskID` (`internal/tui/hera/rail.go`).
///
/// Behavior (pinned by `NeedsInputNavigationTests`):
///  - An empty `needingInput` set always yields `nil`.
///  - When `current` is `nil`, or is not itself a member of `needingInput`
///    (including a stale id absent from `orderedIDs` entirely), the search
///    starts from the top of `orderedIDs` and returns the first match.
///  - When `current` IS itself a member of `needingInput` (and present in
///    `orderedIDs`), the search scans strictly after its position, wrapping
///    all the way around — so a lone remaining match re-selects itself.
///  - Ids in `needingInput` that are absent from `orderedIDs` are ignored;
///    they can never be selected and never block a real match.
public enum NeedsInputNavigation {
    public static func next(orderedIDs: [String], needingInput: Set<String>, current: String?) -> String? {
        guard !needingInput.isEmpty else { return nil }

        // Only ids that are both ordered and needing input are eligible,
        // preserving orderedIDs' order.
        let candidates = orderedIDs.filter { needingInput.contains($0) }
        guard !candidates.isEmpty else { return nil }

        if let current, let idx = candidates.firstIndex(of: current) {
            let nextIndex = (idx + 1) % candidates.count
            return candidates[nextIndex]
        }

        return candidates.first
    }
}

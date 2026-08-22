import Foundation

/// Generic wrap-around stepping through a fixed, ordered sequence of
/// values — backs the Terminal tab's Cmd+Left/Cmd+Right "cycle detail tab"
/// shortcut (`AppState.cycleDetailTab(forward:)`, stepping through
/// `AppState.DetailTab`'s fixed Terminal→Diff→Files→Info order) so the
/// index math has a real test instead of living untested inside
/// `AppState`. Generic over any `Equatable` rather than hardcoding
/// `DetailTab` — that type lives in the App target, not ArgusKit, and
/// nothing about this stepping logic is specific to it.
public enum CyclicSelection {
    /// Steps one position forward or backward through `order`, wrapping at
    /// both ends. `current` absent from `order` returns `order.first`
    /// (falling back to `current` itself if `order` is empty — a stable
    /// no-op rather than a crash). A single-element `order` is likewise a
    /// stable no-op in either direction.
    public static func step<T: Equatable>(_ order: [T], current: T, forward: Bool) -> T {
        guard !order.isEmpty else { return current }
        guard let idx = order.firstIndex(of: current) else { return order[0] }
        let count = order.count
        let newIdx = forward ? (idx + 1) % count : (idx - 1 + count) % count
        return order[newIdx]
    }
}

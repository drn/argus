import Foundation
import Testing
@testable import ArgusKit

/// Stage 1 (TDD red phase) of `add-mac-keybinding-parity`: pins the exact
/// cycling semantics `NeedsInputNavigation.next(orderedIDs:needingInput:current:)`
/// must implement — the mac app's "jump to next task needing input" shortcut
/// (spec.md "Jump to next needs-input task via shortcut", mirroring the TUI's
/// `Rail.NextNeedsInputTaskID`, `internal/tui/hera/rail.go`). The type does
/// not exist yet; this file is expected to fail to compile until a later
/// stage adds `macos/Sources/ArgusKit/NeedsInputNavigation.swift`.
///
/// Documented, tested behavior for the ambiguous case design.md left open
/// ("starting from the top if the current selection isn't itself in the
/// needing-input set" vs. scanning strictly after `current`): this suite
/// pins **start-from-the-top** whenever `current` is `nil` OR is not itself
/// a member of `needingInput` (including a stale id absent from
/// `orderedIDs` entirely) — mirroring the common case where the task you're
/// currently viewing does not itself need input. Only when `current` DOES
/// need input does the search instead scan strictly after its position,
/// wrapping all the way around the ordered list (back through `current`'s
/// own position last) — so a lone remaining match keeps re-selecting
/// itself, matching the TUI's documented wrap behavior.
@Suite("NeedsInputNavigation.next")
struct NeedsInputNavigationTests {

    // MARK: - Empty set

    @Test("an empty needing-input set always yields nil")
    func emptySetYieldsNil() {
        let next = NeedsInputNavigation.next(orderedIDs: ["a", "b"], needingInput: [], current: "a")
        #expect(next == nil)
    }

    @Test("an empty needing-input set yields nil even with no current selection")
    func emptySetYieldsNilNoCurrent() {
        let next = NeedsInputNavigation.next(orderedIDs: ["a", "b"], needingInput: [], current: nil)
        #expect(next == nil)
    }

    // MARK: - Single match

    @Test("a single match with no current selection is returned")
    func singleMatchNoCurrent() {
        let next = NeedsInputNavigation.next(orderedIDs: ["a", "b", "c"],
                                             needingInput: ["b"], current: nil)
        #expect(next == "b")
    }

    @Test("a lone match re-selects itself once the ring wraps all the way around")
    func loneMatchReselectsSelf() {
        let next = NeedsInputNavigation.next(orderedIDs: ["a", "b", "c"],
                                             needingInput: ["b"], current: "b")
        #expect(next == "b")
    }

    // MARK: - current not itself needing input -> start from the top

    @Test("current selection not itself needing input starts the search from the top")
    func currentNotNeedingInputStartsFromTop() {
        // "c" doesn't need input; naive "scan strictly after current" would
        // land on "d" — the documented behavior instead restarts from the
        // top of the list and returns the first candidate, "b".
        let next = NeedsInputNavigation.next(orderedIDs: ["a", "b", "c", "d"],
                                             needingInput: ["b", "d"], current: "c")
        #expect(next == "b")
    }

    @Test("nil current selection starts the search from the top")
    func nilCurrentStartsFromTop() {
        let next = NeedsInputNavigation.next(orderedIDs: ["a", "b", "c", "d"],
                                             needingInput: ["b", "d"], current: nil)
        #expect(next == "b")
    }

    @Test("a stale current id absent from orderedIDs starts the search from the top")
    func staleCurrentStartsFromTop() {
        let next = NeedsInputNavigation.next(orderedIDs: ["a", "b", "c"],
                                             needingInput: ["b"], current: "deleted-task-id")
        #expect(next == "b")
    }

    // MARK: - current itself needing input -> advance to the NEXT one, not itself

    @Test("current selection itself needing input advances to the next match, not itself")
    func currentNeedingInputAdvancesToNext() {
        let next = NeedsInputNavigation.next(orderedIDs: ["a", "b", "c", "d"],
                                             needingInput: ["b", "d"], current: "b")
        #expect(next == "d")
    }

    @Test("cycling past the end of the list wraps back to the start")
    func cyclingPastEndWrapsToStart() {
        let next = NeedsInputNavigation.next(orderedIDs: ["a", "b", "c", "d"],
                                             needingInput: ["b", "d"], current: "d")
        #expect(next == "b")
    }

    // MARK: - Needing-input ids outside the ordered list are ignored

    @Test("a needing-input id absent from orderedIDs can never be selected")
    func idOutsideOrderedListIgnored() {
        let next = NeedsInputNavigation.next(orderedIDs: ["a", "b", "c"],
                                             needingInput: ["not-in-list"], current: nil)
        #expect(next == nil)
    }

    @Test("a needing-input id absent from orderedIDs is ignored without disrupting a real match")
    func idOutsideOrderedListDoesNotBlockRealMatch() {
        let next = NeedsInputNavigation.next(orderedIDs: ["a", "b", "c"],
                                             needingInput: ["b", "not-in-list"], current: nil)
        #expect(next == "b")
    }

    // MARK: - Empty ordered list

    @Test("an empty ordered list yields nil regardless of the needing-input set")
    func emptyOrderedListYieldsNil() {
        let next = NeedsInputNavigation.next(orderedIDs: [], needingInput: ["a"], current: nil)
        #expect(next == nil)
    }
}

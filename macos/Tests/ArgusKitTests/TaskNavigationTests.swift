import Testing
@testable import ArgusKit

@Suite("TaskNavigation")
struct TaskNavigationTests {
    static let ids = ["a", "b", "c"]

    @Test("an empty orderedIDs always yields nil, regardless of direction")
    func emptyListYieldsNil() {
        #expect(TaskNavigation.adjacent(orderedIDs: [], current: nil, direction: .next) == nil)
        #expect(TaskNavigation.adjacent(orderedIDs: [], current: "a", direction: .previous) == nil)
    }

    @Test("a nil current starts from the top for .next")
    func nilCurrentNext() {
        #expect(TaskNavigation.adjacent(orderedIDs: Self.ids, current: nil, direction: .next) == "a")
    }

    @Test("a nil current starts from the top for .previous too")
    func nilCurrentPrevious() {
        #expect(TaskNavigation.adjacent(orderedIDs: Self.ids, current: nil, direction: .previous) == "a")
    }

    @Test("a current id absent from orderedIDs starts from the top")
    func staleCurrentStartsFromTop() {
        #expect(TaskNavigation.adjacent(orderedIDs: Self.ids, current: "zzz", direction: .next) == "a")
        #expect(TaskNavigation.adjacent(orderedIDs: Self.ids, current: "zzz", direction: .previous) == "a")
    }

    @Test("next moves one step toward the bottom")
    func nextStepsForward() {
        #expect(TaskNavigation.adjacent(orderedIDs: Self.ids, current: "a", direction: .next) == "b")
        #expect(TaskNavigation.adjacent(orderedIDs: Self.ids, current: "b", direction: .next) == "c")
    }

    @Test("previous moves one step toward the top")
    func previousStepsBackward() {
        #expect(TaskNavigation.adjacent(orderedIDs: Self.ids, current: "c", direction: .previous) == "b")
        #expect(TaskNavigation.adjacent(orderedIDs: Self.ids, current: "b", direction: .previous) == "a")
    }

    @Test("previous at the first task clamps — it does not wrap to the last")
    func previousClampsAtStart() {
        #expect(TaskNavigation.adjacent(orderedIDs: Self.ids, current: "a", direction: .previous) == "a")
    }

    @Test("next at the last task clamps — it does not wrap to the first")
    func nextClampsAtEnd() {
        #expect(TaskNavigation.adjacent(orderedIDs: Self.ids, current: "c", direction: .next) == "c")
    }

    @Test("a single-task list clamps in both directions")
    func singleTaskClampsBothWays() {
        #expect(TaskNavigation.adjacent(orderedIDs: ["only"], current: "only", direction: .next) == "only")
        #expect(TaskNavigation.adjacent(orderedIDs: ["only"], current: "only", direction: .previous) == "only")
    }
}

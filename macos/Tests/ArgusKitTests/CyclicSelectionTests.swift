import Testing
@testable import ArgusKit

@Suite("CyclicSelection")
struct CyclicSelectionTests {
    // Stands in for `AppState.DetailTab`'s fixed Terminal→Diff→Files→Info
    // order (a String here, since `DetailTab` lives in the App target and
    // this logic is deliberately generic over any `Equatable`).
    static let tabs = ["terminal", "diff", "files", "info"]

    @Test("forward steps to the next element")
    func forwardSteps() {
        #expect(CyclicSelection.step(Self.tabs, current: "terminal", forward: true) == "diff")
        #expect(CyclicSelection.step(Self.tabs, current: "diff", forward: true) == "files")
        #expect(CyclicSelection.step(Self.tabs, current: "files", forward: true) == "info")
    }

    @Test("forward from the last element wraps to the first")
    func forwardWrapsAtEnd() {
        #expect(CyclicSelection.step(Self.tabs, current: "info", forward: true) == "terminal")
    }

    @Test("backward steps to the previous element")
    func backwardSteps() {
        #expect(CyclicSelection.step(Self.tabs, current: "info", forward: false) == "files")
        #expect(CyclicSelection.step(Self.tabs, current: "diff", forward: false) == "terminal")
    }

    @Test("backward from the first element wraps to the last")
    func backwardWrapsAtStart() {
        #expect(CyclicSelection.step(Self.tabs, current: "terminal", forward: false) == "info")
    }

    @Test("a current value absent from order returns the first element")
    func absentCurrentReturnsFirst() {
        #expect(CyclicSelection.step(Self.tabs, current: "nonexistent", forward: true) == "terminal")
        #expect(CyclicSelection.step(Self.tabs, current: "nonexistent", forward: false) == "terminal")
    }

    @Test("an empty order is a stable no-op, returning current unchanged")
    func emptyOrderIsNoOp() {
        #expect(CyclicSelection.step([String](), current: "anything", forward: true) == "anything")
    }

    @Test("a single-element order is a stable no-op in both directions")
    func singleElementIsNoOp() {
        #expect(CyclicSelection.step(["only"], current: "only", forward: true) == "only")
        #expect(CyclicSelection.step(["only"], current: "only", forward: false) == "only")
    }
}

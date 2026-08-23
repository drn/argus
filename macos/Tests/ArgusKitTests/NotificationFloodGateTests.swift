import Foundation
import Testing
@testable import ArgusKit

@Suite("NotificationFloodGate")
struct NotificationFloodGateTests {

    private let base = Date(timeIntervalSince1970: 1_700_000_000)

    @Test("Sparse arrivals (at or under the threshold) each post individually")
    func sparseArrivalsPostIndividually() {
        var gate = NotificationFloodGate()
        for i in 0..<NotificationFloodGate.burstThreshold {
            let decision = gate.decide(taskID: "task-\(i)", taskName: "Task \(i)", now: base)
            #expect(decision == .postIndividual)
        }
    }

    @Test("Arrivals beyond the threshold within the window coalesce")
    func burstBeyondThresholdCoalesces() {
        var gate = NotificationFloodGate()
        for i in 0..<NotificationFloodGate.burstThreshold {
            #expect(gate.decide(taskID: "task-\(i)", taskName: "Task \(i)", now: base) == .postIndividual)
        }
        let overflowID = "task-\(NotificationFloodGate.burstThreshold)"
        let decision = gate.decide(taskID: overflowID, taskName: "Overflow Task", now: base)
        guard case .coalesce(let summary) = decision else {
            Issue.record("expected .coalesce, got \(decision)")
            return
        }
        #expect(summary.taskNames == ["Overflow Task"])
    }

    @Test("Coalesced summary accumulates every subsequent burst arrival")
    func coalescedSummaryAccumulates() {
        var gate = NotificationFloodGate()
        for i in 0..<NotificationFloodGate.burstThreshold {
            _ = gate.decide(taskID: "task-\(i)", taskName: "Task \(i)", now: base)
        }
        _ = gate.decide(taskID: "extra-1", taskName: "Extra 1", now: base)
        let decision = gate.decide(taskID: "extra-2", taskName: "Extra 2", now: base)
        guard case .coalesce(let summary) = decision else {
            Issue.record("expected .coalesce, got \(decision)")
            return
        }
        #expect(summary.taskNames == ["Extra 1", "Extra 2"])
    }

    @Test("The same task re-arriving mid-burst is not duplicated in the summary")
    func sameTaskNotDuplicatedInSummary() {
        var gate = NotificationFloodGate()
        for i in 0..<NotificationFloodGate.burstThreshold {
            _ = gate.decide(taskID: "task-\(i)", taskName: "Task \(i)", now: base)
        }
        _ = gate.decide(taskID: "extra-1", taskName: "Extra 1", now: base)
        let decision = gate.decide(taskID: "extra-1", taskName: "Extra 1", now: base)
        guard case .coalesce(let summary) = decision else {
            Issue.record("expected .coalesce, got \(decision)")
            return
        }
        #expect(summary.taskNames == ["Extra 1"])
    }

    @Test("The window is self-terminating: a quiet gap resets to individual posting")
    func windowResetsAfterQuietGap() {
        var gate = NotificationFloodGate()
        for i in 0..<(NotificationFloodGate.burstThreshold + 2) {
            _ = gate.decide(taskID: "task-\(i)", taskName: "Task \(i)", now: base)
        }
        let later = base.addingTimeInterval(NotificationFloodGate.window + 1)
        let decision = gate.decide(taskID: "later-task", taskName: "Later Task", now: later)
        #expect(decision == .postIndividual)
    }

    @Test("A fresh burst after a reset starts its own summary, not a stale one")
    func freshBurstAfterResetStartsCleanSummary() {
        var gate = NotificationFloodGate()
        for i in 0..<(NotificationFloodGate.burstThreshold + 2) {
            _ = gate.decide(taskID: "old-\(i)", taskName: "Old \(i)", now: base)
        }
        let later = base.addingTimeInterval(NotificationFloodGate.window + 1)
        for i in 0..<NotificationFloodGate.burstThreshold {
            _ = gate.decide(taskID: "new-\(i)", taskName: "New \(i)", now: later)
        }
        let decision = gate.decide(taskID: "new-overflow", taskName: "New Overflow", now: later)
        guard case .coalesce(let summary) = decision else {
            Issue.record("expected .coalesce, got \(decision)")
            return
        }
        #expect(summary.taskNames == ["New Overflow"])
    }

    @Test("Summary body truncates a long task-name list with a remainder count")
    func summaryBodyTruncatesLongList() {
        let summary = NotificationBurstSummary(taskNames: ["a", "b", "c", "d", "e"])
        #expect(summary.title == "5 tasks need input")
        #expect(summary.body == "a, b, c, and 2 more")
    }

    @Test("Summary body lists names in full when under the truncation cap")
    func summaryBodyListsShortNamesInFull() {
        let summary = NotificationBurstSummary(taskNames: ["a", "b"])
        #expect(summary.body == "a, b")
    }
}

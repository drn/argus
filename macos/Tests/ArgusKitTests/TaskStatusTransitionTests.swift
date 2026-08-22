import Foundation
import Testing
@testable import ArgusKit

/// Stage 1 (TDD red phase) of `add-mac-keybinding-parity`: pins the exact
/// transition semantics `TaskStatus.advanced()` / `TaskStatus.reverted()`
/// must implement — the mac app's context-menu status-advance/revert action
/// (the `s`/`S` TUI keys' mac equivalent, design.md D4). Neither method
/// exists yet; this file is expected to fail to compile until a later stage
/// adds them to `ArgusKit.TaskStatus` (see `Models+Task.swift`, where
/// `TaskStatus` is actually defined — NOT `Models+Status.swift`).
///
/// Mirrors `internal/model/status.go`'s linear order exactly:
/// `pending < inProgress < inReview < complete`, with `Next()`/`Prev()`
/// clamping at the ends (returning the same status rather than wrapping).
/// `.other(_)` — ArgusKit's catch-all for an unrecognized/future status
/// string — must be a safe no-op in both directions: there is no defined
/// "next"/"previous" for a status this client doesn't understand, and
/// crashing or guessing would be worse than doing nothing.
@Suite("TaskStatus advance/revert transitions")
struct TaskStatusTransitionTests {

    // MARK: - advanced() — every forward transition, plus the clamp

    @Test("pending advances to inProgress")
    func advancePendingToInProgress() {
        #expect(TaskStatus.pending.advanced() == .inProgress)
    }

    @Test("inProgress advances to inReview")
    func advanceInProgressToInReview() {
        #expect(TaskStatus.inProgress.advanced() == .inReview)
    }

    @Test("inReview advances to complete")
    func advanceInReviewToComplete() {
        #expect(TaskStatus.inReview.advanced() == .complete)
    }

    @Test("complete clamps at complete — advancing the terminal status is a no-op")
    func advanceCompleteClamps() {
        #expect(TaskStatus.complete.advanced() == .complete)
    }

    // MARK: - reverted() — every backward transition, plus the clamp

    @Test("pending clamps at pending — reverting the initial status is a no-op")
    func revertPendingClamps() {
        #expect(TaskStatus.pending.reverted() == .pending)
    }

    @Test("inProgress reverts to pending")
    func revertInProgressToPending() {
        #expect(TaskStatus.inProgress.reverted() == .pending)
    }

    @Test("inReview reverts to inProgress")
    func revertInReviewToInProgress() {
        #expect(TaskStatus.inReview.reverted() == .inProgress)
    }

    @Test("complete reverts to inReview")
    func revertCompleteToInReview() {
        #expect(TaskStatus.complete.reverted() == .inReview)
    }

    // MARK: - .other(_) — unknown status is a safe no-op in both directions

    @Test("advancing an unrecognized status is a no-op, not a crash or a guess")
    func advanceOtherIsNoOp() {
        let unknown = TaskStatus.other("some-future-status")
        #expect(unknown.advanced() == unknown)
    }

    @Test("reverting an unrecognized status is a no-op, not a crash or a guess")
    func revertOtherIsNoOp() {
        let unknown = TaskStatus.other("some-future-status")
        #expect(unknown.reverted() == unknown)
    }
}

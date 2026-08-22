import AppKit
import ArgusKit
import UserNotifications
import os

/// Owns the app's native notifications (UNUserNotificationCenter): lazy
/// authorization, needs-input / idle banners, tap-to-select, needs-input
/// dedupe, and burst-summary posting. ``AppState`` drives it from the event
/// stream.
///
/// Imports ArgusKit ONLY for ``NotificationBurstSummary``, a pure value type
/// that deals in ids/names — never the ArgusKit `Task` domain model, so
/// `Task { … }` stays unambiguous here as long as it stays fully qualified
/// (`_Concurrency.Task`, used throughout below).
///
/// ## Non-bundled safety
/// `UNUserNotificationCenter.current()` traps when the process has no bundle
/// identifier (i.e. `swift run Argus`, a bare Mach-O). We detect that and
/// degrade to a no-op so the app still runs outside an `.app` bundle; inside a
/// real bundle the full path is exercised.
@MainActor
final class NotificationManager: NSObject, UNUserNotificationCenterDelegate {

    /// Invoked on notification tap with the carried task id. Wired by
    /// ``AppState`` to select + reveal that task.
    var onSelectTask: ((String) -> Void)?

    private let center: UNUserNotificationCenter?
    private var authorizationRequested = false
    private var authorized = false

    /// Task ids with a currently-posted needs-input notification. Guards against
    /// restacking a second banner for the same task while the first is still
    /// pending — cleared only when the task stops needing input (or is gone).
    private var pendingNeedsInput: Set<String> = []

    // nonisolated so the UNUserNotificationCenter completion closures (Sendable,
    // non-main-actor) can log; Logger is itself Sendable.
    private nonisolated static let log = Logger(subsystem: "com.argus.mac", category: "notifications")

    override init() {
        if Bundle.main.bundleIdentifier != nil {
            center = UNUserNotificationCenter.current()
        } else {
            center = nil
            Self.log.info("[notify] no bundle identifier — notifications disabled (bare executable)")
        }
        super.init()
        center?.delegate = self
    }

    // MARK: - Authorization (lazy, on first use)

    private func requestAuthorizationIfNeeded() {
        guard let center, !authorizationRequested else { return }
        authorizationRequested = true
        center.requestAuthorization(options: [.alert, .sound, .badge]) { [weak self] granted, error in
            _Concurrency.Task { @MainActor in
                self?.authorized = granted
                if let error {
                    Self.log.error("[notify] authorization error: \(String(describing: error), privacy: .public)")
                } else {
                    Self.log.info("[notify] authorization granted=\(granted)")
                }
            }
        }
    }

    // MARK: - Posting

    /// Posts a "needs input" banner for a task, deduped by id: a second call
    /// while one is still pending is dropped (no restacking).
    func notifyNeedsInput(taskID: String, taskName: String) {
        guard let center else { return }
        requestAuthorizationIfNeeded()
        guard !pendingNeedsInput.contains(taskID) else {
            Self.log.info("[notify] needs-input dedupe skip task=\(taskID, privacy: .public)")
            return
        }
        pendingNeedsInput.insert(taskID)
        post(center, id: Self.needsInputID(taskID), thread: taskID, taskID: taskID,
             title: taskName, body: "needs input")
        Self.log.info("[notify] needs-input posted task=\(taskID, privacy: .public)")
    }

    /// Posts (or, while a burst is ongoing, updates in place) a single
    /// coalesced banner summarizing a burst of needs-input arrivals — see
    /// ``NotificationFloodGate`` in ArgusKit for the burst policy that
    /// produces `summary`. Uses a fixed identifier so repeated calls during
    /// the same burst replace the previous banner rather than restacking.
    func notifyNeedsInputSummary(_ summary: NotificationBurstSummary) {
        guard let center else { return }
        requestAuthorizationIfNeeded()
        let content = UNMutableNotificationContent()
        content.title = summary.title
        content.body = summary.body
        content.threadIdentifier = Self.needsInputSummaryID
        content.sound = .default
        let request = UNNotificationRequest(identifier: Self.needsInputSummaryID, content: content, trigger: nil)
        center.add(request) { error in
            if let error {
                Self.log.error("[notify] summary post failed err=\(String(describing: error), privacy: .public)")
            }
        }
        Self.log.info("[notify] needs-input coalesced count=\(summary.taskNames.count, privacy: .public)")
    }

    /// Posts an "is idle" banner for a task. The frontmost-app gate lives in the
    /// caller (``AppState``); this just posts.
    func notifyIdle(taskID: String, taskName: String) {
        guard let center else { return }
        requestAuthorizationIfNeeded()
        post(center, id: Self.idleID(taskID), thread: taskID, taskID: taskID,
             title: taskName, body: "is idle")
        Self.log.info("[notify] idle posted task=\(taskID, privacy: .public)")
    }

    /// Clears the needs-input dedupe entry and removes any delivered/pending
    /// banner for the task. Safe to call for a task that never had one.
    func clearNeedsInput(taskID: String) {
        pendingNeedsInput.remove(taskID)
        let id = Self.needsInputID(taskID)
        center?.removeDeliveredNotifications(withIdentifiers: [id])
        center?.removePendingNotificationRequests(withIdentifiers: [id])
    }

    private func post(_ center: UNUserNotificationCenter, id: String, thread: String,
                      taskID: String, title: String, body: String) {
        let content = UNMutableNotificationContent()
        content.title = title
        content.body = body
        content.threadIdentifier = thread
        content.userInfo = ["task_id": taskID]
        content.sound = .default
        let request = UNNotificationRequest(identifier: id, content: content, trigger: nil)
        center.add(request) { error in
            if let error {
                Self.log.error("[notify] post failed id=\(id, privacy: .public) err=\(String(describing: error), privacy: .public)")
            }
        }
    }

    private static func needsInputID(_ taskID: String) -> String { "needs-input.\(taskID)" }
    private static func idleID(_ taskID: String) -> String { "idle.\(taskID)" }
    /// Distinct from `needsInputID`'s per-task scheme (never collides with a
    /// real task id) so a coalesced-burst banner and an individual banner
    /// never share an identifier.
    private static let needsInputSummaryID = "needs-input-summary"

    // MARK: - UNUserNotificationCenterDelegate (nonisolated: protocol isn't MainActor)

    /// Present banners even when the app is frontmost so a needs-input signal is
    /// never silently swallowed while the user is in another window.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .sound])
    }

    /// On tap: bring the app forward and select the carried task.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        let taskID = response.notification.request.content.userInfo["task_id"] as? String
        _Concurrency.Task { @MainActor [weak self] in
            NSApplication.shared.activate(ignoringOtherApps: true)
            if let taskID { self?.onSelectTask?(taskID) }
        }
        completionHandler()
    }
}

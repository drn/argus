import Foundation
import Observation
import ArgusKit

/// The ArgusKit task model. Aliased because SwiftUI code lives alongside Swift
/// Concurrency's `Task`, and the two names collide; using `ArgusTask` for the
/// model keeps `Task { … }` (concurrency) unambiguous when qualified.
typealias ArgusTask = ArgusKit.Task

/// The app's single source of truth: owns the ``ArgusClient``, the task list,
/// the current selection, and the connection state. `@MainActor` because every
/// property drives SwiftUI; `@Observable` so views re-render on mutation.
@MainActor
@Observable
final class AppState {
    /// Coarse connection status surfaced in the toolbar + banner.
    enum ConnectionState: Equatable {
        case connecting
        case connected
        case error(String)
    }

    // MARK: - Observable state

    private(set) var tasks: [ArgusTask] = []
    var selectedTaskID: ArgusTask.ID?
    private(set) var connection: ConnectionState = .connecting

    /// Project + backend names pulled once from `/api/config`, feeding the New
    /// Task sheet's pickers.
    private(set) var projectNames: [String] = []
    private(set) var backendNames: [String] = []
    /// The daemon's configured default backend name (`Defaults.Backend`), used
    /// to label the New Task sheet's "use default" backend option.
    private(set) var defaultBackendName: String = ""

    /// Which tab of the selected task's detail view is showing. Forced to
    /// ``DetailTab/terminal`` after creating or forking a task so the user
    /// lands on the live session rather than whatever tab a previously
    /// selected task left scrolled to.
    enum DetailTab: Hashable {
        case terminal, diff, files, info
    }
    var activeDetailTab: DetailTab = .terminal

    /// A destructive/interrupting task action awaiting user confirmation.
    /// Driven by a single `.confirmationDialog` mounted once in
    /// ``ContentView`` — the sidebar context menu and the detail pane's
    /// overflow menu both just set this instead of owning their own dialogs.
    enum PendingConfirmation {
        case stop(ArgusTask)
        case delete(ArgusTask)
    }
    var pendingConfirmation: PendingConfirmation?

    /// Non-nil while the rename sheet is open, carrying the task being
    /// renamed. Driven by a single `.sheet(item:)` in ``ContentView``.
    var renamingTask: ArgusTask?

    /// A transient, non-blocking error surfaced after a failed task action
    /// (stop/restart/resume/archive/rename/fork/delete). Auto-dismisses after
    /// a few seconds; visually mirrors ``ConnectionBanner`` but is
    /// action-scoped rather than connection-scoped (see ``ActionErrorBanner``).
    private(set) var actionError: String?
    private var actionErrorDismissTask: _Concurrency.Task<Void, Never>?

    // MARK: - Collaborators

    let preferences: Preferences
    private var client: ArgusClient?
    private var pollTask: _Concurrency.Task<Void, Never>?

    /// Live terminal controllers, cached by task ID so switching tasks in the
    /// sidebar and back preserves scrollback. Torn down when the task is deleted
    /// (``pruneTerminalControllers(keeping:)``) or the client is rebuilt.
    private var terminalControllers: [String: TerminalController] = [:]

    /// Poll cadence. A later phase swaps this whole path for the `/api/events`
    /// SSE stream — see ``refreshOnce()`` / ``startPolling()``.
    private let pollInterval: Duration = .seconds(2)

    init(preferences: Preferences = Preferences()) {
        self.preferences = preferences
    }

    // MARK: - Selection helpers

    var selectedTask: ArgusTask? {
        guard let id = selectedTaskID else { return nil }
        return tasks.first { $0.id == id }
    }

    // MARK: - Grouping (drives the sidebar sections)

    /// Active = not archived and pending or in_progress.
    var activeTasks: [ArgusTask] {
        tasks.filter { !$0.archived && ($0.taskStatus == .pending || $0.taskStatus == .inProgress) }
    }

    var inReviewTasks: [ArgusTask] {
        tasks.filter { !$0.archived && $0.taskStatus == .inReview }
    }

    var completeTasks: [ArgusTask] {
        tasks.filter { !$0.archived && $0.taskStatus == .complete }
    }

    var archivedTasks: [ArgusTask] {
        tasks.filter { $0.archived }
    }

    // MARK: - Connection lifecycle

    /// (Re)builds the client from the current preferences and restarts polling.
    /// Safe to call repeatedly — cancels any in-flight loop first.
    func connect() {
        stop()
        connection = .connecting
        // A rebuilt client may point at a different server/token, so drop every
        // cached terminal controller (each holds its own client).
        teardownTerminalControllers()
        do {
            client = try makeClient()
        } catch {
            client = nil
            connection = .error(Self.describe(error))
            return
        }
        startPolling()
    }

    // MARK: - Terminal controllers

    /// Returns the cached terminal controller for a task, creating it on first
    /// use. Returns nil when there is no live client (not connected yet).
    func terminalController(for taskID: String) -> TerminalController? {
        if let existing = terminalControllers[taskID] { return existing }
        guard let client else { return nil }
        let controller = TerminalController(taskID: taskID, client: client)
        terminalControllers[taskID] = controller
        return controller
    }

    /// Tears down controllers whose task no longer exists.
    private func pruneTerminalControllers(keeping ids: Set<String>) {
        for (id, controller) in terminalControllers where !ids.contains(id) {
            controller.teardown()
            terminalControllers[id] = nil
        }
    }

    private func teardownTerminalControllers() {
        for controller in terminalControllers.values { controller.teardown() }
        terminalControllers.removeAll()
    }

    /// Retry after a connection failure (wired to the banner's Retry button).
    func retry() {
        connect()
    }

    /// Stops the polling loop. Called on reconnect and can be used at teardown.
    func stop() {
        pollTask?.cancel()
        pollTask = nil
    }

    // MARK: - Data fetch

    /// One refresh pass. Structured as a single async call so the SSE swap is a
    /// drop-in: replace ``startPolling()`` with a stream consumer that calls the
    /// same mutation sites (`tasks = …`, `connection = …`).
    func refreshOnce() async {
        guard let client else { return }
        do {
            // "all" so archived tasks are present for the collapsed section.
            let fetched = try await client.tasks(archived: "all")
            tasks = fetched
            pruneTerminalControllers(keeping: Set(fetched.map(\.id)))
            connection = .connected
        } catch {
            connection = .error(Self.describe(error))
        }
    }

    /// Fetches links (PR / branch URLs) for a task's Info tab. Non-throwing:
    /// a failure yields an empty list rather than surfacing an error.
    func fetchLinks(taskID: String) async -> [Link] {
        guard let client else { return [] }
        return (try? await client.links(taskID: taskID)) ?? []
    }

    // MARK: - Git surfaces (Diff / Files tabs)
    //
    // Thin throwing wrappers over the ArgusKit git endpoints. Views own their
    // own loading/error state (see ``DiffTabModel`` / ``FilesTabModel``); these
    // just gate on a live client and run off the caller's task. `ArgusClient` is
    // Sendable and actor-free, so the awaits hop off the main actor for the URL
    // round-trip and resume back here.

    func gitStatus(taskID: String) async throws -> GitStatus {
        guard let client else { throw ArgusError.invalidResponse("not connected") }
        return try await client.gitStatus(taskID: taskID)
    }

    func gitDiff(taskID: String, path: String) async throws -> GitDiff {
        guard let client else { throw ArgusError.invalidResponse("not connected") }
        return try await client.gitDiff(taskID: taskID, path: path)
    }

    func fileTree(taskID: String, dir: String = "") async throws -> FileTree {
        guard let client else { throw ArgusError.invalidResponse("not connected") }
        return try await client.fileTree(taskID: taskID, dir: dir)
    }

    // MARK: - Task lifecycle actions

    /// Creates a task from the New Task sheet. Throws (rather than routing
    /// through ``showActionError``) so the sheet can show an inline,
    /// input-preserving error instead of a toast. On success, refreshes,
    /// selects the new task, and switches the detail pane to Terminal.
    func createTask(_ req: CreateTaskRequest) async throws -> String {
        guard let client else { throw ArgusError.invalidResponse("not connected") }
        let resp = try await client.createTask(req)
        await refreshOnce()
        selectedTaskID = resp.id
        activeDetailTab = .terminal
        return resp.id
    }

    /// `POST /api/tasks/{id}/stop` — called after the Stop confirmation.
    func stop(_ task: ArgusTask) async {
        await perform(task, label: "stop") { try await $0.stopTask(id: task.id) }
    }

    func restart(_ task: ArgusTask) async {
        await perform(task, label: "restart") { _ = try await $0.restartTask(id: task.id) }
    }

    func resume(_ task: ArgusTask) async {
        await perform(task, label: "resume") { _ = try await $0.resumeTask(id: task.id) }
    }

    /// Archiving the selected task moves it out of the sidebar's default
    /// (collapsed-archive) view, so clear the selection rather than leaving
    /// the detail pane pointed at a task that just left the visible list.
    func archive(_ task: ArgusTask) async {
        await perform(task, label: "archive") { try await $0.archiveTask(id: task.id) }
        if selectedTaskID == task.id { selectedTaskID = nil }
    }

    func unarchive(_ task: ArgusTask) async {
        await perform(task, label: "unarchive") { try await $0.unarchiveTask(id: task.id) }
    }

    func rename(_ task: ArgusTask, to name: String) async {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        await perform(task, label: "rename") { try await $0.renameTask(id: task.id, name: trimmed) }
    }

    /// `POST /api/tasks/{id}/fork` — mirrors ``createTask(_:)``'s post-success
    /// UX (select + switch to Terminal) since a fork is effectively a new task.
    func fork(_ task: ArgusTask) async {
        guard let client else { return }
        do {
            let resp = try await client.forkTask(id: task.id)
            await refreshOnce()
            selectedTaskID = resp.id
            activeDetailTab = .terminal
        } catch {
            showActionError("Failed to fork \"\(task.name)\": \(Self.describe(error))")
        }
    }

    /// `DELETE /api/tasks/{id}` — called after the Delete confirmation. Clears
    /// the selection up front; ``refreshOnce()``'s existing
    /// ``pruneTerminalControllers(keeping:)`` pass tears down the terminal
    /// controller once the deleted id drops out of the fetched list.
    func delete(_ task: ArgusTask) async {
        if selectedTaskID == task.id { selectedTaskID = nil }
        await perform(task, label: "delete") { try await $0.deleteTask(id: task.id) }
    }

    /// Dismisses the action-error toast immediately (wired to its close
    /// button in ``ActionErrorBanner``).
    func dismissActionError() {
        actionErrorDismissTask?.cancel()
        actionErrorDismissTask = nil
        actionError = nil
    }

    /// Shared plumbing for the simple task actions: run the client call,
    /// refresh the task list on success, and surface a transient error on
    /// failure.
    private func perform(_ task: ArgusTask, label: String,
                         _ body: (ArgusClient) async throws -> Void) async {
        guard let client else { return }
        do {
            try await body(client)
            await refreshOnce()
        } catch {
            showActionError("Failed to \(label) \"\(task.name)\": \(Self.describe(error))")
        }
    }

    private func showActionError(_ message: String) {
        actionError = message
        actionErrorDismissTask?.cancel()
        actionErrorDismissTask = _Concurrency.Task { [weak self] in
            try? await _Concurrency.Task.sleep(for: .seconds(6))
            guard !_Concurrency.Task.isCancelled else { return }
            self?.actionError = nil
        }
    }

    // MARK: - Internals

    private func makeClient() throws -> ArgusClient {
        let baseURL: URL?
        if let raw = preferences.serverURLString, !raw.isEmpty {
            guard let url = URL(string: raw), url.scheme != nil else {
                throw ArgusError.invalidResponse("invalid server URL: \(raw)")
            }
            baseURL = url
        } else {
            baseURL = nil // ArgusConfig falls back to the default local port.
        }
        // Empty token override => resolve from ~/.argus/api-token.
        let tokenOverride = preferences.tokenOverride
        let config = try ArgusConfig.resolve(baseURL: baseURL, token: tokenOverride)
        return ArgusClient(config: config)
    }

    private func startPolling() {
        pollTask = _Concurrency.Task { [weak self] in
            guard let self else { return }
            await self.loadConfigNames()
            while !_Concurrency.Task.isCancelled {
                await self.refreshOnce()
                try? await _Concurrency.Task.sleep(for: self.pollInterval)
            }
        }
    }

    /// Pulls project + backend names from `/api/config` once per connect.
    /// The daemon marshals its Go `config.Config` with field names verbatim, so
    /// the keys are PascalCase (`Projects`, `Backends`).
    private func loadConfigNames() async {
        guard let client else { return }
        guard let cfg = try? await client.config() else { return }
        if let projects = cfg["Projects"]?.objectValue {
            projectNames = projects.keys.sorted()
        }
        if let backends = cfg["Backends"]?.objectValue {
            backendNames = backends.keys.sorted()
        }
        if let backend = cfg["Defaults"]?["Backend"]?.stringValue {
            defaultBackendName = backend
        }
    }

    /// Human-readable, token-free rendering of an error for the UI.
    static func describe(_ error: Error) -> String {
        switch error {
        case let ArgusError.transport(msg):
            return "Cannot reach daemon (\(msg))"
        case let ArgusError.http(status, body):
            return "HTTP \(status): \(body)"
        case let ArgusError.decoding(msg):
            return "Bad response: \(msg)"
        case let ArgusError.invalidResponse(msg):
            return msg
        case let ArgusError.tokenUnavailable(path):
            return "No API token at \(path)"
        default:
            return String(describing: error)
        }
    }
}

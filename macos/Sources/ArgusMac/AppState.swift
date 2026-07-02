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

    /// Project + backend names pulled once from `/api/config`. Populated for
    /// future new-task UI; today they feed nothing but are cheap to keep fresh.
    private(set) var projectNames: [String] = []
    private(set) var backendNames: [String] = []

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

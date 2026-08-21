import AppKit
import Foundation
import Observation
import os
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

    /// Task ids the daemon currently reports as blocked waiting on the user.
    /// Fed incrementally from `session.needs_input` events and rebuilt wholesale
    /// on every `/api/tasks` snapshot. Drives the dock badge, the menu-bar count,
    /// and the sidebar needs-input marker.
    private(set) var needsInputTaskIDs: Set<String> = []

    /// Task ids currently bound to a live Hera role (worker or coordinator),
    /// mirroring the TUI's "hera-managed" classification (`hideHeraManaged` /
    /// `isHeraSpawnedWorker` in `internal/tui/taskview/tasklist.go`): a task
    /// with a live binding in the `/api/hera` roster. There is no such field
    /// on ``ArgusKit/Task`` itself — `GET /api/hera` is a separate endpoint —
    /// so this is rebuilt wholesale alongside every task snapshot
    /// (``refreshHeraManagedTaskIDs()``), the same cadence ``HeraTab`` already
    /// polls on its own. Drives the sidebar's hera-managed visibility toggle.
    private(set) var heraManagedTaskIDs: Set<String> = []

    /// Task ids known to be pinned — tracked purely client-side because
    /// `/api/tasks`'s lossy wire shape (`taskJSON` in
    /// `internal/api/handlers.go`) omits `pinned` entirely; only the raw
    /// endpoint (`GET /api/tasks/{id}/raw`) carries it. Unlike
    /// ``needsInputTaskIDs``, this set is NOT rebuilt from every `/api/tasks`
    /// snapshot — it starts empty on every ``connect()`` and is populated
    /// lazily via ``refreshPinnedState(taskID:)`` (called as each sidebar row
    /// appears) and kept current afterward by this app's own optimistic
    /// updates in ``setPinned(_:pinned:)``. A pin/unpin made from another
    /// client (the TUI, the web SPA) is invisible to this cache until the
    /// row is re-fetched — an accepted client-side-tracking gap, not a bug.
    private(set) var pinnedTaskIDs: Set<String> = []

    /// Bound to the New Task sheet's presentation so both the toolbar `+` and the
    /// menu-bar "New Task…" item can open it. Owned here (not in a view's local
    /// `@State`) so an out-of-window trigger still works.
    var isPresentingNewTask = false

    /// Bound to the shortcuts-help sheet's presentation (Cmd+Shift+/).
    var isPresentingShortcutsHelp = false

    // MARK: - Settings mirrors (observable; persisted via Preferences)

    /// Whether to post a notification when a task needs input.
    var notifyOnNeedsInput: Bool {
        didSet { preferences.notifyOnNeedsInput = notifyOnNeedsInput }
    }
    /// Whether to post a notification when a task goes idle (app not frontmost).
    var notifyOnIdle: Bool {
        didSet { preferences.notifyOnIdle = notifyOnIdle }
    }
    /// Whether the menu-bar extra is shown.
    var showMenuBarExtra: Bool {
        didSet { preferences.showMenuBarExtra = showMenuBarExtra }
    }

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

        /// Parses `ARGUS_MAC_INITIAL_TAB`'s value; unrecognized strings are ignored.
        init?(envValue: String) {
            switch envValue {
            case "terminal": self = .terminal
            case "diff": self = .diff
            case "files": self = .files
            case "info": self = .info
            default: return nil
            }
        }
    }
    var activeDetailTab: DetailTab = .terminal

    /// True while the Hera roster (``HeraTab``) replaces the detail pane.
    /// Toggled from the toolbar; ``selectHeraTask(_:)`` flips it back off when
    /// a role's bound task is clicked so the user lands on that task's
    /// terminal.
    var showingHera = false

    /// A destructive/interrupting task action awaiting user confirmation.
    /// Driven by a single `.confirmationDialog` mounted once in
    /// ``ContentView`` — the sidebar context menu and the detail pane's
    /// overflow menu both just set this instead of owning their own dialogs.
    enum PendingConfirmation {
        case stop(ArgusTask)
        case delete(ArgusTask)
        /// The toolbar overflow menu's "Prune stale worktrees" item — global
        /// and cross-task (not scoped to one ``ArgusTask``), so it carries no
        /// associated value, mirroring the TUI's own Ctrl+R caution gate.
        case pruneCompleted
    }
    var pendingConfirmation: PendingConfirmation?

    /// Non-nil while the rename sheet is open, carrying the task being
    /// renamed. Driven by a single `.sheet(item:)` in ``ContentView``.
    var renamingTask: ArgusTask?

    /// Non-nil while the Claude session picker sheet is open, carrying the
    /// task, its available sessions (newest first), and the currently-active
    /// session id (may be `""`). Populated up front by
    /// ``openClaudeSessionPicker(for:)`` — never opened empty/broken on a
    /// fetch failure — and driven by a single `.sheet(item:)` in
    /// ``ContentView``, mirroring ``renamingTask``'s presentation mechanics.
    struct ClaudeSessionPickerState: Identifiable, Equatable {
        let task: ArgusTask
        let sessions: [ClaudeSession]
        let currentSessionID: String
        var id: String { task.id }
    }
    var claudeSessionPicker: ClaudeSessionPickerState?

    /// A transient, non-blocking error surfaced after a failed task action
    /// (stop/restart/resume/archive/rename/fork/delete). Auto-dismisses after
    /// a few seconds; visually mirrors ``ConnectionBanner`` but is
    /// action-scoped rather than connection-scoped (see ``ActionErrorBanner``).
    private(set) var actionError: String?
    private var actionErrorDismissTask: _Concurrency.Task<Void, Never>?

    // MARK: - Collaborators

    let preferences: Preferences
    private var client: ArgusClient?

    /// The pure task-list sync state machine (cursor + reconnect/backoff policy).
    /// Recreated on every ``connect()`` so a rebuilt client starts from a fresh
    /// cursor.
    private var eventSession = EventsStreamSession()
    private var eventStreamTask: _Concurrency.Task<Void, Never>?
    private var snapshotTask: _Concurrency.Task<Void, Never>?
    private var fallbackPollTask: _Concurrency.Task<Void, Never>?

    /// Subscribe-before-snapshot fencing state. While `eventBuffering` is true the
    /// stream is open but the `/api/tasks` snapshot has not landed yet, so decoded
    /// events are buffered and applied on top once it does (see ``consumeEvents(since:)``).
    private var eventBuffering = false
    private var eventBuffer: [DaemonEvent] = []
    private var eventBufferNeedsResnapshot = false

    let notificationManager = NotificationManager()

    /// Slow safety-net poll interval. The 2s poll is gone — the event stream is
    /// the live path; this only heals a silently-wedged stream or a delta the
    /// buffering window happened to miss.
    private let fallbackPollInterval: Duration = .seconds(30)

    private static let log = Logger(subsystem: "com.argus.mac", category: "appstate")

    // MARK: - Launch-state defaults (automation / deep-link hooks)

    /// Set once, before the first task snapshot lands, so the window never
    /// opens empty. See ``applyLaunchStateIfNeeded(_:)``.
    private var didApplyLaunchState = false

    /// Automation/deep-link hook, checked once at startup: selects a task by
    /// id or exact name match at launch, overriding the default auto-select.
    /// e.g. `ARGUS_MAC_SELECT_TASK=my-task-name open macos/dist/Argus.app`.
    private static let envSelectTask = ProcessInfo.processInfo.environment["ARGUS_MAC_SELECT_TASK"]

    /// Automation/deep-link hook, checked once at startup: sets the initial
    /// detail tab (`terminal`|`diff`|`files`|`info`) regardless of which task
    /// ends up selected. Unrecognized values are ignored.
    private static let envInitialTab = ProcessInfo.processInfo.environment["ARGUS_MAC_INITIAL_TAB"]
        .flatMap(DetailTab.init(envValue:))

    /// Live terminal controllers, cached by task ID so switching tasks in the
    /// sidebar and back preserves scrollback. Torn down when the task is deleted
    /// (``pruneTerminalControllers(keeping:)``) or the client is rebuilt.
    private var terminalControllers: [String: TerminalController] = [:]

    init(preferences: Preferences = Preferences()) {
        self.preferences = preferences
        self.notifyOnNeedsInput = preferences.notifyOnNeedsInput
        self.notifyOnIdle = preferences.notifyOnIdle
        self.showMenuBarExtra = preferences.showMenuBarExtra
        self.notificationManager.onSelectTask = { [weak self] id in
            self?.selectTask(id)
        }
    }

    // MARK: - Selection helpers

    var selectedTask: ArgusTask? {
        guard let id = selectedTaskID else { return nil }
        return tasks.first { $0.id == id }
    }

    /// Looks up a task by id outside the `selectedTaskID`-scoped
    /// ``selectedTask``. Used by the Terminal tab's local key monitor to
    /// resolve "the task currently showing in this terminal" (via
    /// `TerminalController.taskID`) for its Cmd+Shift+U open-PR fallback
    /// (add-mac-keybinding-parity Stage 5) — the terminal's bound task
    /// isn't necessarily `selectedTask` the instant a rebuild is in
    /// flight, so this looks it up directly rather than assuming they
    /// match.
    func task(withID id: String) -> ArgusTask? {
        tasks.first { $0.id == id }
    }

    // MARK: - Grouping (drives the sidebar sections)

    /// Active = not archived and pending or in_progress. Feeds the launch
    /// auto-select fallback (``applyLaunchStateIfNeeded(_:)``) and the
    /// menu-bar dropdown (``MenuBarContent``) — both want "tasks currently
    /// doing something", independent of the sidebar's project-folder layout.
    var activeTasks: [ArgusTask] {
        tasks.filter { !$0.archived && ($0.taskStatus == .pending || $0.taskStatus == .inProgress) }
    }

    var archivedTasks: [ArgusTask] {
        tasks.filter { $0.archived }
    }

    /// Non-archived tasks (every status), grouped into project folders and
    /// ordered the way the TUI's task list orders them: projects sorted
    /// alphabetically (`"(no project)"` last-resort label for an empty
    /// project field), tasks within a folder in the order the daemon
    /// returned them (`created_at ASC` — see `internal/tui/taskview/tasklist.go`
    /// `groupByProject`). Drives ``Sidebar``'s per-folder sections; each row
    /// still carries its own status icon since the folder no longer implies
    /// a single status.
    var tasksByFolder: [TaskFolder] {
        TaskGrouping.byProject(tasks.filter { !$0.archived })
    }

    /// Archived tasks, grouped the same way as ``tasksByFolder`` — used inside
    /// the sidebar's collapsed Archived section so archived tasks are still
    /// organized by project once expanded.
    var archivedTasksByFolder: [TaskFolder] {
        TaskGrouping.byProject(archivedTasks)
    }

    // MARK: - Connection lifecycle

    /// (Re)builds the client from the current preferences and restarts the event
    /// stream + fallback poll. Safe to call repeatedly — cancels any in-flight
    /// work first.
    func connect() {
        stop()
        connection = .connecting
        // A rebuilt client may point at a different server/token, so drop every
        // cached terminal controller (each holds its own client) and reset the
        // event cursor.
        teardownTerminalControllers()
        eventSession = EventsStreamSession()
        do {
            client = try makeClient()
        } catch {
            client = nil
            connection = .error(Self.describe(error))
            return
        }
        _Concurrency.Task { [weak self] in await self?.loadConfigNames() }
        startEventStream()
        startFallbackPoll()
    }

    // MARK: - Terminal controllers

    /// Returns the cached terminal controller for a task, creating it on first
    /// use. Returns nil when there is no live client (not connected yet).
    func terminalController(for taskID: String) -> TerminalController? {
        if let existing = terminalControllers[taskID] { return existing }
        guard let client else { return nil }
        let controller = TerminalController(taskID: taskID, client: client)
        // The Terminal tab's local key monitor (`FocusTakingTerminalView`,
        // add-mac-keybinding-parity Stage 5) needs both this controller
        // (to run the scroll/copy actions) and this AppState (to run the
        // task-switch/tab-cycle/open-PR actions) — wired here, the one
        // place that constructs the view's backing controller, rather than
        // widening `TerminalController`'s own init signature for it.
        if let view = controller.terminalView as? FocusTakingTerminalView {
            view.controller = controller
            view.appState = self
        }
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

    /// Cancels the event stream, any in-flight snapshot, and the fallback poll.
    /// Called on reconnect and can be used at teardown.
    func stop() {
        eventStreamTask?.cancel()
        eventStreamTask = nil
        snapshotTask?.cancel()
        snapshotTask = nil
        fallbackPollTask?.cancel()
        fallbackPollTask = nil
        eventBuffering = false
        eventBuffer.removeAll()
    }

    /// Brings the app forward, selects a task, and shows its live terminal.
    /// Wired to notification taps and the menu-bar task list.
    func selectTask(_ id: String) {
        NSApplication.shared.activate(ignoringOtherApps: true)
        selectedTaskID = id
        activeDetailTab = .terminal
    }

    /// Opens the New Task sheet (from the menu-bar item), bringing the app
    /// forward first.
    func requestNewTask() {
        NSApplication.shared.activate(ignoringOtherApps: true)
        isPresentingNewTask = true
    }

    /// Selects a role's bound task from the Hera roster and swaps the detail
    /// pane back from the roster to the normal tabbed task view.
    func selectHeraTask(_ id: String) {
        selectedTaskID = id
        activeDetailTab = .terminal
        showingHera = false
    }

    // MARK: - Data fetch

    /// Fetches `/api/tasks` and applies it as the authoritative baseline: the
    /// task list, the terminal-controller pruning, and a wholesale rebuild of the
    /// needs-input set (so a badge/marker can never drift from the server's
    /// truth). Silent — it never posts notifications; those fire only on live
    /// `session.*` edges. Used for the initial/reconnect snapshot, after task
    /// actions, and by the fallback poll.
    func refreshOnce() async {
        guard let client else { return }
        do {
            // "all" so archived tasks are present for the collapsed section.
            let fetched = try await client.tasks(archived: "all")
            applySnapshot(fetched)
            connection = .connected
        } catch is CancellationError {
            return
        } catch {
            connection = .error(Self.describe(error))
            return
        }
        await refreshHeraManagedTaskIDs()
    }

    /// Best-effort refresh of ``heraManagedTaskIDs`` from `GET /api/hera`.
    /// Silent on failure (mirrors ``fetchLinks(taskID:)``'s non-throwing
    /// pattern) — a stale hera-managed set just leaves the sidebar toggle's
    /// filter a beat behind, never a hard error surfaced to the user.
    private func refreshHeraManagedTaskIDs() async {
        guard let client else { return }
        guard let roster = try? await client.heraRoster() else { return }
        var ids: Set<String> = []
        for orch in roster.orchestrators {
            for role in orch.roles where role.live && !role.taskID.isEmpty {
                ids.insert(role.taskID)
            }
        }
        for role in roster.freelance where role.live && !role.taskID.isEmpty {
            ids.insert(role.taskID)
        }
        heraManagedTaskIDs = ids
    }

    /// Applies a fresh `/api/tasks` result: replaces the list, prunes dead
    /// terminal controllers, and rebuilds the needs-input set from the server's
    /// per-task `needs_input` flags.
    private func applySnapshot(_ fetched: [ArgusTask]) {
        tasks = fetched
        pruneTerminalControllers(keeping: Set(fetched.map(\.id)))
        let rebuilt = Set(fetched.filter { $0.needsInput && !$0.archived }.map(\.id))
        // Clear any stale notifications for tasks that no longer need input.
        for gone in needsInputTaskIDs.subtracting(rebuilt) {
            notificationManager.clearNeedsInput(taskID: gone)
        }
        needsInputTaskIDs = rebuilt
        updateBadge()
        // Archiving clears `pinned` server-side (`model.Task.SetArchived`), and a
        // deleted task obviously can't still be pinned — reconcile the
        // client-only cache against the authoritative snapshot so it can't
        // drift stale after either transition, regardless of which client (or
        // this one, via a stale local toggle) caused it.
        let archivedOrGone = Set(fetched.filter(\.archived).map(\.id))
            .union(pinnedTaskIDs.subtracting(fetched.map(\.id)))
        pinnedTaskIDs.subtract(archivedOrGone)
        applyLaunchStateIfNeeded(fetched)
    }

    /// Deterministic launch-state defaults, applied once on the first task
    /// snapshot to land after launch so the window never opens on an empty
    /// detail pane:
    ///  - `ARGUS_MAC_SELECT_TASK` (id or exact name match), if set, wins over
    ///    the default auto-select.
    ///  - Otherwise, auto-selects the most recently created `in_progress`
    ///    task, falling back to the first task in the Active section
    ///    (``activeTasks``).
    ///  - `ARGUS_MAC_INITIAL_TAB`, if set to a recognized tab, is applied
    ///    regardless of which task (if any) got selected.
    /// A pre-existing selection (e.g. from a prior snapshot or user click
    /// racing this one) is left untouched.
    private func applyLaunchStateIfNeeded(_ fetched: [ArgusTask]) {
        guard !didApplyLaunchState, !fetched.isEmpty else { return }
        didApplyLaunchState = true

        if let tab = Self.envInitialTab {
            activeDetailTab = tab
        }

        guard selectedTaskID == nil else { return }

        if let want = Self.envSelectTask,
           let match = fetched.first(where: { $0.id == want || $0.name == want }) {
            selectedTaskID = match.id
            return
        }

        let inProgress = fetched.filter { !$0.archived && $0.taskStatus == .inProgress }
        if let mostRecent = inProgress.max(by: { $0.createdAt < $1.createdAt }) {
            selectedTaskID = mostRecent.id
        } else if let firstActive = activeTasks.first {
            selectedTaskID = firstActive.id
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

    // MARK: - Hera / Schedules / System surfaces
    //
    // Same shape as the git surfaces above: thin throwing wrappers gated on a
    // live client. Views (``HeraTab``, ``SchedulesView``, ``SystemView``) own
    // their own loading/error/polling state.

    /// `GET /api/hera` — the read-only orchestration roster.
    func heraRoster() async throws -> HeraRoster {
        guard let client else { throw ArgusError.invalidResponse("not connected") }
        return try await client.heraRoster()
    }

    func schedules() async throws -> [Schedule] {
        guard let client else { throw ArgusError.invalidResponse("not connected") }
        return try await client.schedules()
    }

    @discardableResult
    func createSchedule(_ req: ScheduleRequest) async throws -> Schedule {
        guard let client else { throw ArgusError.invalidResponse("not connected") }
        return try await client.createSchedule(req)
    }

    @discardableResult
    func updateSchedule(id: String, _ req: ScheduleRequest) async throws -> Schedule {
        guard let client else { throw ArgusError.invalidResponse("not connected") }
        return try await client.updateSchedule(id: id, req)
    }

    func deleteSchedule(id: String) async throws {
        guard let client else { throw ArgusError.invalidResponse("not connected") }
        try await client.deleteSchedule(id: id)
    }

    @discardableResult
    func runSchedule(id: String) async throws -> String {
        guard let client else { throw ArgusError.invalidResponse("not connected") }
        return try await client.runSchedule(id: id)
    }

    /// `GET /api/status` — the daemon's at-a-glance counts.
    func daemonStatus() async throws -> DaemonStatus {
        guard let client else { throw ArgusError.invalidResponse("not connected") }
        return try await client.status()
    }

    /// `GET /api/system-metrics` — the cached host-load snapshot.
    func systemMetrics() async throws -> SystemMetrics {
        guard let client else { throw ArgusError.invalidResponse("not connected") }
        return try await client.systemMetrics()
    }

    /// Re-fetches `/api/config` (wired to the System panel's Reload button).
    func reloadConfig() async {
        await loadConfigNames()
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

    /// `POST /api/tasks/{id}/status` — the sidebar row context menu's
    /// status-advance/status-revert actions (mirrors the TUI's `s`/`S` keys).
    /// Callers pass ``TaskStatus/advanced()``/``TaskStatus/reverted()``;
    /// those clamp at the ladder's ends, so this is always a well-formed
    /// transition (or a harmless no-op at `.complete`/`.pending`).
    func setStatus(_ task: ArgusTask, to status: TaskStatus) async {
        await perform(task, label: "update status") {
            try await $0.setStatus(id: task.id, status: status.rawValue)
        }
    }

    /// Returns whether ``task`` is known to be pinned. Backed by
    /// ``pinnedTaskIDs``, a client-side-only cache — see its doc comment for
    /// why `/api/tasks`'s lossy shape can't answer this directly.
    func isPinned(_ task: ArgusTask) -> Bool {
        pinnedTaskIDs.contains(task.id)
    }

    /// Lazily backfills ``pinnedTaskIDs`` for one task via the raw endpoint.
    /// Called as each sidebar row appears (see ``TaskRow``) so its Pin/Unpin
    /// label renders correctly without an eager bulk fetch of every task's
    /// raw representation. Silent on failure — falls back to the "not
    /// pinned" default rather than surfacing an error toast for a
    /// non-interactive background refresh.
    func refreshPinnedState(taskID: String) async {
        guard let client, let raw = try? await client.rawTask(id: taskID) else { return }
        if raw["pinned"]?.boolValue == true {
            pinnedTaskIDs.insert(taskID)
        } else {
            pinnedTaskIDs.remove(taskID)
        }
    }

    /// `PUT /api/tasks/{id}/raw` via ``ArgusClient/setPinned(id:pinned:)``.
    /// Updates ``pinnedTaskIDs`` optimistically, but only on success (unlike
    /// ``perform(_:label:_:)``'s callers, this one's success/failure
    /// distinction matters: the client-side cache is this app's ONLY record
    /// of pin state, so marking it pinned after a failed request would lie).
    func setPinned(_ task: ArgusTask, pinned: Bool) async {
        guard let client else { return }
        do {
            try await client.setPinned(id: task.id, pinned: pinned)
            if pinned {
                pinnedTaskIDs.insert(task.id)
            } else {
                pinnedTaskIDs.remove(task.id)
            }
            await refreshOnce()
        } catch {
            showActionError("Failed to \(pinned ? "pin" : "unpin") \"\(task.name)\": \(Self.describe(error))")
        }
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

    /// Opens the task's worktree root in Finder — the mac equivalent of the
    /// TUI's "open repo" action. Mirrors ``FilesTab``'s per-file "Reveal in
    /// Finder" (`NSWorkspace.shared.selectFile`), but for the whole worktree
    /// root rather than one file, so it uses `open(_:)` instead. A missing or
    /// empty `worktreePath` (a task whose worktree was never created, or one
    /// already torn down) is a silent no-op rather than opening garbage.
    func openRepo(_ task: ArgusTask) {
        guard let path = task.worktreePath, !path.isEmpty else { return }
        NSWorkspace.shared.open(URL(fileURLWithPath: path))
    }

    /// Opens the task's pull request in the default browser — the mac
    /// equivalent of ``DetailHeaderChips``'s `PRChip` tap handler, reachable
    /// from a shortcut instead of a click. Fetches the task's links fresh
    /// (mirroring ``fetchLinks(taskID:)``'s own no-cache behavior) and opens
    /// the first PR link found. No PR for this task is a normal, expected
    /// outcome — surfaced via the same non-blocking ``actionError`` banner
    /// every other action failure uses, not a crash or a silent no-op.
    func openPR(for task: ArgusTask) async {
        let links = await fetchLinks(taskID: task.id)
        guard let prLink = links.first(where: { $0.isPR }), let url = prLink.webURL else {
            showActionError("No PR found for \u{201C}\(task.name)\u{201D}.")
            return
        }
        NSWorkspace.shared.open(url)
    }

    /// Moves the sidebar selection to the next task whose session needs
    /// input (mirroring the TUI's jump-to-next-needs-input rail action). A
    /// nil result (nothing currently needs input) is a no-op — the selection
    /// is left untouched. Ordering follows the sidebar's own visual order
    /// (``tasksByFolder``, project-folder then creation order) so cycling
    /// matches what the user sees scanning the rail top to bottom.
    func jumpToNextNeedsInput() {
        let orderedIDs = tasksByFolder.flatMap { $0.tasks.map(\.id) }
        guard let next = NeedsInputNavigation.next(orderedIDs: orderedIDs,
                                                    needingInput: needsInputTaskIDs,
                                                    current: selectedTaskID) else { return }
        selectedTaskID = next
    }

    /// Moves the sidebar selection to the previous/next task in the
    /// sidebar's own visual order (``tasksByFolder``, project-folder then
    /// creation order) — the Terminal tab's Cmd+Up/Cmd+Down shortcut
    /// (spec.md "Switch tasks via Cmd+Up/Down without PTY leak"). Pure
    /// index math lives in ``TaskNavigation`` (ArgusKit) so it has real
    /// tests; this is just the ordered-id-list wiring. Distinct from
    /// ``jumpToNextNeedsInput()``: this is a plain adjacent move over
    /// EVERY task, not one filtered to tasks needing input, and — per
    /// ``TaskNavigation``'s own doc comment — it CLAMPS at the rail's ends
    /// rather than wrapping. Reaching either end (or an empty rail) is a
    /// no-op.
    func selectPreviousTask() {
        let orderedIDs = tasksByFolder.flatMap { $0.tasks.map(\.id) }
        guard let next = TaskNavigation.adjacent(orderedIDs: orderedIDs,
                                                  current: selectedTaskID,
                                                  direction: .previous) else { return }
        selectedTaskID = next
    }

    /// See ``selectPreviousTask()`` — the Cmd+Down half of the same
    /// shortcut pair, stepping toward the bottom of the rail instead.
    func selectNextTask() {
        let orderedIDs = tasksByFolder.flatMap { $0.tasks.map(\.id) }
        guard let next = TaskNavigation.adjacent(orderedIDs: orderedIDs,
                                                  current: selectedTaskID,
                                                  direction: .next) else { return }
        selectedTaskID = next
    }

    /// The Terminal tab's Cmd+Left/Cmd+Right "pane focus" shortcut
    /// (spec.md "Switch pane focus via Cmd+Left/Right without PTY leak").
    /// The mac app has no split-pane terminal view to move focus between
    /// (design.md's Stage 5 resolution notes `TerminalController.swift`
    /// has zero "pane" concept) — cycling the detail pane's active tab
    /// through the same Terminal→Diff→Files→Info order Stage 2's Cmd+1-4
    /// direct-select shortcuts already use is the closest structural
    /// analog. Wraps at both ends (``CyclicSelection``, ArgusKit); `true`
    /// cycles forward (Cmd+Right), `false` backward (Cmd+Left).
    func cycleDetailTab(forward: Bool) {
        activeDetailTab = CyclicSelection.step(Self.detailTabCycleOrder, current: activeDetailTab, forward: forward)
    }

    private static let detailTabCycleOrder: [DetailTab] = [.terminal, .diff, .files, .info]

    /// `POST /api/maintenance/prune-completed` — removes every completed
    /// task, its worktree, and branch, and sweeps orphaned worktree
    /// directories. Mirrors the TUI's Ctrl+R "Prune completed tasks" action,
    /// reached here from the toolbar's overflow menu after a confirmation
    /// dialog (``AppState/PendingConfirmation/pruneCompleted``). Same
    /// success/failure shape as ``perform(_:label:_:)`` (refresh on success,
    /// ``showActionError`` on failure) but not task-scoped, so it can't reuse
    /// that helper directly.
    func pruneCompleted() async {
        guard let client else { return }
        do {
            _ = try await client.pruneCompleted()
            await refreshOnce()
        } catch {
            showActionError("Failed to prune completed tasks: \(Self.describe(error))")
        }
    }

    // MARK: - Claude session switcher

    /// Best-effort client-side mirror of the daemon's own
    /// `!IsCodexBackend && !IsPiBackend && !IsOpencodeBackend` guard
    /// (`internal/agent/agent.go`) that gates `/claude-sessions` /
    /// `/claude-session` — an unset, `"claude"`, or custom backend name is
    /// treated as Claude-eligible, and only the three known non-Claude
    /// backend names are excluded. Lets the toolbar button hide itself for
    /// an obviously-ineligible task without a doomed round trip; the
    /// daemon's own 400 (handled in ``openClaudeSessionPicker(for:)``) stays
    /// the authoritative check for anything this heuristic can't see (e.g. a
    /// custom backend *name* that happens to run a non-Claude command).
    static func isLikelyClaudeBacked(_ task: ArgusTask) -> Bool {
        guard let backend = task.backend?.lowercased(), !backend.isEmpty else { return true }
        return !["codex", "pi", "opencode"].contains(backend)
    }

    /// Fetches the task's Claude sessions and opens the picker sheet on
    /// success — the sheet is never presented empty/broken. A 400 (the
    /// task's backend isn't Claude) or any other failure surfaces via
    /// ``ActionErrorBanner`` instead.
    func openClaudeSessionPicker(for task: ArgusTask) async {
        guard let client else { return }
        do {
            let (sessions, currentID) = try await client.claudeSessions(taskID: task.id)
            claudeSessionPicker = ClaudeSessionPickerState(task: task, sessions: sessions,
                                                            currentSessionID: currentID)
        } catch {
            if let argusError = error as? ArgusError, argusError.isBadRequest {
                showActionError("\"\(task.name)\" isn't a Claude-backed task — session switching isn't available.")
            } else {
                showActionError("Failed to load Claude sessions for \"\(task.name)\": \(Self.describe(error))")
            }
        }
    }

    /// Dismisses the Claude session picker sheet (its Cancel button, or after
    /// a successful switch).
    func dismissClaudeSessionPicker() {
        claudeSessionPicker = nil
    }

    /// Switches the task to a different Claude session. Both the
    /// `"switched"` and `"unchanged"` responses from
    /// ``ArgusClient/switchClaudeSession(taskID:sessionID:)`` are successful
    /// outcomes — either way the sheet dismisses with no further client
    /// action: a `"switched"` response means the daemon already stopped and
    /// restarted the task's live PTY, resuming with the new session, and the
    /// Terminal tab's existing SSE reconnect (`TerminalStreamSession`'s
    /// `exit {"rerendering":true}` path — the same mechanism an ordinary
    /// resize-triggered kick-restart already relies on) picks up the fresh
    /// session's output once the daemon's stream emits that exit. On
    /// failure the sheet stays open (so the user can pick something else or
    /// cancel) and the error surfaces via ``ActionErrorBanner``, matching
    /// every other task action's failure UX.
    func selectClaudeSession(_ session: ClaudeSession, for task: ArgusTask) async {
        guard let client else { return }
        do {
            _ = try await client.switchClaudeSession(taskID: task.id, sessionID: session.id)
            claudeSessionPicker = nil
        } catch {
            showActionError("Failed to switch Claude session for \"\(task.name)\": \(Self.describe(error))")
        }
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

    // MARK: - Event stream (live task-list sync)

    /// Kicks the event-stream loop by driving the session's initial open action.
    /// The reconnect path is self-scheduling via ``consumeEvents(since:)`` →
    /// ``applyEventActions(_:)`` (mirrors ``TerminalController``'s pattern).
    private func startEventStream() {
        applyEventActions(eventSession.start())
    }

    /// Opens `/api/events/stream?since=<cursor>` after an optional backoff delay,
    /// then consumes it. Cancel-and-replace so only one attempt is live.
    private func openEventStream(since: Int64, after delay: Duration?) {
        eventStreamTask?.cancel()
        eventStreamTask = _Concurrency.Task { [weak self] in
            if let delay { try? await _Concurrency.Task.sleep(for: delay) }
            if _Concurrency.Task.isCancelled { return }
            await self?.consumeEvents(since: since)
        }
    }

    /// One stream attempt with subscribe-before-snapshot fencing:
    /// 1. Mark this dial in-flight (so a failure re-enters the backoff path).
    /// 2. Open the stream FIRST and buffer decoded events.
    /// 3. Concurrently fetch the `/api/tasks` snapshot; when it lands, apply it
    ///    as the baseline and drain the buffered events on top.
    /// 4. On stream end/failure, schedule a reconnect via the session policy.
    ///
    /// The ordering (stream before snapshot) closes the gap where an event
    /// committed between a snapshot and a later subscribe would be lost — see
    /// `internal/api/events_stream.go` + `gotchas/events.md`.
    private func consumeEvents(since: Int64) async {
        guard let client else { return }
        eventSession.streamOpening()
        eventBuffering = true
        eventBuffer.removeAll()
        eventBufferNeedsResnapshot = false

        // Snapshot runs concurrently on the main actor; it interleaves with the
        // stream loop at each network suspension point (no data race — single
        // actor). When it lands it flips buffering off and drains the buffer.
        let snap = _Concurrency.Task { @MainActor [weak self] in
            guard let self else { return }
            await self.refreshOnce()
            self.flushEventBuffer()
        }

        do {
            for try await item in client.eventsStream(since: since) {
                if _Concurrency.Task.isCancelled { break }
                switch item {
                case .connected:
                    // Response validated before any frame: mark live + reset the
                    // backoff so a quiet events channel isn't stuck reconnecting.
                    eventSession.streamConnected()
                case .event(let raw):
                    applyEventActions(eventSession.handle(raw))
                }
            }
        } catch is CancellationError {
            snap.cancel()
            return
        } catch {
            Self.log.error("[events] stream error: \(String(describing: error), privacy: .public)")
        }
        snap.cancel()
        // If the stream died before the snapshot flipped the flag, don't strand
        // buffered events in limbo — but they're moot on a reconnect resnapshot.
        eventBuffering = false
        if let action = eventSession.streamClosed() {
            Self.log.info("[events] stream closed; reconnecting from cursor=\(self.eventSession.cursor)")
            applyEventActions([action])
        }
    }

    /// Executes the session's actions. Stream open/reconnect schedule the next
    /// attempt; resnapshot and apply are deferred while buffering.
    private func applyEventActions(_ actions: [EventsStreamSession.Action]) {
        for action in actions {
            switch action {
            case .openStream(let since):
                openEventStream(since: since, after: nil)
            case .reconnect(let since, let after):
                openEventStream(since: since, after: after)
            case .resnapshot:
                if eventBuffering {
                    eventBufferNeedsResnapshot = true
                } else {
                    scheduleSnapshot()
                }
            case .apply(let event):
                if eventBuffering {
                    eventBuffer.append(event)
                } else {
                    applyDaemonEvent(event)
                }
            }
        }
    }

    /// Applies the baseline snapshot then drains the buffered events on top (in
    /// id order — last-per-task wins, converging with the snapshot). Buffered
    /// events do NOT notify (they may be historical ring replay); only live
    /// edges do. A resnapshot requested during buffering collapses to one final
    /// snapshot here.
    private func flushEventBuffer() {
        guard eventBuffering else { return }
        eventBuffering = false
        let buffered = eventBuffer
        eventBuffer.removeAll()
        for event in buffered {
            applyDaemonEvent(event, notify: false)
        }
        if eventBufferNeedsResnapshot {
            eventBufferNeedsResnapshot = false
            scheduleSnapshot()
        }
    }

    /// Fires a fresh `/api/tasks` snapshot (resync / created / forked path),
    /// cancel-and-replace so bursts collapse to the latest.
    private func scheduleSnapshot() {
        snapshotTask?.cancel()
        snapshotTask = _Concurrency.Task { [weak self] in await self?.refreshOnce() }
    }

    // MARK: - Incremental event application

    /// Applies one decoded ``DaemonEvent`` to the task list + OS-integration
    /// surfaces. `notify` gates native notifications (false while draining
    /// historical buffered events).
    private func applyDaemonEvent(_ event: DaemonEvent, notify: Bool = true) {
        switch event {
        case .taskRenamed(let id, _, let to):
            patchTask(id: id) { $0.with(name: to) }
        case .taskStatusChanged(let id, _, let to):
            patchTask(id: id) { $0.with(status: to) }
        case .taskCompleted(let id):
            patchTask(id: id) { $0.with(status: TaskStatus.complete.rawValue) }
        case .taskArchived(let id):
            patchTask(id: id) { $0.with(archived: true) }
            clearNeedsInput(id)
            pinnedTaskIDs.remove(id) // archiving clears pinned server-side too
            if selectedTaskID == id { selectedTaskID = nil }
        case .taskDeleted(let id):
            tasks.removeAll { $0.id == id }
            pruneTerminalControllers(keeping: Set(tasks.map(\.id)))
            clearNeedsInput(id)
            pinnedTaskIDs.remove(id)
            if selectedTaskID == id { selectedTaskID = nil }
        case .sessionNeedsInput(let id, let needs):
            if needs { addNeedsInput(id, notify: notify) } else { clearNeedsInput(id) }
        case .sessionIdle(let id):
            patchTask(id: id) { $0.with(idle: true) }
            if notify { maybeNotifyIdle(id) }
        case .sessionStarted(let id, _, _):
            // A fresh/resumed session is working, not blocked — clear the flag.
            patchTask(id: id) { $0.with(idle: false) }
            clearNeedsInput(id)
        case .sessionExited(let id, _, _):
            clearNeedsInput(id)
        case .sessionFocus, .messageSent, .messageAcked:
            break // no task-list impact
        case .taskCreated, .taskForked, .resync, .unknown:
            break // resnapshot-class — never delivered as .apply
        }
    }

    /// Replaces a task in-place via a value copy (``ArgusTask`` fields are `let`).
    private func patchTask(id: String, _ transform: (ArgusTask) -> ArgusTask) {
        guard let idx = tasks.firstIndex(where: { $0.id == id }) else { return }
        tasks[idx] = transform(tasks[idx])
    }

    // MARK: - Needs-input tracking (badge / marker / notifications)

    private func addNeedsInput(_ id: String, notify: Bool) {
        patchTask(id: id) { $0.with(needsInput: true) }
        needsInputTaskIDs.insert(id)
        updateBadge()
        guard notify, notifyOnNeedsInput, let task = tasks.first(where: { $0.id == id }) else { return }
        notificationManager.notifyNeedsInput(taskID: id, taskName: task.name)
    }

    private func clearNeedsInput(_ id: String) {
        patchTask(id: id) { $0.with(needsInput: false) }
        if needsInputTaskIDs.remove(id) != nil { updateBadge() }
        notificationManager.clearNeedsInput(taskID: id)
    }

    private func maybeNotifyIdle(_ id: String) {
        guard notifyOnIdle, !NSApp.isActive,
              let task = tasks.first(where: { $0.id == id }) else { return }
        notificationManager.notifyIdle(taskID: id, taskName: task.name)
    }

    /// Reflects the needs-input count on the Dock icon.
    private func updateBadge() {
        let count = needsInputTaskIDs.count
        NSApp.dockTile.badgeLabel = count > 0 ? String(count) : nil
    }

    private func startFallbackPoll() {
        fallbackPollTask = _Concurrency.Task { [weak self] in
            guard let self else { return }
            while !_Concurrency.Task.isCancelled {
                try? await _Concurrency.Task.sleep(for: self.fallbackPollInterval)
                if _Concurrency.Task.isCancelled { break }
                await self.refreshOnce()
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

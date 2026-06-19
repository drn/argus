package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"golang.org/x/term"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/claudesession"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	dclient "github.com/drn/argus/internal/daemon/client"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/launchagent"
	"github.com/drn/argus/internal/macapps"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/scheduler"
	"github.com/drn/argus/internal/tui/gitpanel"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/keyenc"
	"github.com/drn/argus/internal/tui/modal"
	"github.com/drn/argus/internal/tui/store"
	"github.com/drn/argus/internal/tui/taskview"
	"github.com/drn/argus/internal/tui/terminal"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

// recentStartGrace is the time window after startSession during which a task
// is immune from reconciliation. Protects against false completion when
// ListSessions returns stale data after a daemon restart cascade.
const recentStartGrace = 5 * time.Second

// viewMode identifies the active view.
type viewMode int

const (
	modeTaskList viewMode = iota
	modeAgent
	modeNewTask
	modeConfirmDelete
	modeProjectForm
	modeScheduleForm
	modeForkTask
	modeRenameTask
	modeLinkPicker
	modeFuzzyLinkPicker
	modeSessionPicker
	modeTaskSwitcher
	modeQuickAdd
	modeConfirmDeleteProject
	modeConfirmPrune // Ctrl+R "prune completed tasks" caution gate
	modeRestartDaemonPrompt
	modeConfirmRestartSupervisor // Settings → "Restart Session Supervisor" caution gate
	modeAppleEventsPicker
	modeHelp
	modeErrorModal
	modePluginView
	modeHeraInput      // Hera-view rename / spawn-prompt input modal
	modeHeraConfirm    // Hera-view archive-of-live / delete confirmation modal
	modeHeraOrchPicker // Hera-view `J` adopt/reparent orchestrator picker
)

// agentFocus tracks which panel has focus in the agent view.
type agentFocus int

const (
	focusTerminal agentFocus = iota
	focusFiles
)

// App is the top-level tview application shell.
type App struct {
	tapp   *tview.Application
	db     store.Store
	runner agent.SessionProvider
	mu     sync.Mutex

	// Sub-views
	header       *widget.Header
	statusbar    *widget.StatusBar
	tasklist     *taskview.TaskListView
	taskGitPanel *gitpanel.GitPanel // git status for selected task (task list center-top)
	taskPreview  *TaskPreviewPanel
	taskDetail   *taskview.TaskDetailPanel
	agentPane    *terminal.TerminalPane
	agentHeader  *widget.AgentHeader
	gitPanel     *gitpanel.GitPanel // git status for agent view (left panel)
	filePanel    *gitpanel.FilePanel
	attentionBar *widget.AttentionBar // sits above gitPanel in agent view
	agentLeftCol *tview.Flex          // vertical flex that owns attentionBar + gitPanel
	agentPanels  *tview.Flex          // horizontal flex: left col | agent pane | file panel

	// Tabs
	settings     *SettingsView
	settingsPage *SettingsPage

	// Hera tab (created at construction; rail rebuilt by refreshHera on tab
	// entry and on the tick while the Hera tab is active). The second tab is
	// always the native Hera view. heraPage may render a remote-mode
	// "unavailable" banner when a.db is not a local *db.DB.
	heraPage *hera.HeraPage

	// Hera-view mutation layer (M6c) + its modal state. heraOps is nil in
	// remote mode (no local *db.DB), in which case the Hera tab's mutation keys
	// are inert. heraInputModal/heraConfirmModal are created on demand; the
	// pending closures capture the selected (role,orchestrator) target so the
	// submit/confirm path acts on the right binding (multi-binding isolation).
	heraOps          *hera.Ops
	heraInputModal   *RenameTaskForm
	heraConfirmModal *modal.ConfirmModal
	heraInputSubmit  func(string) // called with the field value on input-modal submit
	heraConfirmDo    func()       // called on confirm-modal accept

	// `J` adopt/reparent layer + its orchestrator picker. heraAdoptOps is nil in
	// remote mode (no local *db.DB) so the `J` key is inert. heraOrchPicker is
	// created on demand; heraOrchPickerPick captures the chosen-orchestrator
	// callback (adopt freelancer vs reparent coordinator).
	heraAdoptOps       *hera.AdoptOps
	heraOrchPicker     *OrchPickerModal
	heraOrchPickerPick func(*db.HeraOrchestrator)

	// New task form (created on demand)
	newTaskForm *NewTaskForm
	// newTaskOnDone, when non-nil, OVERRIDES handleNewTaskKey's default
	// create-and-start path: it is called with the assembled task + resolved
	// project on submit (the form is already closed). The Hera rail's `w`/`n`
	// keys use it to spawn a born-bound worker / new root coordinator from the
	// SAME modal as the new-argus-task popup. nil = the default Tasks-tab path.
	newTaskOnDone func(task *model.Task, project string)
	// newTaskReturnPage is the page closeNewTaskForm switches back to (and which
	// primitive it focuses). Defaults to "tasks"; the Hera rail sets "hera" so the
	// shared modal returns to the Hera tab on submit/cancel.
	newTaskReturnPage string

	// Confirm delete modal (created on demand)
	confirmDeleteModal        *modal.ConfirmDeleteModal
	confirmDeleteProjectModal *modal.ConfirmDeleteProjectModal

	// Confirm prune modal (created on demand when Ctrl+R is pressed with at
	// least one completed task). Pruning deletes every completed task plus
	// their worktrees and branches, so it is gated behind a y/N confirmation.
	confirmPruneModal *modal.ConfirmModal

	// Help overlay (created on demand)
	helpModal    *modal.HelpModal
	helpPrevPage string

	// Error modal (created on demand to surface failed actions prominently)
	errorModal *modal.ErrorModal

	// Restart-daemon prompt (created on demand when binary mtime mismatch
	// is detected at startup). daemonStale is set by main before Run() and
	// read once inside Run() — no concurrent access, no lock needed.
	restartDaemonModal *modal.RestartDaemonModal
	daemonStale        bool

	// Session-supervisor restart caution gate (Settings → System). Created on
	// demand when the user activates the row; the bounce SIGHUPs every agent,
	// so it is always confirmed before running.
	restartSupervisorModal *modal.ConfirmModal

	// Link picker modals (created on demand)
	linkPickerModal      *LinkPickerModal
	linkPickerPrevPage   string
	fuzzyLinkPickerModal *FuzzyLinkPickerModal
	sessionPickerModal   *SessionPickerModal
	taskSwitcherModal    *TaskSwitcherModal

	// Fork task modal (created on demand)
	forkModal *ForkTaskModal

	// Rename task modal (created on demand)
	renameModal *RenameTaskForm
	renameTask  *model.Task

	// Settings forms (created on demand)
	projectForm       *ProjectForm
	scheduleForm      *ScheduleForm
	quickAddForm      *QuickAddForm
	appleEventsPicker *AppleEventsPickerModal
	// appleEventsPickerProject is the project name the picker is currently
	// editing — set on open, read on save so we know which DB row to update.
	appleEventsPickerProject string
	appleEventsPickerOrig    config.Project // unedited snapshot for save
	// macAppsCache is the cached scriptable-app list. Populated on first
	// picker open and reused for subsequent opens to keep the scan
	// (~400ms for /Applications + /System/Applications) off the UI thread.
	macAppsCache []macapps.App

	// Layout containers
	root      *tview.Flex
	taskPage  *taskview.TaskPage
	agentPage *tview.Flex
	pages     *tview.Pages

	// State
	mode               viewMode
	agentFocus         agentFocus
	agentZen           bool // single-pane (zoom) mode: side panels collapsed to 0 width
	agentState         agentview.State
	daemonConnected    bool
	tasks              []*model.Task
	runningIDs         []string
	idleIDs            []string
	worktreeDir        string // resolved worktree dir for current agent view task
	lastGitRefresh     time.Time
	lastTaskGitRefresh time.Time
	lastPreviewTW      uint64 // TotalWritten when preview was last refreshed
	lastPreviewTaskID  string // task ID for the cached TotalWritten
	lastPreviewLogSize int64  // log file size when dead-session preview was last refreshed
	// Idle-unvisited tracking (for visual InReview promotion)
	idleUnvisited    map[string]bool // task IDs idle since user last opened their agent view
	viewedWhileAgent map[string]bool // tasks viewed in agent view; suppresses idleUnvisited re-add
	needsInputIDs    []string        // task IDs detected as blocked on a user prompt this tick

	// Daemon health
	daemonFailures    int
	daemonRestarting  bool
	lastDaemonRestart time.Time // cooldown: minimum 30s between restart attempts
	daemonClient      *dclient.Client
	restartedClient   *dclient.Client // set after daemon restart

	// restartDaemonFn is the function invoked by every code path that wants
	// to restart the daemon. Defaults to a.restartDaemon. Tests override it
	// to avoid forking the test binary as a fake daemon (see ErrTestBinary
	// in internal/daemon/client/client.go for the failure mode).
	restartDaemonFn func()

	// restartSupervisorFn is the function invoked to bounce the session-
	// supervisor. Defaults to a.restartSupervisor. Tests override it to avoid
	// forking the test binary as a fake supervisor (same ErrTestBinary failure
	// mode as restartDaemonFn).
	restartSupervisorFn func()

	// Tick control
	tickDone            chan struct{}
	tickCallbackPending atomic.Bool          // debounce: skip enqueue if prior callback hasn't run
	startGen            atomic.Uint64        // double-bumped by startSession (before+after Start RPC); tick captures before its RPC and skips reconciliation on mismatch
	recentStarts        map[string]time.Time // task ID → time of last startSession; grace period prevents false reconciliation

	// pendingRerenderRestart marks tasks whose live session was killed by the
	// auto-rerender path (started with a too-narrow PTY due to the
	// computePTYSize bug). When `handleSessionExitUI` sees an entry in this
	// map, it immediately restarts the session via `--session-id` so Claude
	// re-renders the conversation history at the current (wider) PTY.
	pendingRerenderRestart map[string]bool

	// lastAttachCols caches the panel cols at which we most recently evaluated
	// the rerender predicate for each task. The gate is "panel size unchanged
	// since the last attach" — if the user closes the agent view and reopens
	// it without resizing the terminal, the predicate would otherwise re-fire
	// and (when the panel is meaningfully wider/narrower than the session's
	// initialCols) kill an idle session. That destroys any in-flight
	// interactive UI Claude is rendering (notably AskUserQuestion overlays)
	// because the restart via --session-id rehydrates the conversation but
	// not the ephemeral modal. Storing the cols per task lets reopen-at-same
	// -size short-circuit, while genuine resizes still fall through.
	lastAttachCols map[string]uint16

	// Worktree root for orphan sweep (default: ~/.argus/worktrees/).
	// Overridden in tests to avoid scanning real worktrees.
	wtRoot string

	// Cached agent-staged clipboard text for the currently-active agent-view
	// task. Polled from the daemon on each tick; used to (a) gate the ctrl+y
	// hotkey so PTY pass-through wins when nothing is staged, (b) toggle the
	// agentHeader hint. Empty string when nothing is staged.
	clipboardPending     string
	clipboardPendingTask string // task ID the cached payload belongs to

	// OS clipboard writer. `New()` always populates it with `pbcopyWriter`,
	// which shells out to the real `pbcopy` and clobbers the developer's
	// clipboard. Any test whose code path can reach `copyToClipboard` (via
	// `OnCopyPrompt`, ctrl+y → `copyStagedClipboard`, or a direct call) MUST
	// overwrite this field with a no-op (`func(string) error { return nil }`)
	// immediately after `New()` — otherwise the test writes its sample text to
	// the host's real clipboard. There is no nil-fallback or in-test auto-stub;
	// if you bypass `New()` via a zero-value struct literal, calling the field
	// will nil-panic inside `copyToClipboard`'s goroutine — the panic location
	// in the stack trace points back here.
	clipboardWriter func(text string) error

	// Screen wrapper. lazyScreen is a passthrough today (see its doc for
	// history); the named type is retained so smoke tests can inject a
	// SimulationScreen through the same indirection production uses.
	screen *lazyScreen

	// lastScreenW/H track the most recently observed terminal size so
	// `afterDraw` can detect a resize and call `screen.Sync()` once.
	// tview's draw cycle (Clear+root.Draw+Show) repaints into the new
	// dimensions but doesn't fully overwrite stale cells the terminal
	// still holds from the prior size — visible as stacked status bars
	// at multiple y-positions after a window resize. Sync emits CSI 2J +
	// every cell, clearing the stale state. Resize is rare and user-
	// driven, so the one CSI 2J flash per resize is the right tradeoff.
	lastScreenW int
	lastScreenH int

	// Plugin-registered top-level views. See plugin_views.go for the
	// mount/activate/deactivate lifecycle. pluginConnFactory is overridable
	// so smoke tests can replace the real WebSocket dialer with an in-
	// process stub.
	pluginMounts      []*pluginViewMount
	pluginHotkeys     map[tcell.Key]*pluginViewMount
	activePlugin      *pluginViewMount
	pluginConnFactory pluginConnectorFactory
	// pluginHelpRequested is set when the active plugin sends a help control
	// frame. Retained as an observable seam (smoke tests assert it flips);
	// requestPluginHelp also pops the overlay. Touched only on the tview
	// goroutine.
	pluginHelpRequested bool
	// pluginHelpVisible is true while the plugin-triggered help overlay is on
	// screen. While visible, the modePluginView branch of handleGlobalKey
	// consumes exactly the NEXT key to dismiss the overlay (the overlay does
	// not capture the keyboard beyond dismissal); otherwise argus fully
	// surrenders. Cleared on dismiss and on deactivate. Touched only on the
	// tview goroutine.
	pluginHelpVisible bool

	// lastCtrlQ timestamps the most recent Ctrl+Q seen while a plugin has the
	// ball. A second Ctrl+Q within pluginFailsafeWindow force-returns to argus
	// (the failsafe). Reset to zero in activatePluginView so a stale timestamp
	// never carries between views. Touched only on the tview main goroutine via
	// SetInputCapture → handleGlobalKey, so it needs no mutex.
	lastCtrlQ time.Time
	// nowFn is the clock used by the failsafe window check. Defaults to
	// time.Now; tests override it to make the timing deterministic.
	nowFn func() time.Time
	// resumeResizeDelay is how long after a successful plugin-view reconnect to
	// re-send the resize envelope. A plugin daemon that restarts warm can render
	// its first frame within tens of ms of the new WebSocket connecting — before
	// it has applied argus's initial resize — and then it only repaints at the
	// right size on the NEXT resize it processes. Re-sending once after a short
	// delay (when the plugin has settled) mimics that healing resize so the
	// resumed view isn't stuck rendering tiny. Tests shrink this to keep fast.
	resumeResizeDelay time.Duration

	// Stream-section connectors. Keyed by (scope, title). Created on
	// SettingsView.OnStreamFocus, closed on OnStreamBlur. Reuses the same
	// connector factory as plugin views so smoke tests can stub.
	streamConns   map[pluginStreamKey]pluginConnector
	streamConnsMu sync.Mutex

	// focusTracker is the daemon-level human-focus registry. When non-nil,
	// the App signals SetFocused on agent-view enter/exit so the reliable
	// pane-delivery service can gate auto-submits. Optional: nil is safe.
	focusTracker focusTrackerIface
}

// pluginStreamKey identifies an open stream-section connector. Matches the
// SettingsView's pluginKey shape but lives in package tui so it doesn't
// re-export internal/tui's pluginKey.
type pluginStreamKey struct {
	scope string
	title string
}

// focusTrackerIface is the minimal interface the TUI needs from FocusTracker.
// Defined here so the notify package is not imported into the tui package
// (avoiding a dependency on internal/notify from internal/tui).
type focusTrackerIface interface {
	SetFocused(taskID string, focused bool)
}

// heraFinishStore is the local-DB surface the session-exit finish policy mirror
// needs (BUG-050). Satisfied by *db.DB via the shared db.RollHeraWorkerToReview
// helper (the same one the daemon exit hook and the hera_status("done") MCP arm
// call, so all triggers stay in lockstep). In --remote mode a.db is an
// *apistore.Store that does NOT satisfy it, so the mirror in handleSessionExitUI
// is skipped — the remote daemon already applied the policy authoritatively in
// transitionTaskOnExit. Same local-only type-assertion pattern as the
// remoteTaskCreator / remoteForker daemon-admin guards elsewhere in this file.
type heraFinishStore interface {
	RollHeraWorkerToReview(taskID string) (bool, error)
}

// SetFocusTracker wires the daemon-level focus tracker into the TUI.
// Must be called before Run(). Optional — nil is safe.
func (a *App) SetFocusTracker(ft focusTrackerIface) {
	a.focusTracker = ft
}

// New creates the tui application shell.
//
// Stale-session reconciliation is owned by the runner-holder (daemon Serve, or
// in-process startup in cmd/argus/main.go) — they sweep InProgress → InReview
// before the TUI sees the DB. The TUI's tick reconciler is a backstop for an
// InProgress row whose session has since vanished from the daemon's list; it
// lands such rows in InReview (never Complete — it has no exit event and cannot
// know the agent finished cleanly; the authoritative Complete comes only from an
// observed clean exit via transitionTaskOnExit / handleSessionExitUI).
func New(database store.Store, runner agent.SessionProvider, daemonConnected bool) *App {
	// Use the terminal's default background instead of tview's hard-coded black.
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault

	app := &App{
		tapp:                   tview.NewApplication(),
		db:                     database,
		runner:                 runner,
		daemonConnected:        daemonConnected,
		agentState:             agentview.New(),
		tickDone:               make(chan struct{}),
		recentStarts:           make(map[string]time.Time),
		idleUnvisited:          make(map[string]bool),
		viewedWhileAgent:       make(map[string]bool),
		pendingRerenderRestart: make(map[string]bool),
		lastAttachCols:         make(map[string]uint16),
		wtRoot:                 filepath.Join(db.DataDir(), "worktrees"),
		clipboardWriter:        pbcopyWriter,
		nowFn:                  time.Now,
		resumeResizeDelay:      300 * time.Millisecond,
	}
	if dc, ok := runner.(*dclient.Client); ok {
		app.daemonClient = dc
	}
	app.restartDaemonFn = app.restartDaemon
	app.restartSupervisorFn = app.restartSupervisor

	app.settings = NewSettingsView(database)
	app.settings.SetDaemonConnected(daemonConnected)
	// Remote mode (a.db is not the local *db.DB) hides daemon-admin actions
	// that manage the local OS install.
	if _, isLocal := database.(*db.DB); !isLocal {
		app.settings.SetRemote(true)
	}
	app.settings.OnRestartDaemon = func() {
		app.mu.Lock()
		app.daemonRestarting = true
		app.lastDaemonRestart = time.Now()
		app.mu.Unlock()
		go app.restartDaemonFn()
	}
	app.settings.OnRestartSupervisor = func() { app.openRestartSupervisorPrompt() }
	app.settings.OnUpdateArgus = func() { go app.updateArgus() }
	app.settings.OnToggleAutoStart = func(installed bool) { go app.toggleAutoStart(installed) }
	app.settings.OnNewProject = func() { app.openProjectForm(false, "", config.Project{}) }
	app.settings.OnEditProject = func(name string, p config.Project) { app.openProjectForm(true, name, p) }
	app.settings.OnEditProjectAppleEvents = func(name string, p config.Project) {
		app.openAppleEventsPicker(name, p)
	}
	app.settings.OnDeleteProject = func(name string) { app.deleteProject(name) }
	app.settings.OnQuickAdd = func() { app.openQuickAddForm() }
	app.settings.OnNewSchedule = func() { app.openScheduleForm(nil) }
	app.settings.OnEditSchedule = func(s *model.ScheduledTask) { app.openScheduleForm(s) }
	app.settings.OnDeleteSchedule = func(id string) { app.deleteSchedule(id) }
	app.settings.OnRunSchedule = func(id string) { app.runScheduleNow(id) }
	app.settings.OnBranchChange = func() { app.forceRedraw("settings branch changed") }
	app.settings.OnHeraToggle = func(enabled bool) {
		// The second tab is always the native Hera view now, so the toggle no
		// longer relabels or re-routes it; it only flips cfg.Hera.Enabled, which
		// gates daemon-side behaviour (MCP native-vs-plugin tools, the auto-adopt
		// watcher) on the next daemon start.
		uxlog.Log("[hera-view] hera.enabled toggled to %v", enabled)
	}
	app.settings.OnStreamFocus = app.openStreamSection
	app.settings.OnStreamBlur = app.closeStreamSection
	app.settings.SetPluginSubmit(app.submitPluginSection)
	app.settingsPage = NewSettingsPage(app.settings)

	cfg := database.Config()
	widget.SetActiveSpinner(cfg.UI.SpinnerStyle)

	app.buildUI()
	app.refreshTasks()

	return app
}

// buildUI constructs the tview widget tree.
func (a *App) buildUI() {
	a.header = widget.NewHeader()
	a.statusbar = widget.NewStatusBar()

	a.tasklist = taskview.NewTaskListView()
	a.tasklist.OnSelect = func(task *model.Task) { a.onTaskSelect(task, true) }
	a.tasklist.OnNew = a.onNewTask
	a.tasklist.OnCursorChange = a.onTaskCursorChange
	a.tasklist.OnLayoutChange = func() { a.forceRedraw("tasklist rows changed") }
	a.tasklist.OnFilterToggle = func() { a.forceRedraw("tasklist filter toggled") }
	a.tasklist.OnHeraManagedToggle = func(hidden bool) {
		uxlog.Log("[hera-view] tasklist hide-hera-managed toggled: hidden=%v", hidden)
		a.forceRedraw("tasklist hera-managed toggled")
	}
	a.tasklist.OnStatusChange = func(t *model.Task) {
		uxlog.Log("[tui] manual status change: task %s (%s) → %s", t.ID, t.Name, t.Status)
		// Route through SetStatus (partial column update) not Update: the
		// task-list struct is a cached snapshot that may carry a stale name —
		// a background autoname (Haiku) rename can land in the DB between
		// refreshes. A full-row Update here would silently revert that rename.
		a.db.SetStatus(t.ID, t.Status) //nolint:errcheck // best-effort; display is source of truth
		a.refreshTasksAsync()
	}
	a.tasklist.OnArchive = func(t *model.Task) {
		uxlog.Log("[tui] archive toggle: task %s (%s) archived=%v", t.ID, t.Name, t.Archived)
		// Route through SetArchived (partial column update) so:
		//  - local mode: *db.DB.SetArchived also runs DeleteMessagesForTask
		//    in the same transaction.
		//  - remote mode: apistore.SetArchived hits /api/tasks/{id}/archive
		//    on the server, which triggers handleArchiveTask's setArchive →
		//    DeleteMessagesForTask cleanup.
		// Either path keeps the archived-rows / messages invariant honored.
		// Going through Update + DeleteMessagesForTask here would silently
		// orphan messages in remote mode because apistore can't expose the
		// DeleteMessagesForTask endpoint and PUT /api/tasks/{id}/raw doesn't
		// trigger server-side cleanup.
		a.db.SetArchived(t.ID, t.Archived) //nolint:errcheck // best-effort; display is source of truth
		a.refreshTasksAsync()
	}
	a.tasklist.OnPin = func(t *model.Task) {
		uxlog.Log("[tui] pin toggle: task %s (%s) pinned=%v", t.ID, t.Name, t.Pinned)
		// Route through SetPinned (partial column update) not Update for the
		// same reason as OnStatusChange above: the cached task struct may hold
		// a stale name that a background autoname rename has already superseded
		// in the DB, and a full-row Update would clobber it. SetPinned also
		// preserves the pinned/archived mutual-exclusivity invariant.
		a.db.SetPinned(t.ID, t.Pinned) //nolint:errcheck // best-effort; display is source of truth
		a.refreshTasksAsync()
	}
	a.tasklist.OnRename = func(t *model.Task) {
		a.openRenameModal(t)
	}
	a.tasklist.OnCopyPrompt = func(t *model.Task) {
		taskID, taskName := t.ID, t.Name
		a.copyToClipboard(t.Prompt, "Prompt copied", func() {
			uxlog.Log("[tui] copied prompt to clipboard: task %s (%s)", taskID, taskName)
		})
	}

	a.taskGitPanel = gitpanel.NewGitPanel()
	a.taskGitPanel.OnBranchChange = func() { a.forceRedraw("task git panel branch changed") }
	a.taskPreview = NewTaskPreviewPanel()
	a.taskPreview.OnBranchChange = func() { a.forceRedraw("task preview branch changed") }
	a.taskDetail = taskview.NewTaskDetailPanel()
	a.taskDetail.OnBranchChange = func() { a.forceRedraw("task detail branch changed") }

	a.gitPanel = gitpanel.NewGitPanel()
	a.gitPanel.OnBranchChange = func() { a.forceRedraw("agent git panel branch changed") }
	a.filePanel = gitpanel.NewFilePanel()
	a.agentPane = terminal.NewTerminalPane()
	a.agentHeader = widget.NewAgentHeader()

	// Wire mouse click callbacks so clicking a panel switches agentFocus.
	a.filePanel.OnClick = func() {
		a.agentFocus = focusFiles
		a.updateFocusIndicators()
	}
	a.filePanel.OnLayoutChange = func() { a.forceRedraw("filepanel rows changed") }
	a.agentPane.OnClick = func() {
		a.agentFocus = focusTerminal
		a.updateFocusIndicators()
	}
	a.agentPane.OnBranchChange = func() { a.forceRedraw("agentpane branch changed") }
	a.agentPane.OnNeedRedraw = func() {
		a.tapp.QueueUpdateDraw(func() {})
	}

	// Task list page — three-panel layout: tasks | (git status + preview) | details
	// Center column is a vertical split: git status (30%, clamped 3-15 rows) on top,
	// preview (remaining) on bottom.
	taskCenter := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.taskGitPanel, 0, 3, false).
		AddItem(a.taskPreview, 0, 7, false)
	taskFlex := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(a.tasklist, 0, 1, true).
		AddItem(taskCenter, 0, 3, false).
		AddItem(a.taskDetail, 0, 1, false)
	a.taskPage = taskview.NewTaskPage(taskFlex, a.tasklist)

	// Agent page — header + three-panel layout. The left column stacks
	// the attention bar (visible only when other tasks need user attention)
	// above the git status panel; it starts at zero height and grows as
	// updateAttentionBar resizes it.
	a.attentionBar = widget.NewAttentionBar()
	a.agentLeftCol = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.attentionBar, 0, 0, false).
		AddItem(a.gitPanel, 0, 1, false)
	a.attentionBar.OnHeightChange = func() {
		a.agentLeftCol.ResizeItem(a.attentionBar, a.attentionBar.DesiredHeight(), 0)
		a.forceRedraw("attention bar height changed")
	}
	a.agentPanels = tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(a.agentLeftCol, 0, 1, false).
		AddItem(a.agentPane, 0, 3, false).
		AddItem(a.filePanel, 0, 1, false)
	// Apply the configured default agent-view layout at setup so the resting
	// agentZen flag + panel proportions match the configured default before the
	// first agent-view entry re-asserts them — otherwise the struct's zero-value
	// state (agentZen=false, 1:3:1 flex) would be a layout that's never actually
	// drawn when the user has zoom enabled. (agentFocus is already focusTerminal
	// here — its zero value — so setAgentZen's focus guard is a no-op at setup.)
	a.applyDefaultAgentZen()
	a.agentPage = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.agentHeader, 1, 0, false).
		AddItem(a.agentPanels, 0, 1, true)

	// Native Hera view (M6a). The rail reads the local *db.DB hera store; in
	// --remote mode (a.db is *apistore.Store, which has no hera methods) the
	// reader is nil and the page renders an "unavailable" banner — it never
	// breaks the remote build (see gotchas/remote-tui.md). This mirrors the
	// other local-only type-assert sites.
	var heraReader hera.HeraReader
	if d, ok := a.db.(*db.DB); ok {
		heraReader = d
	}
	a.heraPage = hera.NewHeraPage(heraReader)
	if d, ok := a.db.(*db.DB); ok {
		// Persist + restore the rail's fold/selection state across restarts
		// (BUG-002). Local-only: remote mode (apistore) has no config table seam,
		// so persistence stays off. Mirrors the heraReader / heraOps type-assert.
		a.heraPage.SetRailStateStore(d)
	}
	if heraReader != nil {
		// In-process runner feed seam (replaces Hera's proxy/ SSE fan-out). Read
		// a.runner at call time on the main thread — applySelection/refresh and
		// restartDaemon both run on the tview goroutine, so this never races.
		a.heraPage.SetSessionResolver(func(taskID string) agentview.TerminalAdapter {
			if taskID == "" {
				return nil
			}
			sess := a.runner.Get(taskID)
			if sess == nil {
				return nil
			}
			return sess
		})
	}
	// Focus-aware status bar: update heraFocus whenever the Hera focus machine
	// changes state (keyboard or mouse). The statusbar renders different hint
	// sets for rail vs pane focus so the operator always sees relevant keys.
	// The int passed matches hera.Focus iota: 0=rail, 1=coord, 2=agent.
	a.heraPage.OnFocusChange = func(f hera.Focus) {
		a.statusbar.SetHeraFocus(int(f))
	}

	// Wire the hera panes' redraw callbacks exactly like the main agent pane:
	// OnBranchChange is log-only (forceRedraw never Syncs), OnNeedRedraw bounces
	// a QueueUpdateDraw for async replay-rebuild completion.
	a.heraPage.CoordPane().OnBranchChange = func() { a.forceRedraw("hera coord pane branch changed") }
	a.heraPage.CoordPane().OnNeedRedraw = func() { a.tapp.QueueUpdateDraw(func() {}) }
	a.heraPage.AgentPane().OnBranchChange = func() { a.forceRedraw("hera agent pane branch changed") }
	a.heraPage.AgentPane().OnNeedRedraw = func() { a.tapp.QueueUpdateDraw(func() {}) }
	// The embedded Details-pane plan-DAG widget. OnBranchChange stays log-only
	// (never Sync) — the cursor-highlight ghost-prevention contract.
	a.heraPage.Plan().OnBranchChange = func() { a.forceRedraw("hera plan branch changed") }

	// M6c: thin mutation layer + rail keyset. Wired only in local mode (heraReader
	// is the same *db.DB; remote mode leaves heraOps nil and the callbacks unwired,
	// so the Hera tab's mutation keys are inert). Each callback owns the modal /
	// confirm / refresh orchestration; the DB writes live in hera.Ops, and worker
	// spawn reuses the shared agent.SpawnHeraWorker primitive.
	if d, ok := a.db.(*db.DB); ok {
		a.heraOps = hera.NewOps(d)
		a.heraAdoptOps = hera.NewAdoptOps(d)
		a.heraPage.OnSpawnWorker = a.heraSpawnWorker
		a.heraPage.OnRename = a.heraOpenRename
		a.heraPage.OnArchiveToggle = a.heraHide
		a.heraPage.OnPinToggle = a.heraPinToggle
		a.heraPage.OnStatusAdvance = func(sel hera.Selection) { a.heraStatusStep(sel, +1) }
		a.heraPage.OnStatusRevert = func(sel hera.Selection) { a.heraStatusStep(sel, -1) }
		a.heraPage.OnDelete = a.heraOpenDelete
		a.heraPage.OnReattach = a.heraReattach
		a.heraPage.OnAdopt = a.heraOpenAdopt
		// Rail key family: full new-task modal for w/n (BUG-005/006); the BUG-022
		// two-state EOL — `a` HIDE, `Ctrl+D` NUKE, `C` clear-this-coord's-archive
		// (retire `R` and the rail-wide `Ctrl+R` prune were removed).
		a.heraPage.OnNewCoordinator = a.heraNewCoordinator
		a.heraPage.OnClearArchive = a.heraClearArchive
		// ctrl+y copies the agent-staged clipboard payload for the focused pane's
		// task (resolved by the page from FocusedTerminalTaskID). Daemon-backed
		// only — the copy method no-ops gracefully under the in-process runner.
		a.heraPage.OnCopyClipboard = a.copyStagedClipboardForHeraPane

		// Plan-DAG render mode of the Details pane. The App wires only OnEnter
		// (jump to a leaf node's agent view); OnDrillIn (push the child
		// orchestrator's plan) is owned by the page itself — it needs the rail
		// bridge index and the in-package projection. The node/edge set is projected
		// in-process by the hera package (heraPlanNodesWithBridge over the rail
		// model), so there is no provider seam.
		a.heraPage.Plan().OnEnter = func(id string) { a.openAgentForTask(id) }
	}

	a.pages = tview.NewPages().
		AddPage("tasks", a.taskPage, true, true).
		AddPage("agent", a.agentPage, true, false).
		AddPage("hera", a.heraPage, true, false).
		AddPage("settings", a.settingsPage, true, false)
	a.loadPluginViews()
	// Every Pages mutation (AddPage / RemovePage / SwitchToPage / Show / Hide)
	// is a layout change that needs a tcell Sync to wipe ghost cells under
	// tmux/iTerm2. Wiring it once here covers every modal open/close, every
	// tab switch, and every agent view enter/exit — so individual callsites
	// don't have to remember. See gotchas/ui-threading.md.
	a.pages.SetChangedFunc(func() { a.forceRedraw("pages changed") })

	a.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 1, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.statusbar, 1, 0, false)

	// SetAfterDrawFunc is registered for two things: detect terminal resize
	// and emit one Sync per resize event (see afterDraw doc), and reconcile
	// the active plugin view's resize envelope after every draw (no Sync
	// involved). The full pendingSync/forceRedraw/OnContentChange scaffolding
	// from before the May 2026 cleanup is NOT here; only the resize-Sync case
	// remains because it's the one "repair screen damage" case tview's
	// Clear+Show diff cycle can't handle on its own (the prior size's
	// cells in the terminal aren't fully overwritten by the new size's
	// content). See gotchas/ui-threading.md for the post-mortem.
	a.tapp.SetAfterDrawFunc(a.afterDraw)
	a.tapp.SetInputCapture(a.handleGlobalKey)
	a.tapp.SetRoot(a.root, true)
}

// afterDraw detects terminal resize and Syncs once, then reconciles the
// active plugin view's resize envelope (no Sync — a plain WebSocket send,
// deduped against the last envelope delivered). It does NOT handle the
// deleted pendingSync/forceRedraw/OnContentChange triggers — those
// scaffolds are gone (see post-mortem in gotchas/ui-threading.md).
//
// Why resize needs Sync: tview's draw cycle (screen.Clear() + root.Draw()
// + screen.Show()) repaints into the new dimensions, but the terminal's
// pre-resize cell content isn't overwritten by the new frame's emit
// because tcell's diff compares against the prior emit, not against the
// terminal's actual state. Resize is the one event where those diverge
// (the terminal physically changed size; cells at positions beyond the
// new bounds, or stale content at edges, can survive into the next
// frame). One CSI 2J flash on resize is the right tradeoff — resize is
// rare and user-driven.
//
// First frame after startup: lastScreenW/H are zero, so the size
// comparison fires once and Syncs the initial frame. That's harmless —
// startup is already a high-noise rendering moment.
func (a *App) afterDraw(screen tcell.Screen) {
	w, h := screen.Size()
	if w != a.lastScreenW || h != a.lastScreenH {
		a.lastScreenW = w
		a.lastScreenH = h
		uxlog.Log("[tui] afterDraw resize %dx%d — Sync", w, h)
		screen.Sync()
	}
	// Plugin resize-envelope reconciliation runs after EVERY draw, not just
	// terminal resize: the pane's first real layout pass changes the computed
	// viewport without the screen size changing, and a lost/raced initial
	// envelope is corrected the same way. The draw that just completed is also
	// the signal that the pane's rect is real — mark it laid out when its page
	// was the one on screen (the help overlay may be in front instead).
	if m := a.activePlugin; m != nil && !m.laidOut {
		if front, _ := a.pages.GetFrontPage(); front == m.pageName {
			m.laidOut = true
		}
	}
	a.reconcilePluginViewSize()
}

// SetDaemonStale records that the connected daemon's binary differs from the
// TUI's. Must be called before Run() — the flag is consumed there.
func (a *App) SetDaemonStale(stale bool) {
	a.daemonStale = stale
}

// submitPluginSection is the production submit hook wired into the
// SettingsView at App construction. It looks up the section's callback URL
// from the live PluginSections list and POSTs the user-entered values map
// there.
//
// Two-process limitation: in --remote mode the TUI is on a host that may
// not reach the plugin's callback URL (the plugin server is typically on
// the same LAN as the daemon, not the user's phone). The proper fix is to
// proxy through the daemon's /submit endpoint, which a follow-up will
// wire once the apiclient gains a SubmitPluginSection method. Local mode
// — the common case — works today because both the TUI and the plugin
// share localhost.
func (a *App) submitPluginSection(scope, title string, values map[string]any) error {
	sections, err := a.db.PluginSections()
	if err != nil {
		return err
	}
	var callbackURL string
	for _, sec := range sections {
		if sec.Scope == scope && sec.Title == title {
			callbackURL = sec.CallbackURL
			break
		}
	}
	if callbackURL == "" {
		return fmt.Errorf("plugin section %s/%s not found", scope, title)
	}
	body, err := json.Marshal(values)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("plugin returned %d", resp.StatusCode)
	}
	return nil
}

// openRestartDaemonPrompt shows the modal asking whether to restart the
// out-of-date daemon. Idempotent.
func (a *App) openRestartDaemonPrompt() {
	if a.restartDaemonModal != nil {
		return
	}
	a.restartDaemonModal = modal.NewRestartDaemonModal()
	a.mode = modeRestartDaemonPrompt
	a.pages.AddPage("restartdaemon", a.restartDaemonModal, true, true)
	a.pages.SwitchToPage("restartdaemon")
	a.tapp.SetFocus(a.restartDaemonModal)
}

// closeRestartDaemonPrompt dismisses the modal and returns to the task list.
func (a *App) closeRestartDaemonPrompt() {
	a.mode = modeTaskList
	a.restartDaemonModal = nil
	a.pages.RemovePage("restartdaemon")
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
}

// handleRestartDaemonKey dispatches keys to the restart-daemon modal and
// reacts when the user picks Restart or Skip.
func (a *App) handleRestartDaemonKey(event *tcell.EventKey) {
	if a.restartDaemonModal == nil {
		return
	}
	handler := a.restartDaemonModal.InputHandler()
	handler(event, func(p tview.Primitive) {})
	if !a.restartDaemonModal.Done() {
		return
	}
	chooseRestart := a.restartDaemonModal.ChoseRestart()
	a.closeRestartDaemonPrompt()
	if chooseRestart {
		uxlog.Log("[tui] user chose to restart out-of-date daemon")
		a.mu.Lock()
		a.daemonRestarting = true
		a.lastDaemonRestart = time.Now()
		a.mu.Unlock()
		a.settings.SetDaemonRestarting(true)
		go a.restartDaemonFn()
	} else {
		uxlog.Log("[tui] user skipped daemon restart")
	}
}

// openRestartSupervisorPrompt shows the caution confirm before bouncing the
// session-supervisor. Unlike a daemon restart, this interrupts every running
// agent, so it is always gated. Idempotent.
func (a *App) openRestartSupervisorPrompt() {
	if a.restartSupervisorModal != nil {
		return
	}
	a.restartSupervisorModal = modal.NewConfirmModal(
		"Restart Session Supervisor?",
		"This SIGHUPs every running agent — active tasks flip to In Review.",
	)
	a.mode = modeConfirmRestartSupervisor
	a.pages.AddPage("restartsupervisor", a.restartSupervisorModal, true, true)
	a.pages.SwitchToPage("restartsupervisor")
	a.tapp.SetFocus(a.restartSupervisorModal)
}

// closeRestartSupervisorPrompt dismisses the modal and returns to the settings
// view (the row lives in Settings → System).
func (a *App) closeRestartSupervisorPrompt() {
	// The settings view runs under modeTaskList with the active tab tracked
	// separately by the header; the supervisor row is only reachable from the
	// Settings tab, so return focus there. Mirrors closeErrorModal.
	a.mode = modeTaskList
	a.restartSupervisorModal = nil
	a.pages.RemovePage("restartsupervisor")
	a.pages.SwitchToPage("settings")
	a.tapp.SetFocus(a.settings)
}

// handleRestartSupervisorKey dispatches keys to the confirm modal and bounces
// the supervisor when the user accepts.
func (a *App) handleRestartSupervisorKey(event *tcell.EventKey) {
	if a.restartSupervisorModal == nil {
		return
	}
	a.restartSupervisorModal.InputHandler()(event, func(tview.Primitive) {})
	if a.restartSupervisorModal.Confirmed() {
		a.closeRestartSupervisorPrompt()
		uxlog.Log("[tui] user confirmed session-supervisor restart")
		// The bounce restarts the daemon too (restartSupervisor → restartDaemon).
		// Mark daemonRestarting BEFORE launching the goroutine so the tick-loop
		// health check doesn't see the ~6s daemon-down window, conclude the
		// daemon is dead, and spawn a SECOND concurrent restartDaemon. Mirrors
		// handleRestartDaemonKey; restartDaemon clears the flag when it settles.
		a.mu.Lock()
		a.daemonRestarting = true
		a.lastDaemonRestart = time.Now()
		a.mu.Unlock()
		a.settings.SetDaemonRestarting(true)
		a.settings.SetSupervisorRestarting(true)
		go a.restartSupervisorFn()
		return
	}
	if a.restartSupervisorModal.Canceled() {
		uxlog.Log("[tui] user canceled session-supervisor restart")
		a.closeRestartSupervisorPrompt()
	}
}

// Run starts the application event loop.
func (a *App) Run() error {
	// Wrap the tcell screen in lazyScreen. The wrapper is a passthrough
	// today; keeping the indirection lets smoke tests inject a
	// SimulationScreen through the same path production uses.
	rawScreen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	a.screen = &lazyScreen{Screen: rawScreen}
	a.tapp.SetScreen(a.screen)
	// EnableMouse/EnablePaste must be called AFTER SetScreen. tview's
	// EnablePaste only calls screen.EnablePaste() when a.screen is non-nil,
	// and Run() only auto-enables when it creates its own screen. Calling
	// these before SetScreen stores the flag but never applies it.
	a.tapp.EnableMouse(true)
	a.tapp.EnablePaste(true)
	// Focus reporting (DECSET 1004): tmux/iTerm2 forward focus events to
	// the foreground process. On focus regain we call screen.Sync()
	// directly to repair any drift that accumulated while we were
	// unfocused (the multiplexer may have repainted our pane from a stale
	// backing store). One CSI 2J flash on a rare event is the right
	// tradeoff for guaranteed correctness — and atomic inside tmux when
	// the user has `set -as terminal-features ',xterm*:sync'` in their
	// tmux.conf (see README "Running inside tmux").
	//
	// Concurrency: Sync() is called from tcell's PollEvent goroutine
	// inside lazyScreen.PollEvent before the event is returned to tview.
	// tcell.Screen.Sync() acquires the screen's internal mutex (same lock
	// used by Show() inside tview's draw goroutine), so the call is
	// thread-safe but can interleave with an in-progress root.Draw() at
	// the cell-buffer mutex boundary. This is a subtle change from the
	// deleted flag-deferred-to-afterDraw pattern (which was guaranteed
	// single-threaded by virtue of running inside tview's draw cycle),
	// but the rare-event guarantee holds — focus events arrive at human
	// speed and the lock contention window is microseconds.
	a.screen.EnableFocus()
	a.screen.onFocusGained = func() {
		uxlog.Log("[tui] focus regained — Sync")
		a.screen.Sync()
	}

	go a.tickLoop()
	go a.spinnerLoop()
	defer close(a.tickDone)

	// If main detected the daemon's binary differs from ours, open the prompt
	// directly. We can't use QueueUpdateDraw here: tview's QueueUpdate is
	// synchronous (it sends on a buffered channel, then blocks on a per-call
	// done channel until the event loop executes the closure). The event loop
	// only starts inside tapp.Run() below, so queuing now would deadlock the
	// TUI before any frame is painted. Setting modal state directly is safe
	// because no Draw goroutine exists yet — Pages.AddPage / SwitchToPage
	// don't take their own locks (only SetFocus does), so the safety comes
	// from the absence of a concurrent reader, not internal synchronization.
	// Note: pages.SetChangedFunc fires forceRedraw which is now log-only
	// (no Sync, no channel send, no blocking). Safe to call pre-Run().
	if a.daemonStale {
		a.openRestartDaemonPrompt()
	}

	uxlog.Log("[tui] starting tcell/tview application")
	return a.tapp.Run()
}

// tickLoop runs periodic updates.
func (a *App) tickLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.tickDone:
			return
		case <-ticker.C:
			a.onTick()
		}
	}
}

// spinnerLoop triggers redraws for smooth spinner animation.
// Polls at 100ms (the fastest non-Progress spinner's TickInterval). The actual
// frame selection is time-based in updateSpinnerFrame, so this just ensures
// redraws happen often enough. Only fires when tasks are actively running
// (not idle) — idle tasks show a static moon icon, not the spinner. Skipping
// redraws when all tasks are idle prevents unnecessary full-screen repaints
// that interfere with tmux hyperlink hover and waste CPU.
func (a *App) spinnerLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-a.tickDone:
			return
		case <-ticker.C:
			a.mu.Lock()
			hasActiveRunning := false
			if len(a.runningIDs) > 0 {
				idleSet := make(map[string]bool, len(a.idleIDs))
				for _, id := range a.idleIDs {
					idleSet[id] = true
				}
				for _, id := range a.runningIDs {
					if !idleSet[id] {
						hasActiveRunning = true
						break
					}
				}
			}
			a.mu.Unlock()
			// Reconcile hera pane PTY sizes at the spinner cadence (~100ms) so a
			// freshly-bound session doesn't paint at a stale width for up to a
			// full 1s tick. No-op when no hera pane is drawn this frame.
			a.heraPage.SyncPanes()
			if hasActiveRunning {
				a.tapp.QueueUpdateDraw(func() {})
			}
		}
	}
}

// onTick handles periodic updates.
func (a *App) onTick() {
	// Fetch running IDs OUTSIDE the lock — this is an RPC call that can take
	// up to 5 seconds on timeout, and holding a.mu during that blocks the
	// entire UI (QueueUpdateDraw callbacks can't run while the tick goroutine
	// holds the mutex and waits for RPC).
	// Capture startGen BEFORE the RPC so we can detect if startSession ran
	// between the snapshot and the reconciliation callback.
	startGen := a.startGen.Load()
	// Snapshot runner under lock — restartDaemon swaps a.runner on the tview
	// goroutine, and reading it without the lock is a data race. A stale
	// pointer hits the old client whose RPC connection may be reused by the
	// new daemon (same socket path), returning an empty session list that
	// triggers false reconciliation.
	a.mu.Lock()
	runner := a.runner
	a.mu.Unlock()
	runningIDs, idleIDs := runner.RunningAndIdle()

	// Reconcile hera pane PTY sizes off the main thread (Resize RPC). Safe to
	// call always: it no-ops for unbound panes and for panes whose Draw didn't
	// run this frame (pendingResize stays zero when the Hera tab is inactive),
	// so it can never fight the main agent view's resize of the same task.
	a.heraPage.SyncPanes()

	// Read daemon state for health check BEFORE QueueUpdateDraw — daemon
	// fields are protected by a.mu and don't touch tview widgets.
	a.mu.Lock()
	checkDaemon := a.daemonConnected && a.daemonClient != nil
	a.mu.Unlock()

	// All UI state modifications must run on the tview main goroutine.
	// TaskListView (rows, cursor, expanded), preview panels, and agent pane
	// have no internal mutex — concurrent access from the tick goroutine
	// races with Draw() and InputHandler() on the tview goroutine.
	// This single QueueUpdateDraw replaces the previous pattern of separate
	// QueueUpdateDraw calls per UI mode (agent pane, empty no-op, etc.).
	//
	// Debounce: skip enqueue if a prior tick callback hasn't run yet.
	// Prevents unbounded queueing when the tview goroutine is slow.
	if !a.tickCallbackPending.CompareAndSwap(false, true) {
		goto healthCheck
	}
	a.tapp.QueueUpdateDraw(func() {
		a.tickCallbackPending.Store(false)
		// Lock a.mu to protect App-level fields (mode, agentState,
		// tasks, runningIDs) during refresh.
		a.mu.Lock()
		// If a session was started between the RPC snapshot and now, the
		// runningIDs are stale — the new session won't be in them, causing
		// reconciliation to wrongly flip it to InReview (a false termination).
		// Pass nil to skip reconciliation this tick; the next tick will have fresh IDs.
		if a.startGen.Load() != startGen {
			uxlog.Log("[tui] tick: startGen changed (%d → %d), skipping reconciliation with stale runningIDs", startGen, a.startGen.Load())
			runningIDs = nil
		}
		a.refreshTasksWithIDs(runningIDs, idleIDs)
		taskID := ""
		if a.mode == modeAgent {
			taskID = a.agentState.TaskID
		}
		a.mu.Unlock()

		// Refresh task list side panels.
		// Note: refreshPreview can be expensive for large session logs on
		// first load (up to 256KB ring buffer copy + VT emulator feed), but
		// the TotalWritten/LogSize cache short-circuits on subsequent calls.
		if previewTaskID := a.taskPreview.TaskID(); previewTaskID != "" && a.mode == modeTaskList {
			a.refreshPreview(previewTaskID)
			// Also refresh git status for the selected task periodically.
			// lastTaskGitRefresh is only accessed on the tview goroutine.
			if sel := a.tasklist.SelectedTask(); sel != nil && sel.Worktree != "" && time.Since(a.lastTaskGitRefresh) > 3*time.Second {
				a.lastTaskGitRefresh = time.Now()
				go a.fetchTaskGitStatus(sel.ID, sel.Worktree)
			}
		}

		// Refresh the cached agent-staged clipboard payload for the active
		// task. Cheap (one RPC, in-memory map lookup on the daemon side).
		// Only meaningful when the runner is daemon-backed; in-process mode
		// has no clipboard store. We poll here rather than subscribing
		// because the existing RPC channel is request/response only.
		if taskID != "" {
			a.refreshClipboardCache(taskID)
		} else if a.clipboardPending != "" {
			a.clipboardPending = ""
			a.clipboardPendingTask = ""
			a.agentHeader.SetClipboardHint(false)
		}

		// Update agent pane session (taskID is non-empty only in agent mode).
		// Only set the session if the pane doesn't already have one.
		// onTaskSelect and startSession already wire the correct session;
		// calling runner.Get here repeatedly creates new RemoteSession
		// objects when streams are failing (connect→EOF→removeSessionStreamLost
		// deletes from client map → next Get creates fresh session with empty
		// buffer → SetSession resets emulator → "Waiting for output..." flash).
		if taskID != "" {
			if a.agentPane.Session() == nil {
				sess := a.runner.Get(taskID)
				if sess != nil {
					a.agentPane.SetSession(sess)
				}
			}
			// Refresh git status periodically.
			// lastGitRefresh is only accessed on the tview goroutine.
			if a.worktreeDir != "" && time.Since(a.lastGitRefresh) > 3*time.Second {
				go a.fetchGitStatus(taskID, a.worktreeDir)
			}
		}

	})

healthCheck:
	// Daemon health check — uses RPC (slow), must stay on tick goroutine.
	if checkDaemon {
		a.mu.Lock()
		restarting := a.daemonRestarting
		a.mu.Unlock()
		if !restarting {
			if err := a.daemonClient.Ping(); err != nil {
				a.mu.Lock()
				a.daemonFailures++
				failures := a.daemonFailures
				cooldownOK := time.Since(a.lastDaemonRestart) >= 30*time.Second
				a.mu.Unlock()
				if failures >= 3 && cooldownOK {
					uxlog.Log("[tui] daemon unreachable after %d pings, restarting...", failures)
					a.mu.Lock()
					a.daemonRestarting = true
					a.lastDaemonRestart = time.Now()
					a.mu.Unlock()
					go a.restartDaemonFn()
				} else if failures >= 3 && !cooldownOK {
					uxlog.Log("[tui] daemon unreachable but restart cooldown active, skipping")
				}
			} else {
				a.mu.Lock()
				a.daemonFailures = 0
				a.mu.Unlock()
			}
		}
	}
}

// updateArgus runs `go install ./...` on the daemon side and, on success,
// restarts the daemon so the new binary takes over. Must run in a goroutine.
func (a *App) updateArgus() {
	uxlog.Log("[tui] update argus: starting")
	// Snapshot the daemon client under the lock — restartDaemon swaps it on
	// the tview goroutine and racing with that swap trips the race detector.
	a.mu.Lock()
	dc := a.daemonClient
	a.mu.Unlock()
	if dc == nil {
		a.tapp.QueueUpdateDraw(func() {
			a.settings.SetUpdateResult("", "Failed: no daemon connection")
			a.statusbar.SetError("Update failed: no daemon connection")
		})
		return
	}
	output, err := dc.UpdateSelf()
	if err != nil {
		uxlog.Log("[tui] update argus: failed: %v", err)
		a.tapp.QueueUpdateDraw(func() {
			a.settings.SetUpdateResult(output, "Failed: "+err.Error())
			a.statusbar.SetError("Update failed: " + err.Error())
		})
		return
	}
	uxlog.Log("[tui] update argus: go install ok, restarting daemon")
	a.tapp.QueueUpdateDraw(func() {
		a.settings.SetUpdateResult(output, "Update succeeded — restarting daemon...")
		a.mu.Lock()
		a.daemonRestarting = true
		a.lastDaemonRestart = time.Now()
		a.mu.Unlock()
		a.settings.SetDaemonRestarting(true)
	})
	// Synchronous — updateArgus is already running in a goroutine (spawned
	// from settings.OnUpdateArgus). The other three restartDaemonFn call
	// sites use `go` because they fire from the tview main goroutine.
	a.restartDaemonFn()
}

// toggleAutoStart installs or uninstalls the LaunchAgent. Must run in a
// goroutine so launchctl invocations don't block the tview event loop.
// Reports back via QueueUpdateDraw → SetAutoStartResult.
func (a *App) toggleAutoStart(installed bool) {
	uxlog.Log("[tui] launchagent toggle: installed=%v", installed)
	var message string
	if installed {
		if err := launchagent.Uninstall(); err != nil {
			message = "Uninstall failed: " + err.Error()
			uxlog.Log("[tui] launchagent uninstall failed: %v", err)
		} else {
			message = "LaunchAgent removed"
			uxlog.Log("[tui] launchagent uninstalled")
		}
	} else {
		daemonExe, err := launchagent.ResolveDaemonExe()
		if err != nil {
			message = "Resolve daemon exe failed: " + err.Error()
			uxlog.Log("[tui] launchagent install: resolve daemon exe: %v", err)
		} else if err := launchagent.Install(daemonExe); err != nil {
			message = "Install failed: " + err.Error()
			uxlog.Log("[tui] launchagent install failed: %v", err)
		} else {
			message = "LaunchAgent installed — daemon will auto-start at login"
			uxlog.Log("[tui] launchagent installed (exe=%s)", daemonExe)
		}
	}
	status := launchagent.CurrentStatus()
	a.tapp.QueueUpdateDraw(func() {
		a.settings.SetAutoStartResult(message, status)
	})
}

// restartDaemon kills the old daemon, auto-starts a new one, and reconnects.
// Must be called from a goroutine (not UI thread).
func (a *App) restartDaemon() {
	uxlog.Log("[tui] restarting daemon...")

	// Try graceful shutdown via RPC.
	if a.daemonClient != nil {
		a.daemonClient.Close()
	}

	sockPath := daemon.DefaultSocketPath()
	dclient.WaitForShutdown(sockPath, 3*time.Second)

	// Auto-start new daemon.
	newClient, err := dclient.AutoStart(sockPath)
	if err != nil {
		uxlog.Log("[tui] daemon restart failed: %v", err)
		a.tapp.QueueUpdateDraw(func() {
			a.mu.Lock()
			a.daemonRestarting = false
			a.daemonFailures = 0
			a.mu.Unlock()
			a.settings.SetDaemonRestarting(false)
			a.statusbar.SetError("Daemon restart failed: " + err.Error())
		})
		return
	}

	uxlog.Log("[tui] daemon restarted, reconnected")

	// Wire up session exit callback on the new client.
	newClient.OnSessionExit(func(taskID string, info daemon.ExitInfo) {
		a.HandleSessionExit(taskID, info)
	})

	a.tapp.QueueUpdateDraw(func() {
		a.mu.Lock()
		a.daemonRestarting = false
		a.daemonFailures = 0
		a.daemonClient = newClient
		a.runner = newClient
		a.restartedClient = newClient
		// Clear stale running/idle IDs from old daemon — the new daemon has
		// no sessions yet. Using nil (not empty) ensures reconciliation is
		// skipped until the first tick fetches fresh IDs from the new daemon.
		a.runningIDs = nil
		a.idleIDs = nil
		a.mu.Unlock()

		a.settings.SetDaemonRestarting(false)

		// agent.ReconcileStaleSessions ran inside the new daemon's Serve()
		// before its socket opened, so by the time we reach this code stale
		// InProgress rows are already InReview. Reload locally; an async RPC
		// would race with the user entering tasks while the new daemon is
		// still warming up.
		a.refreshTasksLocal()
	})
}

// restartSupervisor bounces the out-of-process session-supervisor: it stops
// the running supervisor (which SIGHUPs every agent PTY it owns) and then
// restarts the daemon, whose connectSupervisor finds no live supervisor on the
// socket and auto-starts a fresh one. Restarting the daemon is required because
// the daemon holds the SupervisorClient connection — there is no mid-life
// reconnect, so the clean way to pick up the new supervisor is a daemon bounce.
// Must be called from a goroutine (not the UI thread).
func (a *App) restartSupervisor() {
	uxlog.Log("[tui] restarting session-supervisor (agents will be interrupted)...")

	supSock := daemon.DefaultSupervisorSocketPath()
	// Stop the live supervisor over its own socket. Connect dials + best-effort
	// Pings; Shutdown is the same "Daemon.Shutdown" RPC the CLI `argus
	// session-supervisor stop` uses. A connect failure just means none is
	// running — proceed to the daemon restart, which will auto-start one.
	if c, err := dclient.Connect(supSock); err == nil {
		if serr := c.Shutdown(); serr != nil {
			uxlog.Log("[tui] supervisor shutdown RPC error: %v", serr)
		}
		c.Close() //nolint:errcheck // short-lived shutdown client; close error is non-actionable
	} else {
		uxlog.Log("[tui] supervisor not reachable for shutdown (will auto-start fresh): %v", err)
	}
	dclient.WaitForShutdown(supSock, 3*time.Second)

	// Restart the daemon so it reconnects and auto-starts a fresh supervisor.
	// restartDaemon settles by clearing daemonRestarting (App field + the
	// settings daemon flag) — it does NOT touch supervisorRestarting, which is a
	// distinct field, so clear that one explicitly once the daemon is back.
	a.restartDaemon()
	a.tapp.QueueUpdateDraw(func() {
		a.settings.SetSupervisorRestarting(false)
	})
	uxlog.Log("[tui] session-supervisor restart complete")
}

// RestartedClient returns the new daemon client after a daemon restart, or nil.
func (a *App) RestartedClient() *dclient.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.restartedClient
}

// NotifySessionExit is called from the in-process runner's onFinish callback.
// It triggers a UI refresh so session exits are detected immediately (not on next tick).
func (a *App) NotifySessionExit(taskID string, err error, stopped bool, lastOutput []byte) {
	uxlog.Log("[tui] session exit (in-process): task=%s stopped=%v err=%v", taskID, stopped, err)
	_ = lastOutput
	// In-process mode reads HasPendingRestart synchronously off the local
	// runner — no RPC, no main-thread stall.
	pending := a.runner.HasPendingRestart(taskID)
	// Derive cleanExit from the SAME predicate the daemon uses — construct the
	// equivalent ExitInfo and call CleanExit() rather than re-implementing the
	// rule inline, so the in-process and daemon flip sites can never drift (e.g.
	// if CleanExit() ever grows another term). In-process mode has no stream, so
	// StreamLost is always false here. Only a self-driven, zero-exit process is
	// "complete"; a crash / missing-binary fast-fail (err != nil) → InReview.
	var errStr string
	if err != nil {
		errStr = err.Error()
	}
	cleanExit := (daemon.ExitInfo{Stopped: stopped, Err: errStr}).CleanExit()
	a.tapp.QueueUpdateDraw(func() {
		a.handleSessionExitUI(taskID, cleanExit, pending)
	})
}

// HandleSessionExit is called from the daemon client's OnSessionExit callback.
// It flips task status (via handleSessionExitUI) and refreshes the UI — EXCEPT
// when info.StreamLost is set, in which case the process may still be alive, so
// it logs and returns WITHOUT touching status (the row stays as-is).
func (a *App) HandleSessionExit(taskID string, info daemon.ExitInfo) {
	if info.StreamLost {
		uxlog.Log("[tui] stream lost: task=%s — status unchanged, process may still be alive", taskID)
		return
	}
	uxlog.Log("[tui] session exit (daemon): task=%s err=%s stopped=%v pending=%v lastOutput=%d bytes",
		taskID, info.Err, info.Stopped, info.PendingRestart, len(info.LastOutput))
	a.tapp.QueueUpdateDraw(func() {
		// PendingRestart was stamped by the daemon's onFinish under the same
		// snapshot it used to decide whether to skip transitionTaskOnExit, so
		// the TUI never has to RPC from the main goroutine. CleanExit() is the
		// shared predicate the daemon used for its own flip — reuse it so the
		// two sites can never reach different terminal statuses.
		a.handleSessionExitUI(taskID, info.CleanExit(), info.PendingRestart)
	})
}

// handleSessionExitUI runs on the tview main goroutine (inside QueueUpdateDraw).
// Called by both NotifySessionExit (in-process) and HandleSessionExit (daemon).
// pendingRestart is captured by the caller from a non-RPC source (in-process:
// direct method call; daemon: ExitInfo.PendingRestart stamped by daemon side).
func (a *App) handleSessionExitUI(taskID string, cleanExit, pendingRestart bool) {
	// Two callers, two flip-site stories:
	//   - Daemon mode (HandleSessionExit): the daemon's onFinish callback
	//     already ran transitionTaskOnExit before closing the stream that
	//     triggered us, so t.Status may already have moved on. The flip
	//     below is a defensive idempotent retry.
	//   - In-process mode (NotifySessionExit): the in-process onFinish
	//     calls only NotifySessionExit (no transitionTaskOnExit — that's
	//     a *Daemon method), so the flip below is the *only* flip site.
	// Codex session-ID capture is hoisted out of the StatusInProgress check
	// because in daemon mode the check fails after the daemon's flip and
	// would otherwise silently drop the capture.
	var captureTask *model.Task
	t, err := a.db.Get(taskID)
	if err != nil || t == nil {
		uxlog.Log("[tui] handleSessionExitUI: task %s lookup failed: %v", taskID, err)
		return
	}
	if t.Worktree != "" {
		// Snapshot the task for the capture goroutine, which resolves the
		// backend and decides whether to recapture (agent.NeedsSessionRecapture)
		// off the main goroutine. We snapshot regardless of SessionID because
		// Claude must re-capture on every exit (a /clear mints a new UUID) even
		// though its SessionID is always non-empty.
		captureTask = t
	}
	if t.Status == model.StatusInProgress {
		// Skip the transition when the daemon has a kick-restart queued —
		// otherwise the TUI's exit notification (which arrives independently
		// of the API-initiated kick) would flip the row to InReview while the
		// runner's exit goroutine is mid-restart, leaving the resumed session
		// running with the wrong status. The daemon's onFinish guards on the
		// same predicate; this mirrors it from the TUI side. Local-flag kicks
		// (a.pendingRerenderRestart) are handled below where we revert to
		// InProgress before resuming, so they tolerate the transient flip.
		if !pendingRestart {
			// Hera worker finish policy mirror (BUG-050). Kept in lockstep with
			// the daemon's transitionTaskOnExit and the hera_status("done") MCP
			// arm via the shared db.RollHeraWorkerToReview helper, so all flip
			// sites can never disagree (the PR #707 invariant): a task holding a
			// live worker-kind hera binding never self-completes — the
			// coordinator/human closes it out. In daemon mode the daemon flips
			// first (its onFinish runs before the stream close that triggers us),
			// so this is a no-op; it is load-bearing in daemon-less in-process
			// mode, where this is the ONLY flip site. Local-only: a.db is *db.DB
			// locally and satisfies heraFinishStore; in --remote mode it does not,
			// so we fall through to the plain rule (remote daemon is authoritative).
			rolled := false
			if fs, ok := a.db.(heraFinishStore); ok {
				if f, err := fs.RollHeraWorkerToReview(t.ID); err != nil {
					uxlog.Log("[tui] hera worker roll failed for %s (default policy): %v", t.ID, err)
				} else {
					rolled = f
				}
			}
			if rolled {
				uxlog.Log("[tui] task %s (%s) → in_review (hera worker close-out)", t.ID, t.Name)
			} else {
				if cleanExit {
					t.SetStatus(model.StatusComplete)
				} else {
					t.SetStatus(model.StatusInReview) // crash/stop/fast-fail → recoverable
				}
				// Persist via the partial setter, not Update: t was Get'd fresh
				// above, but the concurrent autoname goroutine could land a rename
				// in the straight-line window before this write. SetStatus touches
				// only status/timestamps, so it can't clobber that name — same
				// reasoning as the reconciliation and OnPin/OnStatusChange paths.
				a.db.SetStatus(t.ID, t.Status) //nolint:errcheck
				uxlog.Log("[tui] task %s (%s) → %s", t.ID, t.Name, t.Status)
			}
		} else {
			uxlog.Log("[tui] task %s exit deferred: daemon kick-restart in flight", t.ID)
		}
	}

	// Capture session ID in a background goroutine — agent.CaptureSessionID
	// dispatches to the backend-specific scan (codex SQLite, pi readdir) and
	// returns ("", nil) for Claude-style backends that pre-mint. Filesystem /
	// SQLite work must not block the tview main goroutine. The daemon mirrors
	// this in its onFinish callback so headless / PWA users get the same.
	if captureTask != nil {
		go func(snap model.Task) {
			cfg := a.db.Config()
			// Codex/Pi capture once; Claude refreshes every exit; unknown
			// backends never recapture. Decided here (off the tview main
			// goroutine) so the filesystem/SQLite work below never blocks it.
			if !agent.NeedsSessionRecapture(&snap, cfg) {
				return
			}
			// Resolve the backend name once so log lines tag which dialect
			// (codex / pi / claude) the capture targeted — keeps the previous
			// per-kind logging searchability after the dispatcher refactor.
			kind := "agent"
			if b, berr := agent.ResolveBackend(&snap, cfg); berr == nil {
				switch {
				case agent.IsCodexBackend(b.Command):
					kind = "codex"
				case agent.IsPiBackend(b.Command):
					kind = "pi"
				case agent.IsClaudeBackend(b.Command):
					kind = "claude"
				}
			}
			sid, err := agent.CaptureSessionID(&snap, cfg)
			if err != nil {
				// Capture failure leaves the existing SessionID intact.
				uxlog.Log("[tui] %s session ID capture failed for task %s: %v", kind, snap.ID, err)
				return
			}
			if sid == "" || sid == snap.SessionID {
				return // Unrecognized backend, or no change (common no-/clear exit).
			}
			uxlog.Log("[tui] captured %s session ID %s for task %s", kind, sid, snap.ID)
			a.tapp.QueueUpdateDraw(func() {
				if t, gerr := a.db.Get(snap.ID); gerr == nil && t != nil {
					t.SessionID = sid
					a.db.Update(t) //nolint:errcheck
				}
			})
		}(*captureTask)
	}

	// If maybeKickRerender flagged this task, immediately resume it
	// at the current (wider) PTY before the user-visible "exited" state has
	// a chance to render. The new session inherits SessionID, so Claude
	// re-loads the conversation and renders history at the wider size. Skip
	// the post-exit clearing/navigation below — startSession will reattach
	// the agent pane in place.
	//
	// Gate on !cleanExit (not just "stopped"): a rerender kick always exits
	// non-clean because KickRerender calls sess.Stop() — and if the agent instead
	// crashed during the kick window, restarting it is still the right move, so
	// any non-clean exit with a pending rerender flag continues here.
	//
	// Only restart if the user is still viewing this task. If they navigated
	// away after the kick, fall through to the normal exit path so the task
	// settles at InReview and the user can resume it manually later.
	if !cleanExit && a.pendingRerenderRestart[taskID] {
		delete(a.pendingRerenderRestart, taskID)
		a.mu.Lock()
		stillViewing := a.mode == modeAgent && a.agentState.TaskID == taskID
		a.mu.Unlock()
		if !stillViewing {
			uxlog.Log("[tui] rerender: user navigated away from task=%s, skipping auto-restart", taskID)
			a.statusbar.ClearInfo()
		} else if t, err := a.db.Get(taskID); err == nil && t != nil {
			uxlog.Log("[tui] rerender: restarting task=%s session=%s", t.ID, t.SessionID)
			// Force the resumed task back into InProgress; either the daemon's
			// onFinish callback or this function's StatusInProgress branch
			// above has just flipped it to InReview.
			//
			// Stays on full-row db.Update (not the name-safe db.SetStatus the
			// other status flips use): startSession on the next line immediately
			// writes the same struct again with SessionID+AgentPID, so a partial
			// status setter here would be pointless — name-safety for this path
			// hinges on startSession's multi-field write, which is the
			// pre-existing out-of-scope case.
			t.SetStatus(model.StatusInProgress)
			a.db.Update(t) //nolint:errcheck
			a.startSession(t)
			a.statusbar.ClearInfo()
			a.refreshTasksAsync()
			return
		} else {
			uxlog.Log("[tui] rerender: task %s vanished from DB, falling through", taskID)
			a.statusbar.ClearInfo()
		}
	}

	// If we're viewing this task's agent pane and it exited cleanly (→ Complete),
	// navigate back to the task list. If it ended any other way (→ InReview: a
	// stop, a crash, or a missing-binary fast-fail), STAY in the agent pane and
	// clear the session so the user sees the exited state and can resume in place
	// rather than being bounced to the list with no explanation.
	a.mu.Lock()
	viewing := a.mode == modeAgent && a.agentState.TaskID == taskID
	a.mu.Unlock()
	if viewing {
		if cleanExit {
			a.exitAgentView()
		} else {
			a.agentPane.SetSession(nil)
			// Eagerly start async replay rebuild so the first Draw() after
			// session stop hits the cache instead of showing a brief flash
			// of "Waiting for output..." while the rebuild runs. Only needed
			// for stopped sessions — completed sessions exit agent view, so
			// the pre-built emulator would be discarded on re-entry.
			a.agentPane.EagerReplayBuild()
		}
	}

	// Refresh task list — fetch running/idle IDs in a goroutine to avoid
	// blocking the tview main goroutine with an RPC call.
	a.refreshTasksAsync()
}

// syncIdleUnvisited pushes the current idleUnvisited set to the task list.
// All access to idleUnvisited/viewedWhileAgent happens on the tview main goroutine
// (via QueueUpdateDraw or direct calls from InputHandler), so no mutex is needed.
func (a *App) syncIdleUnvisited() {
	ids := make([]string, 0, len(a.idleUnvisited))
	for id := range a.idleUnvisited {
		ids = append(ids, id)
	}
	a.tasklist.SetIdleUnvisited(ids)
}

// updateAttentionBar feeds the agent view's attention bar with the names of
// tasks currently blocked on a user prompt (the `needsInputIDs` set computed
// by refreshTasksWithIDs). The currently-viewed task is excluded so the bar
// only surfaces OTHER tasks waiting on input — opening one yourself already
// makes it obvious. Names are sorted for stable rendering.
func (a *App) updateAttentionBar() {
	if a.attentionBar == nil {
		return
	}
	currentID := ""
	if a.mode == modeAgent {
		currentID = a.agentState.TaskID
	}
	byID := make(map[string]*model.Task, len(a.tasks))
	for _, t := range a.tasks {
		byID[t.ID] = t
	}
	entries := make([]widget.AttentionEntry, 0, len(a.needsInputIDs))
	for _, id := range a.needsInputIDs {
		if id == currentID {
			continue
		}
		t, ok := byID[id]
		if !ok {
			continue
		}
		entries = append(entries, widget.AttentionEntry{TaskName: t.Name})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].TaskName < entries[j].TaskName })
	a.attentionBar.SetEntries(entries)
}

// detectNeedsInput returns the subset of idleIDs whose recent PTY output
// contains a known "agent is blocked on a user prompt" signature. Scanning is
// gated on idleness — an agent that's still streaming bytes is not blocked
// even if the marker text passes through the buffer transiently. Reads use
// the on-disk session log so detection works for tasks the user has not yet
// visited (see readSessionLogTailBytes).
func (a *App) detectNeedsInput(idleIDs []string) []string {
	if len(idleIDs) == 0 {
		return nil
	}
	var out []string
	for _, id := range idleIDs {
		tail := readSessionLogTailBytes(id, detectNeedsInputTailBytes)
		if len(tail) == 0 {
			continue
		}
		if agent.DetectNeedsInput(tail) {
			out = append(out, id)
		}
	}
	return out
}

// detectNeedsInputSticky wraps detectNeedsInput with a "carry-forward" pass:
// any task that was previously detected as needing input gets re-checked
// against its log tail this tick, even if it has fallen out of idleIDs.
//
// Why: Claude's prompt UI emits periodic animation bytes (cursor blink,
// spinner) while waiting for the user. Each emission bumps the session's
// lastOutput, which kicks the task out of the daemon's idle list for a tick
// or two at a time. Without this pass the attention bar would oscillate —
// visible only during the ~3 s windows when the task crosses back through
// the idle threshold — which the user perceives as "the bar shows briefly
// then disappears."
//
// The sticky entry self-clears when the on-disk marker is gone (the agent
// has produced enough new bytes to push it out of the tail window — i.e.
// the question has been answered) or when the task is no longer running.
func (a *App) detectNeedsInputSticky(idleIDs, runningIDs, prevNeedsInput []string) []string {
	fresh := a.detectNeedsInput(idleIDs)
	if len(prevNeedsInput) == 0 {
		return fresh
	}
	freshSet := make(map[string]bool, len(fresh))
	for _, id := range fresh {
		freshSet[id] = true
	}
	runningSet := make(map[string]bool, len(runningIDs))
	for _, id := range runningIDs {
		runningSet[id] = true
	}
	for _, id := range prevNeedsInput {
		if freshSet[id] || !runningSet[id] {
			continue
		}
		tail := readSessionLogTailBytes(id, detectNeedsInputTailBytes)
		if len(tail) == 0 {
			continue
		}
		if agent.DetectNeedsInput(tail) {
			fresh = append(fresh, id)
			freshSet[id] = true
		}
	}
	return fresh
}

// detectNeedsInputTailBytes is how many bytes to read from the end of each
// idle task's session log per tick. Large enough to contain Claude's full
// selection-UI overlay after the colorized repaint inflates line widths.
//
// Keep in sync with agent.needsInputTailWindow (the ring-buffer equivalent used
// by agent.BlockedOnPrompt) so the TUI and API detect over the same-sized tail.
const detectNeedsInputTailBytes = 16 * 1024

// readSessionLogTailBytes returns the last n raw bytes of a task's session
// log, or nil on any error. Unlike readSessionLogTail (which strips ANSI for
// human display), this preserves the raw stream so the caller can do its own
// ANSI handling.
//
// detectNeedsInput reads here instead of through SessionHandle.RecentOutputTail
// because in daemon-client mode the local ring buffer only fills after the
// TUI opens a stream connection for that session, i.e. after the user has
// visited it. The disk log captures every byte the daemon ever wrote, so the
// detector can flag a blocked agent the user has never opened.
func readSessionLogTailBytes(taskID string, n int) []byte {
	f, err := os.Open(agent.SessionLogPath(taskID))
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil
	}
	size := info.Size()
	offset := int64(0)
	if size > int64(n) {
		offset = size - int64(n)
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil
	}
	return data
}

// sessionBlockedOnPrompt reports whether the task's agent is idle AND blocked
// on a user prompt (selection-UI overlay or trailing question), by scanning the
// on-disk session log tail. The TUI counterpart to the API's
// agent.BlockedOnPrompt, but it reads the disk log rather than the session ring
// — the only reliable source in daemon-client mode, where the local ring stays
// empty until a stream attaches. Gated on idle because a streaming agent can't
// be blocked.
//
// Known tradeoff: the log is truncated only on StartSession (O_TRUNC), so a
// selection-prompt marker lingers in the tail after the user answers until
// ~16 KB of newer output pushes it out or the session restarts. During that
// window a genuine resize defers the (purely cosmetic) scrollback re-render
// rather than firing it. Deferring a re-render is strictly preferable to the
// alternative this gate exists to prevent — killing a session that is actually
// waiting on a question and dismissing it via the --session-id restart.
func sessionBlockedOnPrompt(taskID string, idle bool) bool {
	return idle && agent.DetectNeedsInput(readSessionLogTailBytes(taskID, detectNeedsInputTailBytes))
}

// refreshTasks fetches running/idle session IDs (RPC) and updates the task
// list. IMPORTANT: This blocks on RPC calls — NEVER call from the tview main
// goroutine. Use refreshTasksAsync instead for any UI-thread call site.
func (a *App) refreshTasks() {
	runningIDs, idleIDs := a.runner.RunningAndIdle()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshTasksWithIDs(runningIDs, idleIDs)
}

// refreshTasksAsync fetches running/idle IDs in a background goroutine, then
// updates the task list on the tview main goroutine via QueueUpdateDraw.
// Safe to call from any goroutine including the tview main goroutine.
func (a *App) refreshTasksAsync() {
	startGen := a.startGen.Load()
	go func() {
		runningIDs, idleIDs := a.runner.RunningAndIdle()
		a.tapp.QueueUpdateDraw(func() {
			// If a session was started between the RPC snapshot and now,
			// the runningIDs are stale — pass nil to skip reconciliation.
			// Same guard as onTick to prevent the race where an async
			// refresh flips a newly-started task to InReview (a false termination).
			if a.startGen.Load() != startGen {
				uxlog.Log("[tui] refreshTasksAsync: startGen changed, skipping reconciliation with stale runningIDs")
				runningIDs = nil
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			// If a session was started between the RPC snapshot and now, the
			// runningIDs are stale — skip reconciliation this cycle.
			if a.startGen.Load() != startGen {
				uxlog.Log("[tui] refreshTasksAsync: startGen changed, skipping reconciliation with stale runningIDs")
				runningIDs = nil
			}
			a.refreshTasksWithIDs(runningIDs, idleIDs)
		})
	}()
}

// refreshTasksLocal re-reads tasks from the DB and updates the task list using
// the last-known running/idle IDs. Does NOT make RPC calls, so it is safe to
// call from the tview main goroutine. Use this when only DB state changed
// (e.g. task deleted) and running session state is unchanged.
func (a *App) refreshTasksLocal() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshTasksWithIDs(a.runningIDs, a.idleIDs)
}

// refreshTasksWithIDs updates the task list with pre-fetched running/idle IDs.
// Used by onTick to avoid calling Running() (RPC) while holding a.mu.
func (a *App) refreshTasksWithIDs(runningIDs, idleIDs []string) {
	tasks, err := a.db.Tasks()
	if err != nil {
		uxlog.Log("[tui] refreshTasksWithIDs: failed to load tasks: %v", err)
		return
	}
	a.tasks = tasks
	// Snapshot the previous needs-input set before we overwrite it. The
	// sticky pass below uses this to carry forward detections that have
	// fallen out of idleIDs — Claude's prompt UI emits periodic animation
	// bytes (cursor blink, spinner) that bump lastOutput without
	// representing real progress, kicking a genuinely-blocked task out of
	// the daemon's idle list for a tick or two at a time.
	prevNeedsInput := a.needsInputIDs
	a.runningIDs = runningIDs
	a.idleIDs = idleIDs

	// Reconcile stale in-progress tasks: if a task is InProgress in the DB
	// but has no running session, mark it InReview. This is a pure INFERENCE
	// from session-list absence — we have no exit event, so we cannot know the
	// agent finished cleanly and MUST NOT mark it Complete (that's reserved for
	// an observed clean exit; see handleSessionExitUI / transitionTaskOnExit).
	// InReview is the safe, recoverable landing the user can resume — matching
	// ReconcileStaleSessions' startup policy. This path is a backstop for "the
	// OnSessionExit notification didn't make it through"; the authoritative
	// flip is the daemon's exit-driven transition, which this can only ever
	// agree with or conservatively under-call (InReview, never Complete).
	//
	// Only reconcile when connected to a daemon — the daemon is the source of
	// truth for running sessions. In-process mode has its own onFinish callback.
	// Skip if runningIDs is nil (transient RPC failure) or during a restart.
	if a.daemonConnected && runningIDs != nil && !a.daemonRestarting {
		runningSet := make(map[string]bool, len(runningIDs))
		for _, id := range runningIDs {
			runningSet[id] = true
		}
		now := time.Now()
		for _, t := range a.tasks {
			if t.Status == model.StatusInProgress && !runningSet[t.ID] {
				// Grace period: don't reconcile tasks that were started within
				// the last 5 seconds. The daemon may not have registered the
				// session in ListSessions yet (e.g., after restart cascade).
				if startedAt, ok := a.recentStarts[t.ID]; ok && now.Sub(startedAt) < recentStartGrace {
					uxlog.Log("[tui] reconciliation grace period for task %s (%s), started %v ago", t.ID, t.Name, now.Sub(startedAt).Round(time.Millisecond))
					continue
				}
				t.SetStatus(model.StatusInReview) // in-memory: drives this frame's render
				// Persist via the partial setter, not Update: although a.tasks was
				// just reloaded from the DB at the top of this call, the concurrent
				// autoname goroutine could land a rename in the microsecond window
				// before this write. SetStatus touches only status/timestamps, so it
				// can't clobber that name — same reasoning as OnPin/OnStatusChange.
				a.db.SetStatus(t.ID, model.StatusInReview) //nolint:errcheck
				uxlog.Log("[tui] reconciled stale task %s (%s) → in_review (no running session; inferred, not observed)", t.ID, t.Name)
				delete(a.recentStarts, t.ID) // consumed; no need to check again
			}
		}
		// Evict expired grace period entries.
		for id, startedAt := range a.recentStarts {
			if now.Sub(startedAt) >= recentStartGrace {
				delete(a.recentStarts, id)
			}
		}
	}

	// Update idleUnvisited: add newly-idle tasks, remove tasks no longer idle.
	newIdle := make(map[string]bool, len(idleIDs))
	for _, id := range idleIDs {
		newIdle[id] = true
	}
	prevIdle := a.tasklist.IdleSet()
	for id := range newIdle {
		if !prevIdle[id] {
			// Newly idle — mark as unvisited until user opens the agent view.
			a.idleUnvisited[id] = true
		}
	}
	for id := range a.idleUnvisited {
		if !newIdle[id] {
			// No longer idle (agent produced output again) — clear unvisited.
			delete(a.idleUnvisited, id)
		}
	}
	// If the user recently viewed a task's agent view, suppress the
	// idleUnvisited flag for it. Once the task goes active again (no longer
	// idle), clear the guard — a new idle transition will re-add to
	// idleUnvisited fresh.
	for id := range a.viewedWhileAgent {
		delete(a.idleUnvisited, id)
		if !newIdle[id] {
			delete(a.viewedWhileAgent, id)
		}
	}
	a.tasklist.SetTasks(a.tasks)
	a.tasklist.SetRunning(a.runningIDs)
	a.tasklist.SetIdle(idleIDs)
	a.syncIdleUnvisited()
	a.needsInputIDs = a.detectNeedsInputSticky(idleIDs, runningIDs, prevNeedsInput)
	a.tasklist.SetNeedsInput(a.needsInputIDs)
	// Feed the SAME authoritative needs-input set to the Hera rail so a blocked
	// worker shows "(?)" and the rollup bubbles it up to ancestor coordinators
	// (BUG-018). Cheap pure setter; the rebuild is scheduled below when the tab is
	// active. Always non-nil (created at construction); remote mode no-ops.
	a.heraPage.SetNeedsInput(a.needsInputIDs)
	a.tasklist.SetPRStates(a.readPRStates())
	heraWorkers, heraCoordinators := a.readHeraRoles()
	a.tasklist.SetHeraWorkers(heraWorkers)
	a.tasklist.SetHeraCoordinators(heraCoordinators)
	a.tasklist.SetManagedTasks(a.readManagedTasks())
	// Keep the Hera rail fresh while its tab is active (debounced inside the
	// page so rapid ticks coalesce to one rebuild). DB reads are mutex-guarded
	// and fast, so this is safe on the tview thread; we never run git here.
	// The second tab is ALWAYS the native Hera view now (cfg.Hera.Enabled only
	// gates daemon-side MCP tool registration), so this must NOT be gated on
	// that flag — doing so froze the rail/reconcile loop when hera.enabled=false.
	if a.header.ActiveTab() == widget.TabHera {
		a.heraPage.ScheduleRefresh()
		// Late-bind any coordinator/worker session that came up after the pane
		// was bound (main thread — SetSession is main-goroutine-only).
		a.heraPage.Reconcile()
		// Refresh the agent-staged clipboard hint for the focused terminal pane's
		// task (single RPC, same as the agent view's per-tick poll). Gates the
		// ctrl+y interception + drives the pane's "(ctrl+y copy)" affordance.
		a.refreshHeraClipboardHint()
	}
	a.updateAttentionBar()
	a.statusbar.SetTasks(a.tasks)
	a.statusbar.SetRunning(a.runningIDs)

	// Keep side panels in sync with cursor
	if a.mode == modeTaskList {
		t := a.tasklist.SelectedTask()
		if t != nil {
			a.taskPreview.SetTaskID(t.ID)
			a.taskDetail.SetTask(t, a.isTaskRunning(t.ID))
		} else {
			a.taskPreview.SetTaskID("")
			a.taskDetail.SetTask(nil, false)
		}
	}
}

// readPRStates reads the daemon-populated PR review state cache from task_meta
// namespace "pr" and returns a task ID → model.PRState map for the task list to
// render. The TUI NEVER invokes gh/gitutil.FetchPRState itself — the daemon
// poller is the sole writer; this is a pure cache read.
//
// Works in both modes via the store.Store interface: local mode (*db.DB) hits
// the SQLite index directly, while remote mode (*apistore.Store) reconstructs
// the "pr" namespace from the task list DTO's pr_state field. No type-assert.
func (a *App) readPRStates() map[string]model.PRState {
	raw, err := a.db.ListMetaByNamespace("pr")
	if err != nil {
		uxlog.Log("[pr] tui: read pr meta failed: %v", err)
		return nil
	}
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]model.PRState, len(raw))
	for taskID, kv := range raw {
		s, perr := model.ParsePRState(kv["state"])
		if perr != nil {
			continue // skip unparseable; leave that task's cell blank
		}
		out[taskID] = s
	}
	return out
}

// readHeraRoles reads the task_meta "hera" namespace ONCE and partitions bound
// tasks into the worker set (meta:hera.role=worker, stamped at born-bound spawn
// / auto-adopt in M4 — the task list hides these by default so the Tasks tab
// stays clean, with the `H` reveal toggle) and the coordinator set
// (meta:hera.role=coordinator, stamped when an orchestrator is created or a
// coordinator joins — the task list draws a coordinator glyph for these rows).
//
// A single query feeds both consumers so the tick doesn't hit the same
// namespace twice. Pure cache read; works in both modes via the store.Store
// interface (remote mode returns no "hera" rows, so both sets are empty — a
// safe degradation). A read error logs and returns nil, nil so nothing is
// hidden or marked.
func (a *App) readHeraRoles() (workers, coordinators map[string]bool) {
	raw, err := a.db.ListMetaByNamespace(db.HeraMetaNamespace)
	if err != nil {
		uxlog.Log("[hera-view] read hera meta failed: %v", err)
		return nil, nil
	}
	if len(raw) == 0 {
		return nil, nil
	}
	for taskID, kv := range raw {
		switch kv[db.HeraMetaKeyRole] {
		case string(db.HeraKindWorker):
			if workers == nil {
				workers = make(map[string]bool)
			}
			workers[taskID] = true
		case string(db.HeraKindCoordinator):
			if coordinators == nil {
				coordinators = make(map[string]bool)
			}
			coordinators[taskID] = true
		}
	}
	return workers, coordinators
}

// readManagedTasks returns the set of task IDs that currently hold at least one
// live hera binding (ended_at IS NULL) to a coordinator- or worker-kind role.
// Freelance-kind bindings do NOT count — a task is "managed" only when it is
// actively coordinated.
//
// Local mode: type-asserts a.db to *db.DB and queries ManagedTaskIDs()
// (authoritative; binding table is the single source of truth).
//
// Remote mode (--remote, a.db is *apistore.Store): no binding-query endpoint
// exists, so we fall back to the UNION of the worker + coordinator sets from
// readHeraRoles(). This is best-effort — task_meta hera.role is never cleared
// on binding end, so ended workers/coordinators may appear managed until the
// next full-row refresh. Documented in gotchas/tasklist-ui.md.
func (a *App) readManagedTasks() map[string]bool {
	if d, ok := a.db.(*db.DB); ok {
		ids, err := d.ManagedTaskIDs()
		if err != nil {
			uxlog.Log("[tui] readManagedTasks: query failed: %v", err)
			return nil
		}
		uxlog.Log("[tui] readManagedTasks: %d managed task(s)", len(ids))
		return ids
	}
	// Remote fallback: union worker + coordinator meta maps.
	uxlog.Log("[tui] readManagedTasks: remote mode, falling back to task_meta union")
	workers, coordinators := a.readHeraRoles()
	return mergeManagedFromMeta(workers, coordinators)
}

// mergeManagedFromMeta builds the managed-task set from the worker and
// coordinator maps returned by readHeraRoles(). It is the pure union of both
// maps and is the sole logic path for the remote fallback in readManagedTasks.
// Extracted so it can be unit-tested without wiring a full App or a remote
// store.
func mergeManagedFromMeta(workers, coordinators map[string]bool) map[string]bool {
	if len(workers) == 0 && len(coordinators) == 0 {
		return nil
	}
	out := make(map[string]bool, len(workers)+len(coordinators))
	for id := range workers {
		out[id] = true
	}
	for id := range coordinators {
		out[id] = true
	}
	return out
}

// pluginFailsafeWindow is the maximum gap between two Ctrl+Q presses for the
// second to fire the failsafe (force-return to argus). A first Ctrl+Q is always
// forwarded to the plugin; only a second within this window is intercepted.
const pluginFailsafeWindow = 400 * time.Millisecond

// handleGlobalKey processes key events at the application level.
// heraPaneFocused reports whether the Hera tab is active AND focus is inside one
// of its content panes (coordinator or agent/details), not the rail. When true,
// the global key handler must surrender the keys it would otherwise consume
// (`q` quit, `1`/`2`/`3` tab-switch, `?` help, `Ctrl+C` quit, `Ctrl+L` Sync) so
// they fall through to HeraPage.InputHandler, which forwards them to the focused
// pane's PTY. Without this, typing into a focused Hera worker/coordinator pane
// leaks rail/global shortcuts — e.g. `q` quits all of argus (BUG-001). The rail
// itself is NOT a content pane, so those globals still work while the rail holds
// focus. Mirrors how modeAgent fully surrenders these keys to the agent PTY.
func (a *App) heraPaneFocused() bool {
	return a.header.ActiveTab() == widget.TabHera &&
		a.heraPage != nil && !a.heraPage.IsRemote() &&
		a.heraPage.Machine().State() != hera.FocusRail
}

func (a *App) handleGlobalKey(event *tcell.EventKey) *tcell.EventKey {
	// Plugin-view mode — full surrender. While a plugin has the ball, argus
	// reserves NO key for its own navigation: Esc, Ctrl+C, `?`, tab-switch
	// numbers, focus-rail arrows — all forward to the plugin via the focused
	// pane's InputHandler. The ONE exception is the double-Ctrl+Q failsafe
	// (Decision 3) so a hung plugin can't trap the keyboard. See
	// plugin_views.go.
	if a.mode == modePluginView {
		// Plugin-triggered help overlay: while it is visible, argus consumes
		// exactly the NEXT key to dismiss it and hand control back to the
		// plugin. This is the ONLY key (besides the double-Ctrl+Q failsafe)
		// argus intercepts in plugin mode — the overlay does not capture the
		// keyboard beyond this single dismissal.
		if a.pluginHelpVisible {
			a.dismissPluginHelp()
			return nil
		}
		// While reconnecting, the plugin is GONE — full surrender no longer
		// applies (there's nothing to forward to). A single Esc exits to the
		// task list, alongside the double-Ctrl+Q failsafe. When the plugin is
		// live, Esc falls through and forwards to the plugin (unchanged).
		if a.activePlugin != nil && a.activePlugin.reconnecting && event.Key() == tcell.KeyEscape {
			uxlog.Log("[plugin-view] esc during reconnect — exit to task list")
			a.deactivatePluginView()
			return nil
		}
		if event.Key() == tcell.KeyCtrlQ {
			now := a.nowFn()
			if !a.lastCtrlQ.IsZero() && now.Sub(a.lastCtrlQ) <= pluginFailsafeWindow {
				// Second fast Ctrl+Q — intercept, do NOT forward, force-return.
				uxlog.Log("[plugin-view] failsafe fired: double Ctrl+Q force-return")
				a.lastCtrlQ = time.Time{}
				a.deactivatePluginView()
				return nil
			}
			// First Ctrl+Q (or one outside the window) — record and forward so
			// the plugin receives it.
			a.lastCtrlQ = now
			return event
		}
		return event
	}

	// Plugin-view hotkey activation — checked before form handlers so the
	// hotkey works from any non-modal context. tview.Pages already routes
	// the form modes via earlier branches below, so they get to absorb the
	// keystroke first when active.
	if event.Key() != tcell.KeyRune {
		if m, ok := a.pluginHotkeys[event.Key()]; ok && a.mode == modeTaskList {
			a.activatePluginView(m)
			return nil
		}
	}

	// New task form mode — delegate everything to the form
	if a.mode == modeNewTask && a.newTaskForm != nil {
		a.handleNewTaskKey(event)
		return nil
	}

	// Confirm delete modal — delegate everything to the modal
	if a.mode == modeConfirmDelete && a.confirmDeleteModal != nil {
		a.handleConfirmDeleteKey(event)
		return nil
	}

	// Help overlay — delegate everything to the modal
	if a.mode == modeHelp && a.helpModal != nil {
		a.handleHelpKey(event)
		return nil
	}

	// Error modal — any key dismisses it.
	if a.mode == modeErrorModal && a.errorModal != nil {
		a.handleErrorModalKey(event)
		return nil
	}

	// Confirm delete project modal
	if a.mode == modeConfirmDeleteProject && a.confirmDeleteProjectModal != nil {
		a.handleConfirmDeleteProjectKey(event)
		return nil
	}

	// Confirm prune-completed modal (Ctrl+R caution gate).
	if a.mode == modeConfirmPrune && a.confirmPruneModal != nil {
		a.handleConfirmPruneKey(event)
		return nil
	}

	// Restart-daemon prompt — shown on startup when daemon binary is stale.
	if a.mode == modeRestartDaemonPrompt && a.restartDaemonModal != nil {
		a.handleRestartDaemonKey(event)
		return nil
	}

	// Restart-supervisor caution gate (Settings → System).
	if a.mode == modeConfirmRestartSupervisor && a.restartSupervisorModal != nil {
		a.handleRestartSupervisorKey(event)
		return nil
	}

	// Project form mode — delegate everything to the form
	if a.mode == modeProjectForm && a.projectForm != nil {
		a.handleProjectFormKey(event)
		return nil
	}

	// AppleEvents picker modal — delegate everything to the modal
	if a.mode == modeAppleEventsPicker && a.appleEventsPicker != nil {
		a.handleAppleEventsPickerKey(event)
		return nil
	}

	// Schedule form mode — delegate everything to the form
	if a.mode == modeScheduleForm && a.scheduleForm != nil {
		a.handleScheduleFormKey(event)
		return nil
	}

	// Quick-add form mode — delegate everything to the form
	if a.mode == modeQuickAdd && a.quickAddForm != nil {
		a.handleQuickAddKey(event)
		return nil
	}

	// Fork task modal — delegate everything to the modal
	if a.mode == modeForkTask && a.forkModal != nil {
		a.handleForkTaskKey(event)
		return nil
	}

	// Link picker modal
	if a.mode == modeLinkPicker && a.linkPickerModal != nil {
		a.handleLinkPickerKey(event)
		return nil
	}

	// Fuzzy link picker modal (agent view)
	if a.mode == modeFuzzyLinkPicker && a.fuzzyLinkPickerModal != nil {
		a.handleFuzzyLinkPickerKey(event)
		return nil
	}

	// Session picker modal (agent view)
	if a.mode == modeSessionPicker && a.sessionPickerModal != nil {
		a.handleSessionPickerKey(event)
		return nil
	}

	// Task switcher modal (agent view)
	if a.mode == modeTaskSwitcher && a.taskSwitcherModal != nil {
		a.handleTaskSwitcherKey(event)
		return nil
	}

	// Rename task modal — delegate everything to the modal
	if a.mode == modeRenameTask && a.renameModal != nil {
		a.handleRenameTaskKey(event)
		return nil
	}

	// Hera-view input modal (rename / spawn prompt) — delegate to the modal.
	if a.mode == modeHeraInput && a.heraInputModal != nil {
		a.handleHeraInputKey(event)
		return nil
	}

	// Hera-view confirm modal (archive-of-live / delete) — delegate to the modal.
	if a.mode == modeHeraConfirm && a.heraConfirmModal != nil {
		a.handleHeraConfirmKey(event)
		return nil
	}

	// Hera-view `J` adopt/reparent orchestrator picker — delegate to the modal.
	if a.mode == modeHeraOrchPicker && a.heraOrchPicker != nil {
		a.handleHeraOrchPickerKey(event)
		return nil
	}

	switch event.Key() {
	case tcell.KeyCtrlC:
		if a.mode == modeAgent {
			// Forward ctrl+c to the PTY if session is alive; otherwise ignore
			if sess := a.agentPane.Session(); sess != nil && sess.Alive() {
				if _, err := sess.WriteInput([]byte{0x03}); err != nil {
					uxlog.Log("[tui] write ctrl+c to PTY failed: %v", err)
				}
			}
			return nil
		}
		// A focused Hera pane must receive ^C (interrupt the agent), not quit
		// argus — fall through to HeraPage so it forwards 0x03 to the pane PTY.
		if a.heraPaneFocused() {
			break
		}
		a.tapp.Stop()
		return nil
	case tcell.KeyCtrlL:
		// Manual refresh — force a full screen re-emit to wipe ghost
		// cells that the diff-based Show() failed to overwrite. Only
		// active outside agent view; in agent mode we fall through so
		// handleAgentKey's Ctrl+L → link-picker binding runs instead.
		// User-initiated; one CSI 2J flash is the expected cost. A focused Hera
		// pane also falls through so ^L reaches its PTY.
		if a.mode != modeAgent && !a.heraPaneFocused() {
			uxlog.Log("[tui] ctrl+l — Sync")
			a.screen.Sync()
			return nil
		}
	case tcell.KeyCtrlQ:
		if a.mode == modeAgent {
			// 3-level exit: diff → files panel → agent view
			if a.agentPane.InDiffMode() {
				a.agentPane.ExitDiffMode()
				a.agentFocus = focusTerminal
				a.updateFocusIndicators()
				return nil
			}
			if a.agentFocus == focusFiles {
				a.agentFocus = focusTerminal
				a.updateFocusIndicators()
				return nil
			}
			a.exitAgentView()
			return nil
		}
	case tcell.KeyCtrlD:
		if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks {
			if t := a.tasklist.SelectedTask(); t != nil {
				a.openConfirmDelete(t)
				return nil
			}
		}
	case tcell.KeyCtrlF:
		if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks {
			if t := a.tasklist.SelectedTask(); t != nil && t.Worktree != "" {
				a.openForkModal(t)
				return nil
			}
		}
	case tcell.KeyCtrlO:
		if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks {
			if t := a.tasklist.SelectedTask(); t != nil {
				dir := ""
				if p, ok := a.db.Config().Projects[t.Project]; ok && p.Path != "" {
					dir = p.Path
				} else if t.Worktree != "" {
					dir = t.Worktree
				}
				if dir != "" {
					if err := repoOpener(dir); err != nil {
						uxlog.Log("[tui] open repo failed: %v", err)
					}
					return nil
				}
			}
		}
	case tcell.KeyCtrlP:
		if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks {
			if t := a.tasklist.SelectedTask(); t != nil && t.Worktree != "" {
				if err := prOpener(t.Worktree); err != nil {
					uxlog.Log("[tui] open PR failed: %v", err)
				}
				return nil
			}
		}
	case tcell.KeyCtrlR:
		if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks {
			a.openConfirmPrune()
			return nil
		}
	// NOTE: Left/Right are intentionally NOT handled here. They used to cycle
	// the top-level tabs, but that collided with horizontal navigation inside
	// views (e.g. the Hera rail's rail↔coord↔details movement). Tab switching
	// is now via 1/2/3 only; Left/Right fall through to the focused view. On the
	// Settings tab the settings routing below consumes the directions it uses
	// for rail↔pane focus (Right from the rail, Left from the pane); the
	// others (e.g. Left from the rail) simply fall through and never switch tabs.
	case tcell.KeyRune:
		// When the task list filter or settings prompt editor is active,
		// let all rune keys through instead of handling global shortcuts.
		if a.mode == modeTaskList && a.tasklist.Filtering() {
			break
		}
		if a.mode == modeTaskList && a.settings.IsEditing() {
			break
		}
		// A focused Hera pane is a live terminal — every rune must reach its PTY,
		// so don't intercept any global rune shortcut (q quit / 1·2·3 tab-switch /
		// ? help) while a Hera content pane holds focus (BUG-001). The rail still
		// gets these globals because heraPaneFocused() is false on the rail.
		if a.heraPaneFocused() {
			break
		}
		// While the Hera rail is in `/` search input mode, every rune is filter
		// input — don't run the 1/2/3 tab-switch, q quit, or ? help shortcuts.
		if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabHera && a.heraPage.RailFiltering() {
			break
		}
		switch event.Rune() {
		case 'q':
			if a.mode == modeTaskList {
				a.tapp.Stop()
				return nil
			}
		case '1':
			if a.mode != modeAgent {
				a.switchTab(widget.TabTasks)
				return nil
			}
		case '2':
			if a.mode != modeAgent {
				a.switchTab(widget.TabHera)
				return nil
			}
		case '3':
			if a.mode != modeAgent {
				a.switchTab(widget.TabSettings)
				return nil
			}
		case '?':
			if a.mode != modeAgent {
				a.openHelp()
				return nil
			}
		}
	}

	switch a.mode {
	case modeAgent:
		return a.handleAgentKey(event)
	}

	// Settings tab key routing.
	if a.header.ActiveTab() == widget.TabSettings {
		if a.settings.HandleKey(event) {
			return nil
		}
	}

	return event
}

// updateFocusIndicators syncs border styles with the current focus state.
func (a *App) updateFocusIndicators() {
	a.agentPane.SetFocused(a.agentFocus == focusTerminal)
	a.filePanel.SetFocused(a.agentFocus == focusFiles)
}

// clearAgentZen turns single-pane (zoom) mode off and restores the 1:3:1 agent
// layout. Idempotent — safe to call when zen is already off (the ResizeItem
// calls just re-assert the proportional sizing). Reached from App setup, both
// agent-view entry points, and exitAgentView via applyDefaultAgentZen when
// ui.default_agent_zoom is false, and directly from toggleAgentZen's un-zoom
// branch. Main goroutine only.
func (a *App) clearAgentZen() {
	a.agentZen = false
	a.agentPanels.ResizeItem(a.agentLeftCol, 0, 1)
	a.agentPanels.ResizeItem(a.filePanel, 0, 1)
}

// setAgentZen turns single-pane (zoom) mode on: the left column (attention bar
// + git) and the file panel collapse to zero width so the agent terminal fills
// the whole pane row. Idempotent. Reached from App setup, both agent-view entry
// points (enterPendingAgentView, onTaskSelect), and exitAgentView via
// applyDefaultAgentZen when ui.default_agent_zoom is true (the default), and
// directly from toggleAgentZen's zoom branch.
//
// The focus guard snaps focus back to the terminal because the file panel is
// hidden at zero width — leaving focus there would silently swallow keys with no
// visible target. In practice the guard only does work when called from
// toggleAgentZen (the user may be on the file panel when they press Ctrl+Z);
// every other caller has already set agentFocus=focusTerminal, so it's a no-op
// there. Main goroutine only.
func (a *App) setAgentZen() {
	a.agentZen = true
	a.agentPanels.ResizeItem(a.agentLeftCol, 0, 0)
	a.agentPanels.ResizeItem(a.filePanel, 0, 0)
	if a.agentFocus != focusTerminal {
		a.agentFocus = focusTerminal
		a.updateFocusIndicators()
	}
}

// applyDefaultAgentZen sets the resting agent-view layout to the user's
// configured default: zoomed (single-pane) when ui.default_agent_zoom is true
// (the default), or the 1:3:1 three-pane layout otherwise. Shared by App setup,
// both agent-view entry points (enterPendingAgentView, onTaskSelect), and
// exitAgentView so the agentZen flag and panel proportions always match the
// configured default before/after each agent-view session. Ctrl+Z still toggles
// at runtime regardless of the default. Main goroutine only.
func (a *App) applyDefaultAgentZen() {
	if a.db.Config().UI.DefaultAgentZoom {
		a.setAgentZen()
	} else {
		a.clearAgentZen()
	}
}

// toggleAgentZen flips single-pane (zoom) mode in the agent view. When on, the
// left column (attention bar + git) and the file panel collapse to zero width
// so the agent terminal fills the whole pane row; when off, the 1:3:1 layout is
// restored. The terminal pane's Draw() recomputes the PTY size from its own
// inner rect every frame, so the resize RPC (and the agent's full-width
// repaint) fires automatically — no computePTYSize change or manual Resize.
func (a *App) toggleAgentZen() {
	if a.agentZen {
		// Un-zoom keeps terminal focus: there is no prior-focus tracking, and
		// the user is typically still working in the terminal after a zoom.
		a.clearAgentZen()
	} else {
		a.setAgentZen()
	}
	uxlog.Log("[tui] agent zen mode toggled: %v", a.agentZen)
}

// handleAgentKey handles keys when the agent view is active.
func (a *App) handleAgentKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEscape:
		// Escape refocuses terminal from diff/files, but does NOT exit agent view
		if a.agentPane.InDiffMode() {
			a.agentPane.ExitDiffMode()
			a.agentFocus = focusTerminal
			a.updateFocusIndicators()
			return nil
		}
		if a.agentFocus == focusFiles {
			a.agentFocus = focusTerminal
			a.updateFocusIndicators()
			return nil
		}
		// When focused on terminal, forward escape to PTY if alive, otherwise consume it
		if sess := a.agentPane.Session(); sess != nil && sess.Alive() {
			if _, err := sess.WriteInput([]byte{0x1b}); err != nil {
				uxlog.Log("[tui] write escape to PTY failed: %v", err)
			}
			a.agentPane.ResetScroll()
		}
		return nil
	case tcell.KeyCtrlL: // Overrides typical "clear screen" — intercepted before PTY
		a.openAgentLinks()
		return nil
	case tcell.KeyCtrlR: // Switch Claude session — intercepted before PTY (shadows Claude's transcript toggle)
		a.openSessionPicker()
		return nil
	case tcell.KeyCtrlK: // Open task switcher — intercepted before PTY (shadows readline kill-line)
		a.openTaskSwitcher()
		return nil
	case tcell.KeyCtrlP: // Open PR for the worktree's branch via gh
		a.openPR()
		return nil
	case tcell.KeyCtrlZ:
		// Toggle single-pane (zoom) view: collapse/restore side panels.
		// Intercepted here so it never reaches the PTY — otherwise Claude
		// Code would background the foreground task on Ctrl+Z (0x1a / SIGTSTP).
		a.toggleAgentZen()
		return nil
	case tcell.KeyCtrlY:
		// Conditional intercept: only steal ctrl+y from the PTY when an
		// agent has staged a clipboard payload. Without a payload, fall
		// through so `vim`/`emacs` style yank still reaches the agent.
		if a.copyStagedClipboard() {
			return nil
		}
	case tcell.KeyLeft:
		if event.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			// Zoomed view is single-pane — the side panels are collapsed to zero
			// width, so a pane switch would move focus to an invisible panel and
			// silently swallow keys. Consume the key without changing panes.
			if !a.agentZen && a.agentFocus > focusTerminal {
				a.agentFocus--
				a.updateFocusIndicators()
			}
			return nil
		}
	case tcell.KeyRight:
		if event.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			if !a.agentZen && a.agentFocus < focusFiles {
				a.agentFocus++
				a.updateFocusIndicators()
			}
			return nil
		}
	case tcell.KeyUp:
		if event.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			a.navigateAgentTask(-1)
			return nil
		}
	case tcell.KeyDown:
		if event.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			a.navigateAgentTask(1)
			return nil
		}
	}

	// Diff mode keys
	if a.agentPane.InDiffMode() {
		return a.handleDiffKey(event)
	}

	// File panel navigation
	if a.agentFocus == focusFiles {
		return a.handleFilePanelKey(event)
	}

	sess := a.agentPane.Session()

	// Scrollback keys
	if event.Modifiers()&tcell.ModShift != 0 {
		switch event.Key() {
		case tcell.KeyUp:
			a.agentPane.AccelScrollUp()
			return nil
		case tcell.KeyDown:
			a.agentPane.AccelScrollDown()
			return nil
		case tcell.KeyPgUp:
			a.agentPane.ScrollUp(20)
			return nil
		case tcell.KeyPgDn:
			a.agentPane.ScrollDown(20)
			return nil
		case tcell.KeyEnd:
			a.agentPane.ResetScroll()
			return nil
		}
	}

	// When session is finished, ctrl+d exits agent view (same as ctrl+q/esc)
	if event.Key() == tcell.KeyCtrlD && (sess == nil || !sess.Alive()) {
		a.exitAgentView()
		return nil
	}

	// Enter restarts/resumes the session when dead.
	if event.Key() == tcell.KeyEnter && (sess == nil || !sess.Alive()) {
		a.mu.Lock()
		taskID := a.agentState.TaskID
		a.mu.Unlock()
		if t, err := a.db.Get(taskID); err == nil && t != nil {
			a.startSession(t)
		} else {
			uxlog.Log("[tui] enter-to-restart: db.Get(%s) failed: %v", taskID, err)
		}
		return nil
	}

	// Reset scroll on any other key
	if a.agentPane.ScrollOffset() > 0 {
		a.agentPane.ResetScroll()
	}

	// Forward to PTY
	if sess != nil && sess.Alive() {
		b := tcellKeyToBytes(event)
		if len(b) > 0 {
			if _, err := sess.WriteInput(b); err != nil {
				uxlog.Log("[tui] write to PTY failed: %v", err)
			}
			// Schedule a fast follow-up redraw to paint the PTY echo.
			// The immediate tview draw (from returning nil) fires before
			// the echo arrives (~1-5ms). Without this, the echo waits
			// up to 200ms for the redraw loop poll — visible as typing lag.
			tw := sess.TotalWritten()
			go func() {
				time.Sleep(16 * time.Millisecond)
				if sess.TotalWritten() != tw {
					a.tapp.QueueUpdateDraw(func() {})
				}
			}()
			return nil
		}
	}

	return event
}

// handleFilePanelKey handles keys when the file panel has focus.
func (a *App) handleFilePanelKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyUp:
		if dir := a.filePanel.CursorUp(); dir != "" {
			go a.fetchDirChildren(dir)
		}
		return nil
	case tcell.KeyDown:
		if dir := a.filePanel.CursorDown(); dir != "" {
			go a.fetchDirChildren(dir)
		}
		return nil
	case tcell.KeyEnter:
		// Open diff for selected file
		a.openFileDiff()
		return nil
	case tcell.KeyRune:
		switch event.Rune() {
		case 'j':
			if dir := a.filePanel.CursorDown(); dir != "" {
				go a.fetchDirChildren(dir)
			}
			return nil
		case 'k':
			if dir := a.filePanel.CursorUp(); dir != "" {
				go a.fetchDirChildren(dir)
			}
			return nil
		case 'f':
			a.openInFinder()
			return nil
		case 'o':
			a.openFile()
			return nil
		case 'e':
			a.openInEditor()
			return nil
		case 't':
			a.openTerminal()
			return nil
		}
	}
	return event
}

// handleDiffKey handles keys when viewing a diff.
func (a *App) handleDiffKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyUp:
		// Navigate to previous file's diff.
		if dir := a.filePanel.CursorUp(); dir != "" {
			go a.fetchDirChildren(dir)
		}
		a.openFileDiff()
		return nil
	case tcell.KeyDown:
		// Navigate to next file's diff.
		if dir := a.filePanel.CursorDown(); dir != "" {
			go a.fetchDirChildren(dir)
		}
		a.openFileDiff()
		return nil
	case tcell.KeyPgUp:
		a.agentPane.DiffScrollUp(20)
		return nil
	case tcell.KeyPgDn:
		a.agentPane.DiffScrollDown(20)
		return nil
	case tcell.KeyRune:
		switch event.Rune() {
		case 's':
			a.agentPane.ToggleDiffSplit()
			return nil
		case 'q':
			a.agentPane.ExitDiffMode()
			a.agentFocus = focusTerminal
			a.updateFocusIndicators()
			return nil
		case 'j':
			a.agentPane.DiffScrollDown(1)
			return nil
		case 'k':
			a.agentPane.DiffScrollUp(1)
			return nil
		}
	}
	return nil
}

// fetchGitStatus runs git status asynchronously and updates the panels.
func (a *App) fetchGitStatus(taskID, dir string) {
	msg := gitutil.FetchGitStatus(taskID, dir)
	a.tapp.QueueUpdateDraw(func() {
		if taskID != a.agentState.TaskID {
			return
		}
		a.lastGitRefresh = time.Now()
		a.gitPanel.SetStatus(msg.Status, msg.Diff, msg.BranchFiles)
		// Merge committed + uncommitted files
		files := gitutil.MergeChangedFiles(
			gitutil.ParseGitDiffNameStatus(msg.BranchFiles),
			gitutil.ParseGitStatus(msg.Status),
		)
		if dir := a.filePanel.SetFiles(files); dir != "" {
			go a.fetchDirChildren(dir)
		}
		uxlog.Log("[tui] git status refreshed: %d files", len(files))
	})
}

// fetchDirChildren fetches directory children asynchronously.
func (a *App) fetchDirChildren(dirPath string) {
	taskID := a.agentState.TaskID
	dir := a.worktreeDir
	msg := gitutil.FetchDirFiles(taskID, dir, dirPath)
	a.tapp.QueueUpdateDraw(func() {
		if taskID != a.agentState.TaskID {
			return
		}
		a.filePanel.SetDirChildren(msg.DirPath, msg.Files)
	})
}

// openFileDiff fetches the diff for the selected file and enters diff mode.
func (a *App) openFileDiff() {
	f := a.filePanel.SelectedFile()
	if f == nil || a.worktreeDir == "" {
		return
	}
	filePath := f.Path
	dir := a.worktreeDir
	go func() {
		msg := gitutil.FetchFileDiff(a.agentState.TaskID, dir, filePath)
		a.tapp.QueueUpdateDraw(func() {
			if msg.TaskID != a.agentState.TaskID {
				return
			}
			if msg.Diff != "" {
				a.agentPane.EnterDiffMode(msg.Diff, msg.FilePath)
			}
		})
	}()
}

// systemOpener is the package-level seam for the macOS `open` command. Tests
// stub this out so they don't actually launch Finder or a default app. `open`
// with `-R` reveals a path in Finder; without `-R` it opens the path with its
// default system handler.
var systemOpener = func(args ...string) error {
	return exec.Command("open", args...).Start()
}

// openInFinder reveals the selected file in Finder (bound to `f`).
func (a *App) openInFinder() {
	f := a.filePanel.SelectedFile()
	if f == nil || a.worktreeDir == "" {
		return
	}
	if err := systemOpener("-R", a.worktreeDir+"/"+f.Path); err != nil {
		uxlog.Log("[tui] reveal in Finder failed: %v", err)
	}
}

// openFile opens the selected file with its default system handler (bound to
// `o`).
func (a *App) openFile() {
	f := a.filePanel.SelectedFile()
	if f == nil || a.worktreeDir == "" {
		return
	}
	if err := systemOpener(a.worktreeDir + "/" + f.Path); err != nil {
		uxlog.Log("[tui] open file failed: %v", err)
	}
}

// editorArgv builds the tmux argv for opening a file in a new window running
// $EDITOR. It falls back to `vi` when $EDITOR is empty/unset, and splits the
// editor value on whitespace so values like `code -w` or `nvim -p` work. The
// `--` guards against the file path or editor flags being parsed as tmux
// options. Pulled out as a pure function so the resolution logic is testable
// without shelling out to tmux.
//
// Limitation: shell-style quoting in $EDITOR is NOT interpreted — splitting is
// plain whitespace via strings.Fields, so a value like `emacsclient -a ""`
// yields the literal token `""` rather than an empty argument. The common
// real-world forms (`vi`, `code -w`, `nvim -p`) all use unquoted whitespace and
// work correctly; anything relying on shell quoting must wrap itself in a
// script on $PATH and set $EDITOR to that script's name.
func editorArgv(editor, worktreeDir, path string) []string {
	if strings.TrimSpace(editor) == "" {
		editor = "vi"
	}
	args := append([]string{"new-window", "-c", worktreeDir, "--"}, strings.Fields(editor)...)
	return append(args, worktreeDir+"/"+path)
}

// editorOpener is the package-level seam for "open file in a new tmux window
// running $EDITOR". Tests stub this out so they don't actually spawn tmux
// windows.
var editorOpener = func(worktreeDir, path string) error {
	return exec.Command("tmux", editorArgv(os.Getenv("EDITOR"), worktreeDir, path)...).Start()
}

// openInEditor opens the selected file in a new tmux window running $EDITOR
// (bound to `e`).
func (a *App) openInEditor() {
	f := a.filePanel.SelectedFile()
	if f == nil || a.worktreeDir == "" {
		return
	}
	if err := editorOpener(a.worktreeDir, f.Path); err != nil {
		uxlog.Log("[tui] open in editor failed: %v", err)
	}
}

// terminalOpener is the package-level seam for "open shell in worktree dir
// in a new tmux window". Tests stub this out so they don't actually spawn
// tmux windows.
var terminalOpener = func(worktreeDir string) error {
	return exec.Command("tmux", "new-window", "-c", worktreeDir).Start()
}

// prOpener is the package-level seam for "open the PR for this worktree's
// branch in a browser via gh". Tests stub this out so they don't actually
// shell out. gh discovers the PR from the current branch + remote, so no
// PR URL needs to be tracked locally.
var prOpener = func(worktreeDir string) error {
	cmd := exec.Command("gh", "pr", "view", "--web")
	cmd.Dir = worktreeDir
	return cmd.Start()
}

// repoOpener is the package-level seam for "open the GitHub repo page for
// this project in a browser via gh". gh resolves the URL from the local
// remote, so any directory inside the repo (project root or worktree) works.
var repoOpener = func(dir string) error {
	cmd := exec.Command("gh", "repo", "view", "--web")
	cmd.Dir = dir
	return cmd.Start()
}

func (a *App) openTerminal() {
	if a.worktreeDir == "" {
		return
	}
	if err := terminalOpener(a.worktreeDir); err != nil {
		uxlog.Log("[tui] open terminal failed: %v", err)
	}
}

func (a *App) openPR() {
	if a.worktreeDir == "" {
		return
	}
	if err := prOpener(a.worktreeDir); err != nil {
		uxlog.Log("[tui] open PR failed: %v", err)
	}
}

// tcellKeyToBytes converts a tcell key event to raw terminal bytes for PTY
// input. It delegates to the shared keyenc.Encode so the agent PTY, the
// plugin terminal pane, and the plugin stream pane all encode keys
// identically. keyenc additionally emits the modified-arrow xterm forms
// (Ctrl/Shift/Alt+arrow) that the prior pane encoders dropped.
func tcellKeyToBytes(ev *tcell.EventKey) []byte {
	return keyenc.Encode(ev)
}

// switchTab changes the active top-level tab.
func (a *App) switchTab(t widget.Tab) {
	a.header.SetTab(t)
	a.statusbar.SetTab(t)

	switch t {
	case widget.TabTasks:
		if a.mode == modeAgent {
			// exitAgentView is a complete "return to tasks" primitive: resets
			// mode, tab state, page, and focus. Early return skips the
			// SwitchToPage below.
			a.exitAgentView()
			return
		}
		a.mode = modeTaskList
		a.pages.SwitchToPage("tasks")
		a.tapp.SetFocus(a.tasklist)
	case widget.TabHera:
		a.mode = modeTaskList
		a.switchToHeraTab2()
	case widget.TabSettings:
		a.mode = modeTaskList
		a.settings.Refresh()
		a.pages.SwitchToPage("settings")
		a.tapp.SetFocus(a.settingsPage)
	}
}

// switchToHeraTab2 routes the second tab slot to the native Hera view, updates
// the tab label, and refreshes the rail. Called from switchTab. Must run on the
// tview main goroutine.
func (a *App) switchToHeraTab2() {
	a.header.SetTabLabel(widget.TabHera, "Hera")
	// Tab entry always starts with the rail focused; reset the statusbar hint
	// set so the operator sees rail hints immediately (the rail is the default
	// region — no focus-machine state persists across tab switches).
	a.statusbar.SetHeraFocus(0)
	a.heraPage.Refresh()
	a.pages.SwitchToPage("hera")
	a.tapp.SetFocus(a.heraPage)
	uxlog.Log("[hera-view] tab 2 routed to hera")
}

// focusHeraTab2Page switches to the Hera page without triggering a refresh.
// Used by modal-close paths that need to restore the page after closing a
// modal while on the Hera tab.
func (a *App) focusHeraTab2Page() {
	a.pages.SwitchToPage("hera")
	a.tapp.SetFocus(a.heraPage)
}

// forceRedraw logs the named transition. It does NOT trigger a tcell Sync
// or otherwise mutate the screen — that was the wrong primitive for almost
// every callsite here (tcell.Sync emits CSI 2J which tmux propagates as a
// visible flash; tcell.Show()'s per-cell diff is what's actually needed for
// these cases). The log entry preserves a debug trail for "what transitions
// fired this draw cycle" — useful when chasing future drift reports.
//
// The two scenarios where we genuinely DO want a Sync (repair-screen-damage
// per gdamore's intent) are wired to call `a.screen.Sync()` directly,
// outside this helper: focus regain (lazyScreen.PollEvent → onFocusGained)
// and Ctrl+L (user-initiated refresh). Both are rare, and one CSI 2J flash
// per occurrence is acceptable. See gotchas/ui-threading.md for the full
// post-mortem on why every previous "tearing fix" was self-inflicted.
func (a *App) forceRedraw(reason string) {
	uxlog.Log("[tui] force redraw: %s", reason)
}

// onTaskCursorChange updates the preview, git status, and detail panels when the task list cursor moves.
func (a *App) onTaskCursorChange(task *model.Task) {
	if task == nil {
		a.taskPreview.SetTaskID("")
		a.taskDetail.SetTask(nil, false)
		a.taskGitPanel.Clear()
		return
	}
	a.taskPreview.SetTaskID(task.ID)
	a.taskDetail.SetTask(task, a.isTaskRunning(task.ID))
	// Kick off preview fetch immediately (don't wait for next tick).
	go func() {
		a.refreshPreview(task.ID)
		a.tapp.QueueUpdateDraw(func() {}) // trigger redraw with new cells
	}()
	if task.Worktree != "" {
		a.taskGitPanel.Clear()
		go a.fetchTaskGitStatus(task.ID, task.Worktree)
	} else {
		a.taskGitPanel.Clear()
	}
}

// fetchTaskGitStatus runs git status for a task's worktree and updates the task git panel.
func (a *App) fetchTaskGitStatus(taskID, dir string) {
	msg := gitutil.FetchGitStatus(taskID, dir)
	a.tapp.QueueUpdateDraw(func() {
		// Only update if we're still viewing this task.
		sel := a.tasklist.SelectedTask()
		if sel == nil || sel.ID != taskID {
			return
		}
		a.taskGitPanel.SetStatus(msg.Status, msg.Diff, msg.BranchFiles)
	})
}

// previewEmuSize picks the VT emulator dimensions for a preview render.
// Preference order: live PTY size, persisted session-size sidecar
// (agent.LoadSessionSize — survives session exit), pane dimensions as the
// last resort. The agent's PTY is routinely much wider than the preview
// pane; re-emulating its absolute cursor positioning (CSI nG / CSI nC) in
// a pane-width emulator clamps columns at the right edge and autowraps the
// tail — the "scrambled preview" defect. Emulate at the session's real
// width and let RefreshOutput clip to the pane instead. Returns the chosen
// source for uxlog. See gotchas/pty-terminal.md.
func previewEmuSize(taskID string, ptyCols, ptyRows, paneW, paneH int) (emuCols, emuRows int, src string) {
	if ptyCols > 0 {
		emuRows = paneH
		if ptyRows > 0 {
			emuRows = ptyRows
		}
		return ptyCols, emuRows, "pty"
	}
	if cols, rows, ok := agent.LoadSessionSize(taskID); ok {
		return cols, rows, "sizefile"
	}
	return paneW, paneH, "pane"
}

// refreshPreview fetches output for the selected task and pre-renders cells.
// Called from the tview main goroutine (via QueueUpdateDraw in onTick).
// The TotalWritten/LogSize cache short-circuits on repeated calls; first
// load of a large dead session may briefly block the UI.
//
// It must NEVER resize the agent's real PTY — a Resize RPC here would
// SIGWINCH live agents as the user scrolls the task list. Width mismatch is
// resolved on the emulation side only (previewEmuSize).
func (a *App) refreshPreview(taskID string) {
	w, h := a.taskPreview.DrawSize()
	if w <= 0 || h <= 0 {
		return
	}

	sess := a.runner.Get(taskID)
	if sess != nil {
		// Snapshot the ring tail and high-water mark atomically — the preview
		// emulator advances incrementally off `total`, so reading the tail and
		// total separately would let readLoop slip bytes between them.
		raw, tw := sess.RecentOutputTailWithTotal(256 * 1024)
		// Skip the refresh when output hasn't changed.
		// Protected by a.mu — accessed from tick goroutine and onTaskCursorChange goroutine.
		a.mu.Lock()
		taskChanged := taskID != a.lastPreviewTaskID
		if !taskChanged && tw == a.lastPreviewTW {
			a.mu.Unlock()
			return
		}
		a.lastPreviewTaskID = taskID
		a.lastPreviewTW = tw
		a.lastPreviewLogSize = 0 // reset so dead-session path re-reads log after session exit
		a.mu.Unlock()
		// Use the PTY's actual dimensions for the emulator so cursor
		// positioning and text wrapping match the agent view. The preview
		// viewport (w x h) selects which rows to display.
		ptyCols, ptyRows := sess.PTYSize()
		emuCols, emuRows, src := previewEmuSize(taskID, ptyCols, ptyRows, w, h)
		// Log on task switch always, and on every refresh that had to fall
		// back past the live PTY size (RPC failure / stale info) — the
		// fallback is the path that can scramble, so it must leave a trail.
		if taskChanged || src != "pty" {
			uxlog.Log("[tui] preview: live task=%s emu=%dx%d (%s) view=%dx%d raw=%d total=%d", taskID, emuCols, emuRows, src, w, h, len(raw), tw)
		}
		a.taskPreview.RefreshOutput(taskID, raw, tw, emuCols, emuRows, w, h)
		return
	}

	// No live session — try session log file.
	// Stat the file first to skip redundant reads for completed tasks
	// whose log hasn't changed (avoids reading up to 95MB every tick).
	logSize := statSessionLog(taskID)
	a.mu.Lock()
	if taskID == a.lastPreviewTaskID && logSize > 0 && logSize == a.lastPreviewLogSize {
		a.mu.Unlock()
		return
	}
	a.lastPreviewTaskID = taskID
	a.lastPreviewTW = 0
	a.lastPreviewLogSize = logSize
	a.mu.Unlock()

	if logSize > 0 {
		logData := LoadSessionLog(taskID)
		if len(logData) > 0 {
			// Dead session: the PTY is gone, so the persisted sidecar is
			// the only record of the width the log bytes were formatted
			// for. Pane-size fallback is a best-effort for pre-sidecar
			// sessions and may scramble wide content.
			emuCols, emuRows, src := previewEmuSize(taskID, 0, 0, w, h)
			uxlog.Log("[tui] preview: dead task=%s emu=%dx%d (%s) view=%dx%d log=%d", taskID, emuCols, emuRows, src, w, h, len(logData))
			// logData is the 64KB tail; logSize is the full stream length. A
			// finished session is static, so this single full replay (emu
			// rebuilds on the task switch) reconstructs the viewport and later
			// ticks short-circuit on the unchanged logSize above.
			a.taskPreview.RefreshOutput(taskID, logData, uint64(logSize), emuCols, emuRows, w, h)
			return
		}
	}

	a.taskPreview.SetStatus("No active agent")
}

// isTaskRunning checks if a task has a running session.
func (a *App) isTaskRunning(taskID string) bool {
	for _, id := range a.runningIDs {
		if id == taskID {
			return true
		}
	}
	return false
}

// resolveSandboxed returns whether the given task would run sandboxed
// based on the current config. Called at task creation time to persist
// the sandbox state on the task, so the display reflects the setting
// that was active when the task was launched (not the current setting).
func (a *App) resolveSandboxed(task *model.Task) bool {
	if task == nil {
		return false
	}
	return agent.IsTaskSandboxed(task, a.db.Config())
}

// enterPendingAgentView switches to the agent view with a "launching" banner
// while the worktree is being created. This eliminates the lag between form
// close and agent view appearing.
func (a *App) enterPendingAgentView(task *model.Task) {
	uxlog.Log("[tui] entering pending agent view for task %s (%s)", task.ID, task.Name)

	a.mu.Lock()
	a.mode = modeAgent
	a.agentFocus = focusTerminal
	a.agentState.Reset(task.ID, task.Name)
	a.mu.Unlock()

	if a.focusTracker != nil && task.ID != "" {
		a.focusTracker.SetFocused(task.ID, true)
	}

	// Open with the configured default layout (zoomed single-pane by default).
	// Ctrl+Z toggles the side panels at runtime regardless.
	a.applyDefaultAgentZen()
	a.agentHeader.SetTaskName(task.Name)
	// Leave pane taskID empty — task isn't in the DB yet, no log to replay.
	a.agentPane.SetTaskID("")
	a.agentPane.ResetVT()
	a.agentPane.SetSession(nil)
	a.agentPane.SetPending(true)
	a.agentPane.SetFocused(true)
	a.gitPanel.Clear()
	a.filePanel.Clear()
	a.filePanel.SetFocused(false)

	// Hide the tab header in agent view — only the agent header is shown.
	a.root.ResizeItem(a.header, 0, 0)
	a.pages.SwitchToPage("agent")
	a.tapp.SetFocus(a.agentPane)
}

// onTaskSelect handles Enter on a task — enters the agent view.
func (a *App) onTaskSelect(task *model.Task, autoStart bool) {
	uxlog.Log("[tui] entering agent view for task %s (%s)", task.ID, task.Name)

	// User is viewing the agent — clear the "idle unvisited" flag so the task
	// no longer displays as "in review" in the task list.
	delete(a.idleUnvisited, task.ID)
	a.viewedWhileAgent[task.ID] = true
	a.syncIdleUnvisited()

	a.mu.Lock()
	prevTaskID := a.agentState.TaskID
	a.mode = modeAgent
	a.agentFocus = focusTerminal
	a.agentState.Reset(task.ID, task.Name)
	a.mu.Unlock()
	if a.focusTracker != nil {
		// Clear focus on the prior task when navigating task-to-task inside
		// agent view (navigateAgentTask), so a task we just left isn't
		// permanently stuck as focused in the daemon's FocusTracker.
		if prevTaskID != "" && prevTaskID != task.ID {
			a.focusTracker.SetFocused(prevTaskID, false)
		}
		a.focusTracker.SetFocused(task.ID, true)
	}
	// Open with the configured default layout (zoomed single-pane by default).
	// Ctrl+Z toggles the side panels at runtime regardless.
	a.applyDefaultAgentZen()
	a.agentHeader.SetTaskName(task.Name)
	a.agentPane.SetTaskID(task.ID)
	a.agentPane.ResetVT()
	// Re-filter the attention bar now that currentID is set — otherwise the
	// just-opened task could linger in the bar until the next tick.
	a.updateAttentionBar()
	// Refresh the clipboard hint synchronously on entry so re-opening a
	// task with a pending payload doesn't flash a hint-less header for up
	// to one tick. The tick loop continues to keep this in sync.
	a.refreshClipboardCache(task.ID)

	// Resolve worktree dir
	a.worktreeDir = task.Worktree
	a.lastGitRefresh = time.Time{}
	a.gitPanel.Clear()
	a.filePanel.Clear()

	sess := a.runner.Get(task.ID)
	if sess != nil {
		a.agentPane.SetSession(sess)
	} else {
		a.agentPane.SetSession(nil)
	}

	a.agentPane.SetFocused(true)
	a.filePanel.SetFocused(false)

	// Hide the tab header in agent view — only the agent header is shown.
	a.root.ResizeItem(a.header, 0, 0)
	a.pages.SwitchToPage("agent")
	a.tapp.SetFocus(a.agentPane)
	// Reconcile PTY size on entry so a session whose PTY is stuck at a stale
	// width (dropped SIGWINCH, started in a smaller window, etc.) gets resized
	// to the current panel dimensions on the next Draw.
	a.agentPane.ForceResyncPTY()

	// Kick off initial git status
	if a.worktreeDir != "" {
		go a.fetchGitStatus(task.ID, a.worktreeDir)
	}

	// Start continuous redraw loop for existing running sessions.
	if sess != nil && sess.Alive() {
		a.startAgentRedrawLoop(task.ID, sess)
		// Detect width drift between the session's committed scrollback width
		// and the current panel — covers both the narrow-stuck-at-startup case
		// and the "another viewer (web app, resized terminal) committed at a
		// different width" case. If the session is idle, kill it so the
		// deferred restart in handleSessionExitUI brings it back at the
		// current PTY and the agent re-renders the conversation history.
		a.maybeKickRerender(task, sess)
		return
	}
	// No live session — clear any leaked pending-restart marker so a future
	// re-entry isn't silently blocked from kicking again.
	a.reapStaleRerenderRestart(task.ID, sess)

	// Auto-start sessions when entering agent view for a non-running task.
	// Covers both fresh tasks (no SessionID) and interrupted sessions
	// (e.g., daemon restart with a preserved SessionID). Excludes completed,
	// archived tasks — those are view-only until the user explicitly presses
	// Enter to restart.
	// After the sess.Alive() early-return above, any session here is dead.
	if autoStart && task.Status != model.StatusComplete && !task.Archived {
		sid := task.SessionID
		if sid == "" {
			sid = "(none)"
		}
		uxlog.Log("[tui] auto-starting session for task %s (sessionID=%s)", task.ID, sid)
		a.startSession(task)
	}
}

// maybeKickRerender detects sessions whose committed scrollback width differs
// meaningfully from the current panel — either because the session started
// narrow (the original bug) or because a different viewer (web app, resized
// terminal) committed at a different width earlier. Triggers a kill+resume
// cycle so the resumed session re-emits the conversation history at the
// current panel size. The deferred restart fires in handleSessionExitUI via
// pendingRerenderRestart. No-op for backends that can't resume (no SessionID),
// for already-restarted tasks, or when the session is busy (don't kill mid
// tool-call).
//
// The decision RPCs (`InitialPTYSize`, `IsIdle`) hit the daemon over the
// Unix socket, so we do them on a background goroutine and dispatch the
// kick back via QueueUpdateDraw — never block the tview main goroutine on
// network I/O. The panel size and the session pointer are captured up front
// on the main goroutine where it's safe to read them.
//
// Shared predicate with the API's resize handler — see
// `agent.ShouldKickRerender` for the gating logic.
func (a *App) maybeKickRerender(task *model.Task, sess agent.SessionHandle) {
	if task == nil || sess == nil || !sess.Alive() {
		return
	}
	if a.pendingRerenderRestart[task.ID] {
		return // a kick is already in flight for this task
	}
	taskID := task.ID
	_, panelCols := a.computePTYSize() // safe: GetInnerRect on the main goroutine

	// Cache gate runs before the SessionID check so Codex tasks (which
	// have SessionID=="" and can never be kicked) still benefit from the
	// short-circuit — matches the web side's ordering and avoids spawning
	// an RPC goroutine on every Codex agent-view reopen.
	if a.isRedundantAttach(taskID, panelCols) {
		uxlog.Log("[tui] rerender: skipping kick task=%s — panel cols unchanged since last attach (%d)", taskID, panelCols)
		return
	}
	if task.SessionID == "" {
		return // backend doesn't support --session-id resume; nothing to do
	}

	go func() {
		// RPC calls — must NOT happen on the tview main goroutine.
		initCols, _ := sess.InitialPTYSize()
		idle := sess.IsIdle()
		needsInput := sessionBlockedOnPrompt(taskID, idle)
		a.tapp.QueueUpdateDraw(func() {
			// Re-check liveness and the pending flag — anything could have
			// changed during the RPC round-trip.
			if !sess.Alive() || a.pendingRerenderRestart[taskID] {
				return
			}
			decision := agent.ShouldKickRerender(true, initCols, int(panelCols), idle, false, needsInput)
			switch decision {
			case agent.RerenderSkip:
				return
			case agent.RerenderDeferBusy:
				// Agent is mid-tool-call — invalidate so the next
				// same-cols reopen re-evaluates when the agent goes idle.
				a.invalidateAttachCache(taskID)
				uxlog.Log("[tui] rerender deferred: task=%s busy (init=%d panel=%d)", taskID, initCols, panelCols)
				return
			case agent.RerenderDeferPrompt:
				// Agent is blocked on a user prompt — kicking would dismiss
				// the question. Invalidate so a later resize re-evaluates
				// once the user has answered and the agent moves on.
				a.invalidateAttachCache(taskID)
				uxlog.Log("[tui] rerender deferred: task=%s blocked on user prompt — preserving question (init=%d panel=%d)", taskID, initCols, panelCols)
				return
			case agent.RerenderKick:
				uxlog.Log("[tui] rerender: stopping task=%s session=%s (init=%dx panel=%dx)", taskID, task.SessionID, initCols, panelCols)
				a.statusbar.SetInfo("Re-rendering at full width…")
				a.pendingRerenderRestart[taskID] = true
				if err := sess.Stop(); err != nil {
					uxlog.Log("[tui] rerender: stop failed task=%s err=%v", taskID, err)
					delete(a.pendingRerenderRestart, taskID)
					// Stop attempt failed — invalidate so the next
					// same-cols reopen retries (mirrors DeferBusy).
					a.invalidateAttachCache(taskID)
					a.statusbar.ClearInfo()
				}
			}
		})
	}()
}

// isRedundantAttach returns true when the panel cols match the
// most recent attach for this task — i.e., the user reopened the agent view
// without resizing. The rerender kick would otherwise destroy any in-flight
// Claude UI (e.g. AskUserQuestion overlays) because the --session-id restart
// rehydrates the conversation but not ephemeral modals. When proceeding,
// caches the current cols so a subsequent reopen at the same size short
// -circuits. Genuine resizes fall through because panelCols differs from
// the cached value, so the kick predicate still runs.
func (a *App) isRedundantAttach(taskID string, panelCols uint16) bool {
	if prev, ok := a.lastAttachCols[taskID]; ok && prev == panelCols {
		return true
	}
	a.lastAttachCols[taskID] = panelCols
	return false
}

// invalidateAttachCache clears the cached cols for taskID so the next
// maybeKickRerender call at any panel size re-evaluates the predicate.
// Called from every non-Skip "could have kicked but didn't" outcome (busy
// session, kick attempt error) so subsequent reopens at the same cols retry
// instead of permanently short-circuiting. Main-goroutine-only (lastAttachCols
// has no mutex because every access path runs on the tview main goroutine).
func (a *App) invalidateAttachCache(taskID string) {
	delete(a.lastAttachCols, taskID)
}

// reapStaleRerenderRestart clears a leaked pendingRerenderRestart entry when the
// session it referred to has died without firing handleSessionExitUI (daemon
// crash mid-stop, lost stream notification, etc.). Called from onTaskSelect
// before maybeKickRerender so a stuck flag can't permanently block
// recovery.
func (a *App) reapStaleRerenderRestart(taskID string, sess agent.SessionHandle) {
	if !a.pendingRerenderRestart[taskID] {
		return
	}
	if sess != nil && sess.Alive() {
		return // exit notification still pending; let it run
	}
	uxlog.Log("[tui] rerender: reaping stale pending flag for task=%s", taskID)
	delete(a.pendingRerenderRestart, taskID)
	a.statusbar.ClearInfo()
}

// buildNewTaskForm constructs a NewTaskForm defaulted to defaultProject and
// wires its async branch loader. Shared by the Tasks-tab new-task flow and the
// Hera rail's `w`/`n` keys (which reuse the SAME modal).
func (a *App) buildNewTaskForm(defaultProject string) *NewTaskForm {
	cfg := a.db.Config()
	f := NewNewTaskForm(cfg.Projects, defaultProject, cfg.Backends, cfg.Defaults.Backend)
	f.OnBranchFocus = func(path string) {
		go func() {
			branches := gitutil.ListRemoteBranches(path)
			uxlog.Log("[newtask] loaded %d branches for %s", len(branches), path)
			a.tapp.QueueUpdateDraw(func() {
				if a.newTaskForm != nil {
					a.newTaskForm.SetBranchOptions(branches)
				}
			})
		}()
	}
	return f
}

// onNewTask opens the new task form (Tasks tab, default create-and-start path).
func (a *App) onNewTask() {
	a.newTaskForm = a.buildNewTaskForm(a.tasklist.SelectedProject())
	a.newTaskReturnPage = "tasks"
	// Trigger initial branch load for the default project.
	a.newTaskForm.maybeLoadBranches()

	a.mode = modeNewTask
	a.pages.AddPage("newtask", a.newTaskForm, true, true)
	a.pages.SwitchToPage("newtask")
	a.tapp.SetFocus(a.newTaskForm)
}

// openHeraNewTaskForm opens the shared new-task modal for the Hera tab. title
// labels the modal (e.g. "Spawn worker"); defaultProject pre-fills the project;
// onDone runs with the assembled task + resolved project on submit (the form is
// already closed and focus has returned to the Hera tab).
func (a *App) openHeraNewTaskForm(title, defaultProject string, onDone func(task *model.Task, project string)) {
	a.newTaskForm = a.buildNewTaskForm(defaultProject)
	a.newTaskForm.SetTitle(title)
	a.newTaskOnDone = onDone
	a.newTaskReturnPage = "hera"
	a.newTaskForm.maybeLoadBranches()

	a.mode = modeNewTask
	a.pages.AddPage("newtask", a.newTaskForm, true, true)
	a.pages.SwitchToPage("newtask")
	a.tapp.SetFocus(a.newTaskForm)
}

// handleNewTaskKey processes keys in the new task form mode.
func (a *App) handleNewTaskKey(event *tcell.EventKey) {
	handler := a.newTaskForm.InputHandler()
	handler(event, func(p tview.Primitive) {})

	if a.newTaskForm.Canceled() {
		a.closeNewTaskForm()
		return
	}

	if a.newTaskForm.Done() {
		task := a.newTaskForm.Task()
		if task.Name == "" {
			a.newTaskForm.SetError("Prompt cannot be empty")
			return
		}

		// Capture form data before closing.
		proj := a.newTaskForm.SelectedProject()
		onDone := a.newTaskOnDone
		var projCfg config.Project
		if p, ok := a.db.Config().Projects[proj]; ok {
			projCfg = p
		}

		// Close form immediately so the UI feels responsive. closeNewTaskForm
		// clears newTaskOnDone (captured above) and returns to the correct tab.
		a.closeNewTaskForm()

		// Hera-tab override (rail `w`/`n`): spawn worker / new coordinator from the
		// shared modal instead of the default Tasks-tab create-and-start path.
		if onDone != nil {
			onDone(task, proj)
			return
		}

		if projCfg.Path == "" {
			// No project path — no worktree needed, persist and start inline.
			task.Sandboxed = a.resolveSandboxed(task)
			a.db.Add(task)
			uxlog.Log("[tui] created task %s (%s)", task.ID, task.Name)
			a.refreshTasksLocal()
			a.tasklist.SelectByID(task.ID)
			a.onTaskSelect(task, true)
			return
		}

		// Switch to agent view immediately with a pending banner so there's
		// no lag after the form closes. The CreateAndStart goroutine below
		// does worktree creation + DB insert + session start transactionally.
		a.enterPendingAgentView(task)
		a.statusbar.SetInfo("Creating worktree…")
		uxlog.Log("[tui] starting create-and-start for task %q in project %q", task.Name, proj)
		rows, cols := a.computePTYSize()
		input := agent.CreateInput{
			Name:       task.Name,
			Prompt:     task.Prompt,
			Project:    proj,
			Backend:    task.Backend,
			Model:      task.Model,
			BaseBranch: task.Branch,
			// INVARIANT: the new-task form has no name field — task.Name is
			// always GenerateNameFromPrompt(prompt). If a name field is added
			// later, gate this on whether the user typed one.
			AutoName:    true,
			Rows:        rows,
			Cols:        cols,
			BeforeStart: func() { a.startGen.Add(1) },
			AfterStart:  func() { a.startGen.Add(1) },
		}

		go func() {
			created, err := a.createTaskTransactional(input)
			if err != nil {
				a.tapp.QueueUpdateDraw(func() {
					a.statusbar.ClearInfo()
					a.statusbar.SetError("Create failed: " + err.Error())
					// pending agent view has empty agentState.TaskID — only
					// exit if the user is still there (hasn't navigated away).
					if a.mode == modeAgent && a.agentState.TaskID == "" {
						a.exitAgentView()
					}
					a.showError("Create failed", err.Error())
				})
				uxlog.Log("[tui] create-and-start failed: %v", err)
				return
			}

			a.tapp.QueueUpdateDraw(func() {
				a.statusbar.ClearInfo()
				a.recentStarts[created.ID] = time.Now()
				uxlog.Log("[tui] created task %s (%s)", created.ID, created.Name)
				a.refreshTasksLocal()

				stillPending := a.mode == modeAgent && a.agentState.TaskID == ""
				if !stillPending {
					// User moved away — just select the new task in the list.
					a.tasklist.SelectByID(created.ID)
					return
				}

				// Complete the transition: session is already live, so
				// onTaskSelect sees it via runner.Get and wires up the pane
				// without re-invoking runner.Start.
				a.agentHeader.SetTaskName(created.Name)
				a.tasklist.SelectByID(created.ID)
				a.onTaskSelect(created, true)
			})
		}()
	}
}

// remoteTaskCreator is satisfied by *apistore.Store. In --remote mode the TUI
// can't run agent.CreateAndStart (the worktree + PTY live on the daemon's
// host), so fresh-task creation routes through POST /api/tasks instead. Kept
// as a structural interface so the tui package doesn't import apistore.
type remoteTaskCreator interface {
	CreateTask(ctx context.Context, name, prompt, project, backend, taskModel string) (*model.Task, error)
}

// createTaskTransactional creates a fresh task and returns the resulting row.
// Local mode (a.db is *db.DB) runs the fully-transactional agent.CreateAndStart
// in-process. Remote mode (a.db is *apistore.Store) POSTs to /api/tasks so the
// daemon does the worktree + session creation server-side; base-branch override
// and attachments aren't carried over the REST path. Runs on a background
// goroutine — callers dispatch UI updates via QueueUpdateDraw.
func (a *App) createTaskTransactional(input agent.CreateInput) (*model.Task, error) {
	if d, ok := a.db.(*db.DB); ok {
		created, _, err := agent.CreateAndStart(d, a.runner, input)
		return created, err
	}
	rc, ok := a.db.(remoteTaskCreator)
	if !ok {
		return nil, fmt.Errorf("task creation requires local mode (use POST /api/tasks remotely)")
	}
	if input.BaseBranch != "" {
		uxlog.Log("[tui] remote create: base-branch %q ignored (POST /api/tasks has no base-branch field)", input.BaseBranch)
	}
	// Mirror CreateAndStart's BeforeStart/AfterStart hooks: bump startGen so a
	// concurrent tick doesn't reconcile the new task as "not running" in the
	// window before the SSE stream attaches server-side.
	a.startGen.Add(1)
	defer a.startGen.Add(1)
	return rc.CreateTask(context.Background(), input.Name, input.Prompt, input.Project, input.Backend, input.Model)
}

// computePTYSize returns the best available PTY dimensions for the agent
// terminal pane. Prefers the host terminal size assuming the zoomed
// (single-pane) full-width layout — regardless of the ui.default_agent_zoom
// setting (see ptySizeFromHostTerm) — which is always accurate when stdout is a
// TTY; falls back to the pane's actual inner rect; finally defaults to 24x80.
//
// Host terminal is preferred over the pane rect because tview's Box returns
// its default 15x10 rect before Flex has laid it out — and computePTYSize
// runs synchronously after SwitchToPage("agent") on agent-view entry, before
// the queued layout/Draw can settle. Reading that 15x10 default yielded a
// 20x8 PTY, and Claude rendered the full conversation at narrow width with
// cursor positions that no SIGWINCH-triggered redraw can re-flow.
//
// MUST be called on the tview main goroutine — GetInnerRect is not safe to
// call concurrently with Draw.
func (a *App) computePTYSize() (rows, cols uint16) {
	rows, cols = ptySizeFromHostTerm(term.GetSize(int(os.Stdout.Fd())))
	if rows > 0 && cols > 0 {
		return
	}
	_, _, pw, ph := a.agentPane.GetInnerRect()
	if r, c := ptySizeFromPaneRect(pw, ph); r > 0 && c > 0 {
		return r, c
	}
	return 24, 80
}

// agentViewRowOverhead is the total fixed-row height consumed by chrome
// outside the agent pane's inner content area, when the user is in agent view
// (tab header hidden via ResizeItem(0,0)):
//
//	agentHeader (1) + statusbar (1) + pane top+bottom border (2) = 4
//
// Used by ptySizeFromHostTerm to derive the pane's inner height from the host
// terminal size. If the agent view layout ever grows or shrinks a fixed row
// (e.g., a second status bar), this constant must change with it — otherwise
// computePTYSize will silently drift from the actual pane inner rect and
// every agent-view entry will fire a forceResync correction whose SIGWINCH
// can cause Claude to repaint visibly.
const agentViewRowOverhead = 4

// agentViewColOverhead is the total fixed-column width consumed by the agent
// pane's left+right custom border (1 cell each via widget.DrawBorderedPanel,
// since TerminalPane is a bare tview.Box without a native border):
//
//	pane left+right border (1 + 1) = 2
//
// Used by ptySizeFromHostTerm and ptySizeFromPaneRect to derive the pane's
// inner width. The same `2` appears in SetSession's inner-rect seed in
// internal/tui/terminal/terminalpane.go — if DrawBorderedPanel's border
// width ever changes, all three sites must update together. Keeping the
// constant here ties the architectural invariant to a single name.
const agentViewColOverhead = 2

// ptySizeFromHostTerm derives the agent PTY size from the host terminal,
// applying the zoomed (single-pane) full-width column layout and the
// header/footer/border row deductions. Returns 0,0 when the input is unusable.
func ptySizeFromHostTerm(tw, th int, err error) (rows, cols uint16) {
	if err != nil || tw <= 0 || th <= 0 {
		return 0, 0
	}
	// Seeds at the zoomed full-width layout: the terminal pane spans the full
	// host width minus its own custom border on both sides (agentViewColOverhead).
	// When ui.default_agent_zoom is true (the default) this matches the laid-out
	// pane exactly, so no SIGWINCH repaint fires on entry. When the configured
	// default is split (1:3:1), the pane lays out narrower and the terminal
	// pane's own Draw() recompute corrects the size on the next frame — one
	// SIGWINCH repaint on entry, accepted (see gotchas/keybindings.md).
	centerW := max(tw-agentViewColOverhead, 20)
	// Every entry path that calls computePTYSize hides the tab header BEFORE
	// this function runs — enterPendingAgentView (new task) and onTaskSelect
	// (auto-start) both run ResizeItem first. Fork is the one exception (its
	// computePTYSize fires before onTaskSelect), so the agent's PTY is 1 row
	// taller than the still-header-visible pane during the brief CreateAndStart
	// window. That's fine: the pane isn't on screen yet, and by the time the
	// user reaches the agent view the header is hidden and sizes match — so
	// no SIGWINCH-triggered repaint is needed when the agent becomes visible.
	centerH := max(th-agentViewRowOverhead, 5)
	return uint16(centerH), uint16(centerW)
}

// ptySizeFromPaneRect derives the agent PTY size from the agent pane's full
// box rect (as returned by GetInnerRect — the agent pane has no native tview
// border, so its inner rect equals its outer rect). The pane draws its own
// 1-cell border via widget.DrawBorderedPanel, so the visible content area is
// pw-agentViewColOverhead by ph-agentViewColOverhead. See also
// `agentViewRowOverhead` for the related row-deduction constant used by
// ptySizeFromHostTerm.
//
// Rejects the tview Box default of 15x10 — that rect surfaces before Flex
// has laid the pane out and would produce a 20x8 PTY (Claude renders narrow
// forever).
func ptySizeFromPaneRect(pw, ph int) (rows, cols uint16) {
	if pw <= 0 || ph <= 0 {
		return 0, 0
	}
	// tview's NewBox defaults to 15x10. Any laid-out agent pane is wider
	// AND taller than those defaults on a usable terminal, so we treat
	// anything ≤ either as the uninitialized default. 30x10 stays generous
	// enough that even a tiny 50-col host fed via the fallback would not
	// falsely match.
	if pw <= 30 || ph <= 10 {
		return 0, 0
	}
	// ph and pw are realistic terminal cell counts (low thousands at most),
	// so the int → uint16 conversion cannot overflow; the max() floors also
	// guarantee positive values. Silence gosec G115 for both fields.
	return uint16(max(ph-agentViewColOverhead, 5)), uint16(max(pw-agentViewColOverhead, 20)) //nolint:gosec // see comment
}

// Rect is the agent-pane outer rectangle on screen, taken at face value.
// Multi-pane layouts (PR 7 and later) pass per-pane rects here rather than
// going through computePTYSize's host-term/box-default reasoning.
type Rect struct {
	X, Y, W, H int
}

// PTYSizeForRect returns the PTY (rows, cols) for an agent terminal whose
// outer rect on screen is r. The 1-cell border on each side
// (agentViewColOverhead) is subtracted; rows and cols are clamped to the
// minimum useful floor (5 rows / 20 cols).
//
// Unlike [App.computePTYSize], the input rect is trusted: there is no
// host-term fallback and no Box-default rejection. Callers driving the new
// layout registry already know the authoritative pane rect.
func PTYSizeForRect(r Rect) (rows, cols uint16) {
	if r.W <= 0 || r.H <= 0 {
		return 0, 0
	}
	// Realistic cell counts cap well under uint16; max() guarantees positive.
	return uint16(max(r.H-agentViewColOverhead, 5)), uint16(max(r.W-agentViewColOverhead, 20)) //nolint:gosec // bounded by terminal cell count
}

// startSession starts a session for an *existing* task (Enter-to-restart or
// auto-start on entering agent view). On failure, status reverts to Pending
// but the DB row and worktree are preserved — the user may fix the underlying
// issue (e.g. missing backend binary) and retry.
//
// For *fresh* task creation, callers use agent.CreateAndStart instead, which
// unwinds the worktree and DB row on failure so no orphans remain.
func (a *App) startSession(task *model.Task) {
	cfg := a.db.Config()
	rows, cols := a.computePTYSize()

	resume := task.SessionID != ""

	// For Claude-style backends, generate a session ID on first run so we can
	// resume the conversation later. Codex and pi capture their IDs post-exit
	// (in handleSessionExitUI → CaptureCodexSessionID / CapturePiSessionID).
	// generatedSessionID tracks whether THIS call minted the ID, so the error
	// branch below only clears IDs it created — preserving any pre-existing
	// ID across restart failures (daemon restart cascade, transient RPC error)
	// so the next retry can still --resume the conversation.
	generatedSessionID := false
	if !resume {
		backend, berr := agent.ResolveBackend(task, cfg)
		if berr == nil && !agent.IsCodexBackend(backend.Command) && !agent.IsPiBackend(backend.Command) {
			task.SessionID = model.GenerateSessionID()
			generatedSessionID = true
			a.db.Update(task) //nolint:errcheck
			uxlog.Log("[tui] generated session ID %s for task %s", task.SessionID, task.ID)
		}
	}

	// Bump generation BEFORE the RPC so any tick that captured runningIDs
	// before this session exists will detect the change and skip reconciliation.
	a.startGen.Add(1)

	// INVARIANT: runner.Start() MUST be a blocking (synchronous) call on the
	// tview goroutine. The post-bump correctness depends on QueueUpdateDraw
	// callbacks being unable to run until Start returns. If Start is ever made
	// async, the post-bump race window reopens.
	sess, err := a.runner.Start(task, cfg, rows, cols, resume)
	// Bump again AFTER the RPC — covers ticks that captured startGen during
	// the Start RPC (after the pre-bump but before the session was registered
	// in the daemon). The callback runs on the tview goroutine, so it can't
	// fire until this point, making the post-bump always visible.
	a.startGen.Add(1)
	if err != nil {
		uxlog.Log("[tui] failed to start session: %v", err)
		a.statusbar.SetError("Start failed: " + err.Error())
		// Revert to pending so the task isn't left in a ghost state.
		task.SetStatus(model.StatusPending)
		// Only clear the SessionID if WE just generated it — a pre-existing
		// ID (from a prior successful run) must survive transient Start
		// failures so the user's next retry can --resume the conversation.
		// Wiping it here was the cause of the "fresh session with original
		// prompt re-injected after daemon+TUI reboot" bug.
		if generatedSessionID {
			task.SessionID = ""
		}
		task.StartedAt = time.Time{}
		a.db.Update(task) //nolint:errcheck
		return
	}

	task.SetStatus(model.StatusInProgress)
	task.AgentPID = sess.PID()
	a.recentStarts[task.ID] = time.Now() // grace period: protect from false reconciliation
	a.db.Update(task)                    //nolint:errcheck

	// Attach to the terminal pane and start the redraw loop only if the
	// agent view is active for this task. When startSession is called from
	// the background (user navigated away during worktree creation), the
	// pane isn't visible — onTaskSelect will attach when the user returns.
	if a.mode == modeAgent && a.agentState.TaskID == task.ID {
		a.agentPane.SetSession(sess)
		// Force a PTY resize repost on the next Draw. Covers the auto-start
		// path (pending view → session starts while user is watching) where
		// onTaskSelect isn't called and the PTY could otherwise be stuck at
		// its launch size.
		a.agentPane.ForceResyncPTY()
		a.startAgentRedrawLoop(task.ID, sess)
	}
}

// startAgentRedrawLoop runs a goroutine that triggers redraws every 200ms
// while the session is alive and the agent view is active. The 1-second tick
// is too slow for a live terminal. Self-terminates when the session exits or
// the user leaves the agent view.
func (a *App) startAgentRedrawLoop(taskID string, sess agent.SessionHandle) {
	uxlog.Log("[tui] startAgentRedrawLoop: taskID=%s", taskID)
	go func() {
		var lastTotalWritten uint64
		for {
			time.Sleep(200 * time.Millisecond)
			if !sess.Alive() {
				// One final redraw to show the finished state.
				a.tapp.QueueUpdateDraw(func() {})
				return
			}
			a.mu.Lock()
			stillViewing := a.mode == modeAgent && a.agentState.TaskID == taskID
			a.mu.Unlock()
			if !stillViewing {
				return
			}
			// Sync PTY size on every redraw cycle — the 1-second tick is too
			// slow and causes the agent to render at the wrong width (e.g., 80
			// cols) until the first tick fires. This is an RPC call but runs on
			// the background goroutine, not the tview main goroutine.
			a.agentPane.SyncPTYSize()
			// Only trigger a redraw when new output has arrived. Keystroke
			// and window-resize events already trigger their own redraws via
			// tview, so skipping here when idle avoids unnecessary draw cycles.
			tw := sess.TotalWritten()
			if tw != lastTotalWritten {
				lastTotalWritten = tw
				a.tapp.QueueUpdateDraw(func() {})
			}
		}
	}()
}

func (a *App) closeNewTaskForm() {
	a.mode = modeTaskList
	a.newTaskForm = nil
	a.newTaskOnDone = nil
	page := a.newTaskReturnPage
	a.newTaskReturnPage = ""
	a.pages.RemovePage("newtask")
	if page == "hera" {
		a.pages.SwitchToPage("hera")
		a.tapp.SetFocus(a.heraPage)
		return
	}
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
}

// openLinkPickerModal shows the link picker dialog.
func (a *App) openLinkPickerModal(links []Link) {
	// Remember the current page so we restore correctly on close.
	name, _ := a.pages.GetFrontPage()
	a.linkPickerPrevPage = name

	a.linkPickerModal = NewLinkPickerModal(links)
	a.mode = modeLinkPicker
	a.pages.AddPage("linkpicker", a.linkPickerModal, true, true)
	a.pages.SwitchToPage("linkpicker")
	a.tapp.SetFocus(a.linkPickerModal)
}

// handleLinkPickerKey processes keys in the link picker modal.
func (a *App) handleLinkPickerKey(event *tcell.EventKey) {
	handler := a.linkPickerModal.InputHandler()
	handler(event, func(p tview.Primitive) {})

	if a.linkPickerModal.Canceled() {
		a.closeLinkPickerModal()
		return
	}
	if a.linkPickerModal.Selected() {
		link := a.linkPickerModal.SelectedLink()
		a.closeLinkPickerModal()
		openURL(link.URL)
	}
}

// closeLinkPickerModal closes the link picker modal.
func (a *App) closeLinkPickerModal() {
	a.mode = modeTaskList
	a.linkPickerModal = nil
	a.pages.RemovePage("linkpicker")
	if a.linkPickerPrevPage != "" {
		a.pages.SwitchToPage(a.linkPickerPrevPage)
	}
	a.tapp.SetFocus(a.tasklist)
}

// openAgentLinks extracts links from the current agent session and opens the fuzzy link picker.
// File I/O runs in a background goroutine to avoid blocking the tview main goroutine.
func (a *App) openAgentLinks() {
	a.mu.Lock()
	taskID := a.agentState.TaskID
	a.mu.Unlock()
	if taskID == "" {
		return
	}

	go func() {
		// Read from session log file (complete output, not just ring buffer).
		logPath := agent.SessionLogPath(taskID)
		data, err := os.ReadFile(logPath)
		if err != nil || len(data) == 0 {
			return
		}

		links := ExtractLinks(string(data))
		if len(links) == 0 {
			return
		}

		uxlog.Log("[agent] opening fuzzy link picker: %d links found", len(links))
		a.tapp.QueueUpdateDraw(func() {
			// Guard: user may have left agent view while I/O was in-flight.
			if a.mode != modeAgent {
				return
			}
			a.openFuzzyLinkPickerModal(links)
		})
	}()
}

// openFuzzyLinkPickerModal shows the fuzzy link picker dialog.
// Only callable from modeAgent — close always restores modeAgent.
func (a *App) openFuzzyLinkPickerModal(links []Link) {
	a.fuzzyLinkPickerModal = NewFuzzyLinkPickerModal(links)
	a.mode = modeFuzzyLinkPicker
	a.pages.AddPage("fuzzylinkpicker", a.fuzzyLinkPickerModal, true, true)
	a.tapp.SetFocus(a.fuzzyLinkPickerModal)
}

// handleFuzzyLinkPickerKey processes keys in the fuzzy link picker modal.
func (a *App) handleFuzzyLinkPickerKey(event *tcell.EventKey) {
	handler := a.fuzzyLinkPickerModal.InputHandler()
	handler(event, func(p tview.Primitive) {})

	if a.fuzzyLinkPickerModal.Canceled() {
		a.closeFuzzyLinkPickerModal()
		return
	}
	if a.fuzzyLinkPickerModal.Selected() {
		link := a.fuzzyLinkPickerModal.SelectedLink()
		a.closeFuzzyLinkPickerModal()
		openURL(link.URL)
	}
}

// closeFuzzyLinkPickerModal closes the fuzzy link picker and restores agent view.
func (a *App) closeFuzzyLinkPickerModal() {
	a.mode = modeAgent
	a.fuzzyLinkPickerModal = nil
	a.pages.RemovePage("fuzzylinkpicker")
	// Restore focus to the agent pane.
	a.tapp.SetFocus(a.agentPane)
}

// openSessionPicker lists the current task's Claude sessions and opens the
// session switcher modal. The session discovery (filesystem scan + JSONL
// parse) runs in a background goroutine so the tview main goroutine never
// blocks on disk I/O — a long-running conversation's JSONL can be many MB.
//
// The switcher is Claude-only: codex and pi store sessions in their own
// formats and resume through different flags, so for those backends this is a
// no-op with a brief status notice.
func (a *App) openSessionPicker() {
	a.mu.Lock()
	taskID := a.agentState.TaskID
	a.mu.Unlock()
	if taskID == "" {
		return
	}
	task, err := a.db.Get(taskID)
	if err != nil || task == nil {
		uxlog.Log("[tui] session picker: task %s lookup failed: %v", taskID, err)
		return
	}

	cfg := a.db.Config()
	backend, berr := agent.ResolveBackend(task, cfg)
	if berr != nil {
		uxlog.Log("[tui] session picker: resolve backend failed for task %s: %v", taskID, berr)
		return
	}
	if agent.IsCodexBackend(backend.Command) || agent.IsPiBackend(backend.Command) {
		uxlog.Log("[tui] session picker: backend %q is not Claude — switcher unavailable", backend.Command)
		a.statusbar.SetInfo("Session switcher is Claude-only")
		return
	}

	worktree := task.Worktree
	currentID := task.SessionID
	go func() {
		sessions, err := claudesession.List(worktree)
		if err != nil {
			uxlog.Log("[tui] session picker: list failed for %s: %v", worktree, err)
			return
		}
		uxlog.Log("[tui] session picker: %d sessions found for task %s", len(sessions), taskID)
		a.tapp.QueueUpdateDraw(func() {
			// Guard: the user may have left agent view while I/O was in flight.
			if a.mode != modeAgent || a.agentState.TaskID != taskID {
				return
			}
			a.openSessionPickerModal(sessions, currentID)
		})
	}()
}

// openSessionPickerModal shows the session switcher dialog.
// Only callable from modeAgent — close always restores modeAgent.
func (a *App) openSessionPickerModal(sessions []claudesession.Session, currentID string) {
	a.sessionPickerModal = NewSessionPickerModal(sessions, currentID)
	a.mode = modeSessionPicker
	a.pages.AddPage("sessionpicker", a.sessionPickerModal, true, true)
	a.tapp.SetFocus(a.sessionPickerModal)
}

// handleSessionPickerKey processes keys in the session switcher modal.
func (a *App) handleSessionPickerKey(event *tcell.EventKey) {
	handler := a.sessionPickerModal.InputHandler()
	handler(event, func(p tview.Primitive) {})

	if a.sessionPickerModal.Canceled() {
		a.closeSessionPickerModal()
		return
	}
	if a.sessionPickerModal.Selected() {
		chosen := a.sessionPickerModal.SelectedSession()
		a.closeSessionPickerModal()
		a.switchSession(chosen.ID, chosen.Title)
	}
}

// closeSessionPickerModal closes the switcher and restores agent view.
func (a *App) closeSessionPickerModal() {
	a.mode = modeAgent
	a.sessionPickerModal = nil
	a.pages.RemovePage("sessionpicker")
	a.tapp.SetFocus(a.agentPane)
}

// openTaskSwitcher builds the task switcher entries from the cached task list
// and the current needs-input set, then opens the switcher modal. Both
// a.tasks and a.needsInputIDs are maintained on the tview goroutine by the
// tick loop, so the switcher is built synchronously with no disk I/O. Entries
// are sorted needs-input-first (then alphabetically), and the currently-viewed
// task plus archived tasks are excluded.
func (a *App) openTaskSwitcher() {
	a.mu.Lock()
	currentID := a.agentState.TaskID
	a.mu.Unlock()
	needs := make(map[string]bool, len(a.needsInputIDs))
	for _, id := range a.needsInputIDs {
		needs[id] = true
	}
	entries := make([]taskSwitcherEntry, 0, len(a.tasks))
	for _, t := range a.tasks {
		if t.Archived || t.ID == currentID {
			continue
		}
		entries = append(entries, taskSwitcherEntry{
			ID:         t.ID,
			Name:       t.Name,
			Project:    t.Project,
			Status:     t.Status,
			NeedsInput: needs[t.ID],
		})
	}
	// Needs-input first, then alphabetical by name (case-insensitive),
	// breaking final ties on ID for stable ordering.
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].NeedsInput != entries[j].NeedsInput {
			return entries[i].NeedsInput
		}
		li, lj := strings.ToLower(entries[i].Name), strings.ToLower(entries[j].Name)
		if li != lj {
			return li < lj
		}
		return entries[i].ID < entries[j].ID
	})
	uxlog.Log("[tui] task switcher: %d candidate tasks (current=%s)", len(entries), currentID)
	a.openTaskSwitcherModal(entries)
}

// openTaskSwitcherModal shows the task switcher dialog.
// Only callable from modeAgent — close always restores modeAgent.
func (a *App) openTaskSwitcherModal(entries []taskSwitcherEntry) {
	a.taskSwitcherModal = NewTaskSwitcherModal(entries)
	// The Ctrl+K switcher mirrors the task list: tasks nested under project
	// folders, with task-list-style (whitespace-split substring) search.
	a.taskSwitcherModal.SetGrouped(true)
	a.mode = modeTaskSwitcher
	a.pages.AddPage("taskswitcher", a.taskSwitcherModal, true, true)
	a.tapp.SetFocus(a.taskSwitcherModal)
}

// handleTaskSwitcherKey processes keys in the task switcher modal.
func (a *App) handleTaskSwitcherKey(event *tcell.EventKey) {
	handler := a.taskSwitcherModal.InputHandler()
	handler(event, func(p tview.Primitive) {})

	if a.taskSwitcherModal.Canceled() {
		a.closeTaskSwitcherModal()
		return
	}
	if a.taskSwitcherModal.Selected() {
		chosen := a.taskSwitcherModal.SelectedTask()
		a.closeTaskSwitcherModal()
		if chosen == "" {
			return
		}
		task, err := a.db.Get(chosen)
		if err != nil || task == nil {
			uxlog.Log("[tui] task switcher: selected task %s lookup failed: %v", chosen, err)
			return
		}
		uxlog.Log("[tui] task switcher: switching to task %s (%s)", task.ID, task.Name)
		// Keep the task-list cursor in sync so exiting the agent view (Ctrl+Q)
		// lands on the task we switched to, not the one we left — mirrors
		// navigateAgentTask. onTaskSelect (autoStart=false) does not restart a
		// dead session, matching in-agent-view navigation rather than the
		// task-list Enter path.
		a.tasklist.SelectByID(chosen)
		a.onTaskSelect(task, false)
	}
}

// closeTaskSwitcherModal closes the switcher and restores agent view.
func (a *App) closeTaskSwitcherModal() {
	a.mode = modeAgent
	a.taskSwitcherModal = nil
	a.pages.RemovePage("taskswitcher")
	a.tapp.SetFocus(a.agentPane)
}

// switchSession rebinds the current agent task to a different Claude session
// and restarts the agent so it resumes that conversation (BuildCmd appends
// --resume <SessionID>). Selecting the session the task is already bound to is
// a no-op.
//
// When the session is live, the restart reuses the rerender-restart machinery:
// set pendingRerenderRestart, stop the session, and let handleSessionExitUI
// restart it in place once the exit notification arrives. This avoids racing
// the async exit callback — the restart happens inside the exit handler rather
// than alongside it. When the session is already dead, startSession runs
// directly.
func (a *App) switchSession(newID, title string) {
	a.mu.Lock()
	taskID := a.agentState.TaskID
	a.mu.Unlock()
	if taskID == "" || newID == "" {
		return
	}
	task, err := a.db.Get(taskID)
	if err != nil || task == nil {
		uxlog.Log("[tui] session switch: task %s lookup failed: %v", taskID, err)
		return
	}
	if task.SessionID == newID {
		uxlog.Log("[tui] session switch: task %s already on session %s — no-op", taskID, newID)
		return
	}

	task.SessionID = newID
	a.db.Update(task) //nolint:errcheck
	uxlog.Log("[tui] session switch: task %s → session %s (%q)", taskID, newID, title)

	sess := a.agentPane.Session()
	if sess != nil && sess.Alive() {
		// Live session: queue the in-place restart, then stop. The exit
		// handler reads the freshly persisted SessionID and resumes it.
		a.pendingRerenderRestart[taskID] = true
		a.statusbar.SetInfo("Switching session…")
		if err := a.runner.Stop(taskID); err != nil {
			delete(a.pendingRerenderRestart, taskID)
			a.statusbar.SetError("Session switch failed: " + err.Error())
			uxlog.Log("[tui] session switch: stop failed for task %s: %v", taskID, err)
		}
		return
	}
	// Dead session: restart directly with the new SessionID.
	task.SetStatus(model.StatusInProgress)
	a.db.Update(task) //nolint:errcheck
	a.startSession(task)
	a.refreshTasksAsync()
}

// openConfirmDelete shows the confirm delete modal for the given task.
func (a *App) openConfirmDelete(t *model.Task) {
	a.confirmDeleteModal = modal.NewConfirmDeleteModal(t)
	a.mode = modeConfirmDelete
	a.pages.AddPage("confirmdelete", a.confirmDeleteModal, true, true)
	a.pages.SwitchToPage("confirmdelete")
	a.tapp.SetFocus(a.confirmDeleteModal)
}

// handleConfirmDeleteKey processes keys in the confirm delete modal.
func (a *App) handleConfirmDeleteKey(event *tcell.EventKey) {
	handler := a.confirmDeleteModal.InputHandler()
	handler(event, func(p tview.Primitive) {})

	if a.confirmDeleteModal.Canceled() {
		a.closeConfirmDelete()
		return
	}

	if a.confirmDeleteModal.Confirmed() {
		t := a.confirmDeleteModal.Task()
		a.deleteTask(t)
		a.closeConfirmDelete()
	}
}

// closeConfirmDelete dismisses the confirm delete modal.
func (a *App) closeConfirmDelete() {
	a.mode = modeTaskList
	a.confirmDeleteModal = nil
	a.pages.RemovePage("confirmdelete")
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
}

// openHelp shows the keybindings help overlay. The previous active page is
// remembered so closeHelp can restore the user's context (Tasks, DAG, or
// Settings tab).
func (a *App) openHelp() {
	if a.helpModal != nil {
		return
	}
	a.helpPrevPage, _ = a.pages.GetFrontPage()
	a.helpModal = modal.NewHelpModal()
	a.mode = modeHelp
	a.pages.AddPage("help", a.helpModal, true, true)
	a.pages.SwitchToPage("help")
	a.tapp.SetFocus(a.helpModal)
}

// handleHelpKey processes keys in the help overlay.
func (a *App) handleHelpKey(event *tcell.EventKey) {
	handler := a.helpModal.InputHandler()
	handler(event, func(p tview.Primitive) {})
	if a.helpModal.Closed() {
		a.closeHelp()
	}
}

// closeHelp dismisses the help overlay and restores the prior page.
func (a *App) closeHelp() {
	a.mode = modeTaskList
	a.helpModal = nil
	a.pages.RemovePage("help")
	prev := a.helpPrevPage
	a.helpPrevPage = ""
	if prev == "" || prev == "help" {
		prev = "tasks"
	}
	a.pages.SwitchToPage(prev)
	// Restore focus to whichever widget owns the visible tab.
	switch a.header.ActiveTab() {
	case widget.TabSettings:
		a.tapp.SetFocus(a.settings)
	case widget.TabHera:
		// The second tab is always the native Hera view; focusHeraTab2Page
		// restores it without a content refresh.
		a.focusHeraTab2Page()
	default:
		a.tapp.SetFocus(a.tasklist)
	}
}

// --- Error modal ---

// showError surfaces a failed action in a prominent dismiss-only modal. This is
// the correct surface for hard failures of explicit user actions (e.g. agent
// creation): the status bar truncates and is easily missed, so a silent failure
// there leaves the user staring at a closed form with no visible reason. Must
// run on the tview main goroutine.
func (a *App) showError(title, body string) {
	// Replace any existing error modal's contents rather than stacking pages.
	if a.errorModal != nil {
		a.pages.RemovePage("error")
	}
	a.errorModal = modal.NewErrorModal(title, body)
	a.mode = modeErrorModal
	a.pages.AddPage("error", a.errorModal, true, true)
	a.pages.SwitchToPage("error")
	a.tapp.SetFocus(a.errorModal)
	uxlog.Log("[tui] error modal shown: %s — %s", title, body)
}

// handleErrorModalKey dismisses the error modal on any key.
func (a *App) handleErrorModalKey(event *tcell.EventKey) {
	handler := a.errorModal.InputHandler()
	handler(event, func(p tview.Primitive) {})
	if a.errorModal.Closed() {
		a.closeErrorModal()
	}
}

// closeErrorModal dismisses the error modal and returns to the active tab's
// page. Create/fork failures have already exited any pending agent view, so
// landing on the task list (or whichever tab is active) is the safe outcome.
func (a *App) closeErrorModal() {
	a.mode = modeTaskList
	a.errorModal = nil
	a.pages.RemovePage("error")
	switch a.header.ActiveTab() {
	case widget.TabSettings:
		a.pages.SwitchToPage("settings")
		a.tapp.SetFocus(a.settings)
	case widget.TabHera:
		a.focusHeraTab2Page()
	default:
		a.pages.SwitchToPage("tasks")
		a.tapp.SetFocus(a.tasklist)
	}
}

// --- Fork task ---

// openForkModal shows the fork confirmation modal for the given task.
func (a *App) openForkModal(t *model.Task) {
	cfg := a.db.Config()
	a.forkModal = NewForkTaskModal(t, cfg.Projects)
	a.mode = modeForkTask
	a.pages.AddPage("forktask", a.forkModal, true, true)
	a.pages.SwitchToPage("forktask")
	a.tapp.SetFocus(a.forkModal)
}

// handleForkTaskKey processes keys in the fork task modal.
func (a *App) handleForkTaskKey(event *tcell.EventKey) {
	handler := a.forkModal.InputHandler()
	handler(event, func(p tview.Primitive) {})

	if a.forkModal.Canceled() {
		a.closeForkModal()
		return
	}

	if a.forkModal.Confirmed() {
		source := a.forkModal.Task()
		selectedProj := a.forkModal.SelectedProject()
		if selectedProj == "" {
			selectedProj = source.Project
		}
		a.closeForkModal()
		a.executeFork(source, selectedProj)
	}
}

// closeForkModal dismisses the fork task modal.
func (a *App) closeForkModal() {
	a.mode = modeTaskList
	a.forkModal = nil
	a.pages.RemovePage("forktask")
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
}

// sanitizeTaskName strips control characters and collapses whitespace for
// display-safe task names. Prevents rendering glitches from pasted newlines
// or other non-printable characters.
func sanitizeTaskName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r == '\n' || r == '\r' || r == '\t' {
			b.WriteRune(' ')
		} else if r < 0x20 { // other control chars
			continue
		} else {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// --- Rename task ---

// openRenameModal shows the rename modal for the given task.
func (a *App) openRenameModal(t *model.Task) {
	a.renameTask = t
	a.renameModal = NewRenameTaskForm(t.Name)
	a.mode = modeRenameTask
	a.pages.AddPage("renametask", a.renameModal, true, true)
	a.pages.SwitchToPage("renametask")
	a.tapp.SetFocus(a.renameModal)
}

// handleRenameTaskKey processes keys in the rename task modal.
func (a *App) handleRenameTaskKey(event *tcell.EventKey) {
	a.renameModal.HandleKey(event)

	if a.renameModal.Canceled() {
		a.closeRenameModal()
		return
	}

	if a.renameModal.Done() {
		newName := sanitizeTaskName(a.renameModal.Name())
		if newName == "" {
			a.renameModal.ResetDone()
			a.renameModal.SetError("Name cannot be empty")
			return
		}
		oldName := a.renameTask.Name
		if newName == oldName {
			a.closeRenameModal()
			return
		}
		taskID := a.renameTask.ID
		uxlog.Log("[tui] rename task: %s (%s) → (%s)", taskID, oldName, newName)
		a.db.Rename(taskID, newName) //nolint:errcheck // best-effort
		a.closeRenameModal()
		// Use refreshTasksLocal (not refreshTasksAsync) — rename only changes
		// DB state, no session state changed. Avoids RPC reconciliation race.
		a.refreshTasksLocal()
	}
}

// closeRenameModal dismisses the rename task modal.
func (a *App) closeRenameModal() {
	a.mode = modeTaskList
	a.renameModal = nil
	a.renameTask = nil
	a.pages.RemovePage("renametask")
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
}

// executeFork creates a new task forked from the source, extracting context
// and starting a new agent session. Worktree creation and context extraction
// run in a background goroutine to avoid blocking the UI thread.
// targetProject is the project to create the fork in (may differ from source).
func (a *App) executeFork(source *model.Task, targetProject string) {
	cfg := a.db.Config()
	proj := targetProject
	var projCfg config.Project
	if p, ok := cfg.Projects[proj]; ok {
		projCfg = p
	}

	if projCfg.Path == "" {
		uxlog.Log("[fork] aborted: no project path for %s", proj)
		a.statusbar.SetError("Fork failed: no project path configured")
		return
	}

	if proj != source.Project {
		uxlog.Log("[fork] starting fork of task %s (%s) into project %s (was %s)", source.ID, source.Name, proj, source.Project)
	} else {
		uxlog.Log("[fork] starting fork of task %s (%s)", source.ID, source.Name)
	}

	// Avoid "fork-fork-..." names when re-forking an existing fork.
	forkName := "fork-" + strings.TrimPrefix(source.Name, "fork-")
	rows, cols := a.computePTYSize()

	go func() {
		d, ok := a.db.(*db.DB)
		if !ok {
			// Remote mode: the worktree + session log live on the daemon, so
			// the local context extraction below can't run (it reads local
			// files that don't exist on the client) and the rich context
			// carryover is unavailable. Delegate to the server's fork endpoint,
			// which forks from the source's original prompt + backend. Done
			// before extractForkContext so we don't waste two failed file reads.
			// See gotchas/remote-tui.md for the degradation.
			a.executeForkRemote(source, proj, forkName)
			return
		}

		// Local mode: extract context from the source task (reads session log
		// + git diff) and write it into the fork's worktree.
		ctx := extractForkContext(source)
		input := agent.CreateInput{
			Name:    forkName,
			Prompt:  buildForkPrompt(source, ctx, proj),
			Project: proj,
			Backend: source.Backend,
			Rows:    rows,
			Cols:    cols,
			OnWorktreeCreated: func(wtPath string) error {
				if err := writeForkContextFiles(wtPath, ctx); err != nil {
					return err
				}
				uxlog.Log("[fork] context files written to %s/.context/", wtPath)
				return nil
			},
			BeforeStart: func() { a.startGen.Add(1) },
			AfterStart:  func() { a.startGen.Add(1) },
		}

		created, _, err := agent.CreateAndStart(d, a.runner, input)
		if err != nil {
			a.tapp.QueueUpdateDraw(func() {
				a.statusbar.SetError("Fork failed: " + err.Error())
				a.showError("Fork failed", err.Error())
			})
			uxlog.Log("[fork] create-and-start failed: %v", err)
			return
		}

		a.tapp.QueueUpdateDraw(func() {
			a.recentStarts[created.ID] = time.Now()
			uxlog.Log("[fork] created task %s (%s) forked from %s", created.ID, created.Name, source.ID)
			a.refreshTasksLocal()
			a.tasklist.SelectByID(created.ID)
			a.onTaskSelect(created, true)
		})
	}()
}

// remoteForker is satisfied by *apistore.Store in --remote mode. The fork runs
// server-side via POST /api/tasks/{id}/fork. The remote fork is DEGRADED
// relative to local: the server endpoint does not extract the source's session
// log / git diff or write .context/ fork files (those live on the daemon and
// aren't reconstructed), so the new task starts from the source's original
// prompt + backend rather than a context-enriched fork prompt.
type remoteForker interface {
	ForkTask(ctx context.Context, srcID, name, prompt, project string) (*model.Task, error)
}

// executeForkRemote delegates a fork to the daemon over REST. Called from
// executeFork's goroutine when a.db is not a local *db.DB, so the HTTP round
// trip is already off the UI thread. Passing an empty prompt lets the server
// inherit the source task's prompt. Results land back via QueueUpdateDraw.
func (a *App) executeForkRemote(source *model.Task, project, forkName string) {
	forker, ok := a.db.(remoteForker)
	if !ok {
		// Unreachable with today's store types (the only non-*db.DB store,
		// *apistore.Store, implements remoteForker) — defensive guard.
		uxlog.Log("[fork] no remote handler for task %s", source.ID)
		return
	}
	// Mirror the local fork path's BeforeStart/AfterStart hooks (which bump
	// startGen around CreateAndStart) so a concurrent reconciliation tick can't
	// flip the freshly-forked task to "complete" in the window before its
	// session shows up in the server's running set.
	a.startGen.Add(1)
	defer a.startGen.Add(1)
	created, err := forker.ForkTask(context.Background(), source.ID, forkName, "", project)
	if err != nil {
		a.tapp.QueueUpdateDraw(func() {
			a.statusbar.SetError("Fork failed: " + err.Error())
			a.showError("Fork failed", err.Error())
		})
		uxlog.Log("[fork] remote fork of %s failed: %v", source.ID, err)
		return
	}
	a.tapp.QueueUpdateDraw(func() {
		a.recentStarts[created.ID] = time.Now()
		uxlog.Log("[fork] created task %s (%s) forked from %s (remote, context not carried)", created.ID, created.Name, source.ID)
		// Surface the degradation: a remote fork can't carry the source's
		// session-log / git-diff context (those live on the daemon), so the
		// user gets a visible signal rather than a fork that looks identical
		// to a local context-rich one.
		a.statusbar.SetInfo("Forked (remote: source context not carried)")
		a.refreshTasksLocal()
		a.tasklist.SelectByID(created.ID)
		a.onTaskSelect(created, true)
	})
}

// --- Project form ---

func (a *App) openProjectForm(edit bool, name string, p config.Project) {
	a.projectForm = NewProjectForm()
	a.projectForm.OnBranchFocus = func(path string) {
		go func() {
			branches := gitutil.ListRemoteBranches(path)
			a.tapp.QueueUpdateDraw(func() {
				if a.projectForm != nil {
					a.projectForm.SetBranchOptions(branches)
				}
			})
		}()
	}
	if edit {
		a.projectForm.LoadProject(name, p)
	}
	a.mode = modeProjectForm
	a.pages.AddPage("projectform", a.projectForm, true, true)
	a.pages.SwitchToPage("projectform")
	a.tapp.SetFocus(a.projectForm)
}

func (a *App) handleProjectFormKey(event *tcell.EventKey) {
	a.projectForm.HandleKey(event)

	if a.projectForm.Canceled() {
		a.closeProjectForm()
		return
	}

	if a.projectForm.Done() {
		name, proj := a.projectForm.Result()
		if name == "" {
			a.projectForm.SetError("Name cannot be empty")
			a.projectForm.done = false
			return
		}
		if proj.Path == "" {
			a.projectForm.SetError("Path cannot be empty")
			a.projectForm.done = false
			return
		}
		if err := a.db.SetProject(name, proj); err != nil {
			a.projectForm.SetError("Save error: " + err.Error())
			a.projectForm.done = false
			return
		}
		sandboxMode := "inherit"
		if proj.Sandbox.Enabled != nil {
			if *proj.Sandbox.Enabled {
				sandboxMode = "enabled"
			} else {
				sandboxMode = "disabled"
			}
		}
		uxlog.Log("[settings] saved project %s (path=%s, branch=%s, sandbox=%s)", name, proj.Path, proj.Branch, sandboxMode)
		a.closeProjectForm()
	}
}

func (a *App) closeProjectForm() {
	a.mode = modeTaskList
	a.projectForm = nil
	a.pages.RemovePage("projectform")
	a.settings.Refresh()
	a.pages.SwitchToPage("settings")
	a.tapp.SetFocus(a.settingsPage)
}

// --- AppleEvents allowlist picker ---

// openAppleEventsPicker scans the system for scriptable apps (cached after
// the first call) and opens the multi-select modal preloaded with the
// project's current AllowAppleEvents. Scan runs on a background goroutine
// so the UI doesn't block on /Applications I/O; the modal opens immediately
// with an empty list and SetApps fills it in via QueueUpdateDraw when the
// scan completes (typical: ~400ms for ~300 apps).
func (a *App) openAppleEventsPicker(name string, p config.Project) {
	a.appleEventsPickerProject = name
	a.appleEventsPickerOrig = p
	a.appleEventsPicker = NewAppleEventsPickerModal(name, a.macAppsCache, p.Sandbox.AllowAppleEvents)
	a.mode = modeAppleEventsPicker
	a.pages.AddPage("apple-events-picker", a.appleEventsPicker, true, true)
	a.pages.SwitchToPage("apple-events-picker")
	a.tapp.SetFocus(a.appleEventsPicker)

	// Background scan if the cache is empty. macapps.ScanScriptable does
	// pure filesystem I/O; safe to run off the UI thread.
	if len(a.macAppsCache) == 0 {
		go func() {
			apps := macapps.ScanScriptable(nil)
			a.tapp.QueueUpdateDraw(func() {
				a.macAppsCache = apps
				// Modal may have been closed while we were scanning.
				if a.appleEventsPicker != nil {
					a.appleEventsPicker.SetApps(apps)
				}
			})
			uxlog.Log("[settings] macapps scan: %d scriptable apps cached", len(apps))
		}()
	}
}

// handleAppleEventsPickerKey routes key events to the picker and handles
// the post-input state transitions (save on Done, dismiss on Canceled).
func (a *App) handleAppleEventsPickerKey(event *tcell.EventKey) {
	// tview.Application.SetFocus returns *tview.Application; the modal's
	// InputHandler wants a plain func(tview.Primitive) — wrap it.
	setFocus := func(p tview.Primitive) { a.tapp.SetFocus(p) }
	a.appleEventsPicker.InputHandler()(event, setFocus)

	if a.appleEventsPicker.Canceled() {
		a.closeAppleEventsPicker()
		return
	}
	if a.appleEventsPicker.Done() {
		result := a.appleEventsPicker.Result()
		p := a.appleEventsPickerOrig
		p.Sandbox.AllowAppleEvents = result
		if err := a.db.SetProject(a.appleEventsPickerProject, p); err != nil {
			uxlog.Log("[settings] save AllowAppleEvents failed for %s: %v", a.appleEventsPickerProject, err)
		} else {
			uxlog.Log("[settings] saved AllowAppleEvents for %s: %v", a.appleEventsPickerProject, result)
		}
		a.closeAppleEventsPicker()
	}
}

func (a *App) closeAppleEventsPicker() {
	a.mode = modeTaskList
	a.appleEventsPicker = nil
	a.appleEventsPickerProject = ""
	a.appleEventsPickerOrig = config.Project{}
	a.pages.RemovePage("apple-events-picker")
	a.settings.Refresh()
	a.pages.SwitchToPage("settings")
	a.tapp.SetFocus(a.settingsPage)
}

// --- Schedule form ---

// openScheduleForm opens the schedule editor. Pass an existing schedule to
// edit, or nil to create a new one.
func (a *App) openScheduleForm(s *model.ScheduledTask) {
	cfg := a.db.Config()
	projectNames := make([]string, 0, len(cfg.Projects))
	for name := range cfg.Projects {
		projectNames = append(projectNames, name)
	}
	sort.Strings(projectNames)
	backendNames := make([]string, 0, len(cfg.Backends))
	for name := range cfg.Backends {
		backendNames = append(backendNames, name)
	}
	sort.Strings(backendNames)

	a.scheduleForm = NewScheduleForm(projectNames, backendNames)
	if s != nil {
		a.scheduleForm.LoadSchedule(s)
	}
	a.mode = modeScheduleForm
	a.pages.AddPage("scheduleform", a.scheduleForm, true, true)
	a.pages.SwitchToPage("scheduleform")
	a.tapp.SetFocus(a.scheduleForm)
}

func (a *App) handleScheduleFormKey(event *tcell.EventKey) {
	a.scheduleForm.HandleKey(event)

	if a.scheduleForm.Canceled() {
		a.closeScheduleForm()
		return
	}

	if a.scheduleForm.Done() {
		s := a.scheduleForm.Result()
		if err := s.Validate(); err != nil {
			a.scheduleForm.SetError(err.Error())
			a.scheduleForm.done = false
			return
		}
		var dbErr error
		if s.ID == "" {
			dbErr = a.db.AddSchedule(s)
		} else {
			dbErr = a.db.UpdateSchedule(s)
		}
		if dbErr != nil {
			a.scheduleForm.SetError("Save error: " + dbErr.Error())
			a.scheduleForm.done = false
			return
		}
		uxlog.Log("[settings] saved schedule %s (%s) project=%s schedule=%q enabled=%v", s.ID, s.Name, s.Project, s.Schedule, s.Enabled)
		a.closeScheduleForm()
	}
}

func (a *App) closeScheduleForm() {
	a.mode = modeTaskList
	a.scheduleForm = nil
	a.pages.RemovePage("scheduleform")
	a.settings.Refresh()
	a.pages.SwitchToPage("settings")
	a.tapp.SetFocus(a.settingsPage)
}

func (a *App) deleteSchedule(id string) {
	if err := a.db.DeleteSchedule(id); err != nil {
		uxlog.Log("[settings] delete schedule %s: %v", id, err)
		return
	}
	uxlog.Log("[settings] deleted schedule %s", id)
	a.settings.Refresh()
}

// runScheduleNow fires a schedule out-of-cycle. The TUI does not own the
// daemon's scheduler instance (the daemon runs it), but in-process mode has
// no scheduler at all, so we replicate fire()'s exact behaviour here:
//
//   - Per-fire timestamped name (so rapid double-clicks can't collide on
//     worktree paths) — same format as scheduler.fire via FireName.
//   - LastRunAt/LastTaskID/NextRunAt/LastError bookkeeping update so the
//     Settings detail panel reflects the manual fire.
//
// Both the scheduler and this code path serialise through the DB row's
// last-write-wins update, so a manual fire racing with the once-a-minute
// tick is idempotent on the bookkeeping (the second writer overwrites the
// first; both fired tasks remain). A duplicate-fire race is improbable
// (manual run + tick aligned to the same minute) but not impossible —
// acceptable trade-off given this is an admin-only TUI action.
func (a *App) runScheduleNow(id string) {
	// Remote mode: the daemon's scheduler fires the schedule server-side (task
	// creation + LastRunAt/LastTaskID/NextRunAt bookkeeping). Dispatch in a
	// goroutine and skip the local GetSchedule/ParseSchedule below — for
	// *apistore.Store, GetSchedule is a blocking HTTP round trip that must not
	// run on the UI thread.
	if _, ok := a.db.(*db.DB); !ok {
		go a.runScheduleNowRemote(id)
		return
	}

	// Local mode: replicate the daemon scheduler's fire() bookkeeping. These
	// calls hit SQLite synchronously (fast), so running them on the UI thread
	// before spawning the goroutine is fine.
	s, err := a.db.GetSchedule(id)
	if err != nil {
		uxlog.Log("[settings] run schedule %s: %v", id, err)
		return
	}
	now := time.Now()
	parsed, perr := model.ParseSchedule(s.Schedule)
	if perr != nil {
		s.LastError = perr.Error()
		_ = a.db.UpdateSchedule(s)
		uxlog.Log("[settings] run schedule %s: invalid schedule %q: %v", id, s.Schedule, perr)
		return
	}
	go func() {
		d, ok := a.db.(*db.DB)
		if !ok {
			// Unreachable: a.db was confirmed *db.DB above and doesn't change
			// at runtime. Defensive guard against future store-swap drift.
			return
		}
		task, _, err := agent.CreateAndStart(d, a.runner, agent.CreateInput{
			Name:    scheduler.FireName(s.Name, now),
			Prompt:  s.Prompt,
			Project: s.Project,
			Backend: s.Backend,
		})
		if err != nil {
			s.LastError = err.Error()
			s.LastRunAt = now
			s.NextRunAt = parsed.Next(now)
			_ = a.db.UpdateSchedule(s)
			uxlog.Log("[settings] run schedule %s: %v", id, err)
			a.tapp.QueueUpdateDraw(func() { a.settings.Refresh() })
			return
		}
		s.LastRunAt = now
		s.LastTaskID = task.ID
		s.LastError = ""
		s.NextRunAt = parsed.Next(now)
		if uErr := a.db.UpdateSchedule(s); uErr != nil {
			uxlog.Log("[settings] persist post-fire %s: %v", id, uErr)
		}
		uxlog.Log("[settings] manually fired schedule %s -> task %s", id, task.ID)
		a.tapp.QueueUpdateDraw(func() { a.settings.Refresh() })
	}()
}

// remoteScheduleRunner is satisfied by *apistore.Store in --remote mode. The
// daemon's scheduler runs the whole out-of-cycle fire server-side (task
// creation + schedule-row bookkeeping); the client only triggers it.
type remoteScheduleRunner interface {
	RunSchedule(ctx context.Context, id string) (taskID string, err error)
}

// runScheduleNowRemote delegates a manual schedule fire to the daemon over
// REST. Called from runScheduleNow's goroutine when a.db is not a local
// *db.DB, so the HTTP round trip is already off the UI thread; results land
// back via QueueUpdateDraw. The schedule's LastRunAt/NextRunAt bookkeeping is
// updated server-side, so a Settings refresh re-reads the new state.
func (a *App) runScheduleNowRemote(id string) {
	runner, ok := a.db.(remoteScheduleRunner)
	if !ok {
		// Unreachable with today's store types (the only non-*db.DB store,
		// *apistore.Store, implements remoteScheduleRunner) — defensive guard.
		uxlog.Log("[settings] run schedule %s: no remote handler", id)
		return
	}
	// Bump startGen around the fire so a concurrent reconciliation tick can't
	// flip the schedule's freshly-created task to "complete" before its session
	// appears in the server's running set. (The local runScheduleNow doesn't
	// bump — but the remote round trip + SSE attach widens the window, so the
	// extra protection is worth the two lines.)
	a.startGen.Add(1)
	defer a.startGen.Add(1)
	taskID, err := runner.RunSchedule(context.Background(), id)
	if err != nil {
		uxlog.Log("[settings] run schedule %s (remote): %v", id, err)
		a.tapp.QueueUpdateDraw(func() {
			a.statusbar.SetError("Run schedule failed: " + err.Error())
			a.settings.Refresh()
		})
		return
	}
	uxlog.Log("[settings] manually fired schedule %s -> task %s (remote)", id, taskID)
	a.tapp.QueueUpdateDraw(func() { a.settings.Refresh() })
}

// --- Quick-add form ---

func (a *App) openQuickAddForm() {
	projects, err := a.db.Projects()
	if err != nil {
		uxlog.Log("[tui] openQuickAddForm: failed to load projects: %v", err)
	}
	a.quickAddForm = NewQuickAddForm(projects)
	a.quickAddForm.OnScan = func(dir string) {
		existingPaths := a.quickAddForm.existingPaths
		existingNames := a.quickAddForm.existingNames
		go func() {
			repos, err := scanDirectory(dir, existingPaths, existingNames)
			var errMsg string
			if err != nil {
				errMsg = "Error: " + err.Error()
				uxlog.Log("[quickadd] scan error for %s: %v", dir, err)
			} else {
				uxlog.Log("[quickadd] scanned %s, found %d repos", dir, len(repos))
			}
			a.tapp.QueueUpdateDraw(func() {
				if a.quickAddForm != nil {
					a.quickAddForm.SetScanResult(repos, errMsg)
				}
			})
		}()
	}
	a.mode = modeQuickAdd
	a.pages.AddPage("quickadd", a.quickAddForm, true, true)
	a.pages.SwitchToPage("quickadd")
	a.tapp.SetFocus(a.quickAddForm)
}

func (a *App) handleQuickAddKey(event *tcell.EventKey) {
	a.quickAddForm.HandleKey(event)

	if a.quickAddForm.Canceled() {
		a.closeQuickAddForm()
		return
	}

	if a.quickAddForm.Done() {
		selected := a.quickAddForm.SelectedRepos()
		for _, repo := range selected {
			proj := config.Project{
				Path:   repo.path,
				Branch: "origin/master",
			}
			// Branch defaults to origin/master; worktree code has fallbacks
			// for repos using main or other default branches.
			if err := a.db.SetProject(repo.name, proj); err != nil {
				uxlog.Log("[settings] quick-add: failed to save %s: %v", repo.name, err)
				continue
			}
			uxlog.Log("[settings] quick-add: added project %s (path=%s)", repo.name, repo.path)
		}
		uxlog.Log("[settings] quick-add: added %d projects", len(selected))
		a.closeQuickAddForm()
	}
}

func (a *App) closeQuickAddForm() {
	a.mode = modeTaskList
	a.quickAddForm = nil
	a.pages.RemovePage("quickadd")
	a.settings.Refresh()
	a.pages.SwitchToPage("settings")
	a.tapp.SetFocus(a.settingsPage)
}

// deleteProject opens a confirmation modal before removing a project.
func (a *App) deleteProject(name string) {
	// Count tasks belonging to this project.
	taskCount := 0
	for _, t := range a.tasks {
		if t.Project == name {
			taskCount++
		}
	}
	a.openConfirmDeleteProject(name, taskCount)
}

// openConfirmDeleteProject shows the confirm delete modal for the given project.
func (a *App) openConfirmDeleteProject(name string, taskCount int) {
	a.confirmDeleteProjectModal = modal.NewConfirmDeleteProjectModal(name, taskCount)
	a.mode = modeConfirmDeleteProject
	a.pages.AddPage("confirmdeleteproject", a.confirmDeleteProjectModal, true, true)
	a.pages.SwitchToPage("confirmdeleteproject")
	a.tapp.SetFocus(a.confirmDeleteProjectModal)
}

// handleConfirmDeleteProjectKey processes keys for the project delete confirmation modal.
func (a *App) handleConfirmDeleteProjectKey(event *tcell.EventKey) {
	handler := a.confirmDeleteProjectModal.InputHandler()
	handler(event, func(p tview.Primitive) {})

	if a.confirmDeleteProjectModal.Canceled() {
		a.closeConfirmDeleteProject()
		return
	}

	if a.confirmDeleteProjectModal.Confirmed() {
		name := a.confirmDeleteProjectModal.Name()
		uxlog.Log("[settings] deleting project %s", name)
		if err := a.db.DeleteProject(name); err != nil {
			uxlog.Log("[settings] failed to delete project %s: %v", name, err)
		}
		a.closeConfirmDeleteProject()
		a.settings.Refresh()
		a.refreshTasksLocal()
	}
}

// closeConfirmDeleteProject dismisses the project delete confirmation modal.
func (a *App) closeConfirmDeleteProject() {
	a.mode = modeTaskList
	a.confirmDeleteProjectModal = nil
	a.pages.RemovePage("confirmdeleteproject")
	a.pages.SwitchToPage("settings")
	a.tapp.SetFocus(a.settingsPage)
}

// deleteTask stops the agent, cleans up the worktree/branch, and removes the task from DB.
// Worktree/branch cleanup runs in a background goroutine to avoid blocking the UI.
func (a *App) deleteTask(t *model.Task) {
	uxlog.Log("[tui] deleting task %s (%s)", t.ID, t.Name)

	// Stop the agent if running.
	if a.runner.HasSession(t.ID) {
		if err := a.runner.Stop(t.ID); err != nil {
			uxlog.Log("[tui] failed to stop session for task %s: %v", t.ID, err)
		}
	}

	// Remove session log file.
	os.Remove(agent.SessionLogPath(t.ID)) //nolint:errcheck

	// Remove registered artifacts (manifest rows + on-disk bytes). Best-effort
	// and parity with the REST delete path. In remote mode the apistore stub
	// errors (the daemon already cleaned up on the server side) — log and move
	// on. The dir removal is local-only and harmless either way.
	if n, derr := a.db.DeleteArtifactsForTask(t.ID); derr != nil {
		uxlog.Log("[tui] delete: artifact row cleanup skipped/failed for %s: %v", t.ID, derr)
	} else if n > 0 {
		uxlog.Log("[tui] delete: cleared %d artifact row(s) for %s", n, t.ID)
	}
	os.RemoveAll(agent.ArtifactsDir(t.ID)) //nolint:errcheck

	// Delete from database first so the UI updates immediately.
	if err := a.db.Delete(t.ID); err != nil {
		uxlog.Log("[tui] failed to delete task %s: %v", t.ID, err)
	}
	// Drop any per-task cache entries so deleted tasks don't accumulate
	// in long-lived TUI sessions. Matches the cleanup pattern for
	// pendingRerenderRestart (in handleSessionExitUI).
	a.invalidateAttachCache(t.ID)
	delete(a.pendingRerenderRestart, t.ID)
	a.refreshTasksLocal()

	// Clean up worktree and branch in background — git operations can take seconds.
	cfg := a.db.Config()
	worktree, branch := t.Worktree, t.Branch
	go func() {
		repoDir := agent.ResolveDir(t, cfg)
		if worktree != "" {
			agent.RemoveWorktreeAndBranch(worktree, branch, repoDir)
		} else if branch != "" && repoDir != "" {
			agent.DeleteBranch(repoDir, branch)
			agent.DeleteRemoteBranch(repoDir, branch)
		}
	}()
}

// openConfirmPrune gates pruning behind a y/N confirmation. Pruning is
// destructive — it deletes every completed task plus its worktree and branch —
// so it is never run on a bare Ctrl+R. When no completed tasks exist the modal
// is skipped and a brief status note explains why.
func (a *App) openConfirmPrune() {
	// Re-entrancy guard: a prune is already running (header notice set).
	if a.header.Notice() != "" {
		return
	}

	// Count from the already-loaded task list for the prompt. This is a display
	// snapshot only — the authoritative set is re-derived by db.PruneCompleted
	// when the user confirms, so a task completing during modal dwell time can
	// make the actual deletion count differ from the number shown here. Narrow
	// (human-speed) window, cosmetic-only, and correctness is unaffected.
	completed := 0
	for _, t := range a.tasks {
		if t != nil && t.Status == model.StatusComplete {
			completed++
		}
	}
	if completed == 0 {
		a.statusbar.SetInfo("No completed tasks to prune")
		uxlog.Log("[tui] prune: no completed tasks, skipping confirmation")
		return
	}

	noun := "task"
	if completed != 1 {
		noun = "tasks"
	}
	msg := fmt.Sprintf(
		"Delete %d completed %s and remove their worktrees and branches? This cannot be undone.",
		completed, noun,
	)
	a.confirmPruneModal = modal.NewConfirmModal("Prune completed tasks", msg)
	a.mode = modeConfirmPrune
	a.pages.AddPage("confirmprune", a.confirmPruneModal, true, true)
	a.pages.SwitchToPage("confirmprune")
	a.tapp.SetFocus(a.confirmPruneModal)
}

// handleConfirmPruneKey processes keys in the confirm prune modal.
func (a *App) handleConfirmPruneKey(event *tcell.EventKey) {
	handler := a.confirmPruneModal.InputHandler()
	handler(event, func(p tview.Primitive) {})

	if a.confirmPruneModal.Canceled() {
		uxlog.Log("[tui] prune: canceled at confirmation")
		a.closeConfirmPrune()
		return
	}

	if a.confirmPruneModal.Confirmed() {
		a.closeConfirmPrune()
		a.pruneCompletedTasks()
	}
}

// closeConfirmPrune dismisses the confirm prune modal.
func (a *App) closeConfirmPrune() {
	a.mode = modeTaskList
	a.confirmPruneModal = nil
	a.pages.RemovePage("confirmprune")
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
}

// pruneCompletedTasks removes all completed tasks, cleaning up worktrees and branches.
// Progress is shown via a non-blocking header notice while cleanup runs in background goroutines.
func (a *App) pruneCompletedTasks() {
	// Guard against re-entrancy (rapid double Ctrl+R).
	if a.header.Notice() != "" {
		return
	}

	cfg := a.db.Config()
	projects := make(map[string]string, len(cfg.Projects))
	for name, p := range cfg.Projects {
		projects[name] = p.Path
	}

	// Phase 1 — DB delete + session stop + log removal. Run synchronously so
	// the task list refresh below shows the pruned rows already gone.
	d, ok := a.db.(*db.DB)
	if !ok {
		// Remote mode: the local prune flow (PrunePrepare) shells out to
		// git/PTY directly, which only works against the local daemon. Delegate
		// the whole operation to the server via REST instead.
		a.pruneCompletedRemote()
		return
	}
	preview, err := agent.PrunePrepare(d, agent.PruneOptions{
		WtRoot:   a.wtRoot,
		Projects: projects,
		ResolveRepoDir: func(t *model.Task) string {
			return agent.ResolveDir(t, cfg)
		},
		Runner: a.runner,
	})
	if err != nil {
		uxlog.Log("[tui] prune error: %v", err)
		return
	}
	if len(preview.Pruned) == 0 {
		return
	}

	totalClean := preview.WorktreeCount
	if preview.OrphanCount > 0 {
		totalClean++
	}

	// Refresh task list immediately so pruned rows disappear.
	a.refreshTasksLocal()

	if totalClean == 0 {
		return
	}

	// Show progress as a header notice (non-blocking).
	a.header.SetNotice(fmt.Sprintf("Cleaning worktrees (0/%d)", totalClean))

	// Phase 2 — worktree + orphan cleanup runs in the background and reports
	// progress through the header notice.
	go func() {
		preview.Run(func(done, total int) {
			a.tapp.QueueUpdateDraw(func() {
				a.header.SetNotice(fmt.Sprintf("Cleaning worktrees (%d/%d)", done, total))
			})
		})

		// Fetch session state off UI thread, then clear notice + refresh.
		startGen := a.startGen.Load()
		runningIDs, idleIDs := a.runner.RunningAndIdle()
		a.tapp.QueueUpdateDraw(func() {
			a.header.ClearNotice()
			a.mu.Lock()
			if a.startGen.Load() != startGen {
				uxlog.Log("[tui] prune: startGen changed, skipping reconciliation with stale runningIDs")
				runningIDs = nil
			}
			a.refreshTasksWithIDs(runningIDs, idleIDs)
			a.mu.Unlock()
		})
	}()
}

// remotePruner is satisfied by *apistore.Store in --remote mode. The whole
// prune-completed operation (DB delete, session stop, worktree + orphan
// cleanup) runs server-side on the daemon; the client only fires the request
// and refreshes the task list with the result.
type remotePruner interface {
	PruneCompleted(ctx context.Context) (pruned, worktrees, orphans int, err error)
}

// pruneCompletedRemote delegates prune-completed to the daemon over REST.
// Used when a.db is not a local *db.DB (i.e. --remote mode). The HTTP round
// trip runs in a background goroutine so the UI thread never blocks; results
// land back via QueueUpdateDraw. The caller (pruneCompletedTasks) has already
// passed the re-entrancy guard, so the header notice is clear on entry.
func (a *App) pruneCompletedRemote() {
	pruner, ok := a.db.(remotePruner)
	if !ok {
		// Unreachable with today's store types (the only non-*db.DB store,
		// *apistore.Store, implements remotePruner) — defensive guard against
		// a future third store implementation that supports neither path.
		a.statusbar.SetError("Prune-completed requires local mode (use POST /api/maintenance/prune-completed remotely)")
		return
	}

	a.header.SetNotice("Pruning completed tasks…")
	go func() {
		pruned, worktrees, orphans, err := pruner.PruneCompleted(context.Background())
		a.tapp.QueueUpdateDraw(func() {
			a.header.ClearNotice()
			if err != nil {
				uxlog.Log("[tui] remote prune error: %v", err)
				a.statusbar.SetError("Prune failed: " + err.Error())
				return
			}
			uxlog.Log("[tui] remote prune: pruned=%d worktrees=%d orphans=%d", pruned, worktrees, orphans)
			a.refreshTasksLocal()
		})
	}()
}

// navigateAgentTask switches to the next (+1) or previous (-1) task
// while staying in the agent view.
func (a *App) navigateAgentTask(direction int) {
	next := a.tasklist.AdjacentTask(a.agentState.TaskID, direction)
	if next == nil {
		return
	}
	// Update the task list cursor so it stays in sync.
	a.tasklist.SelectByID(next.ID)
	// Enter the agent view for the new task (reuses onTaskSelect which
	// resets all agent state, wires up the session, kicks off git status, etc.)
	a.onTaskSelect(next, false)
}

// exitAgentView returns to the task list. Always resets the active tab to
// widget.TabTasks so the global key handler routes navigation keys correctly.
func (a *App) exitAgentView() {
	uxlog.Log("[tui] exiting agent view")
	a.mu.Lock()
	a.mode = modeTaskList
	a.agentFocus = focusTerminal
	exitTaskID := a.agentState.TaskID
	a.mu.Unlock()
	if a.focusTracker != nil && exitTaskID != "" {
		a.focusTracker.SetFocused(exitTaskID, false)
	}
	// Reset to the configured default layout so the agentZen flag and panel
	// proportions stay consistent while in the task list; the next agent view
	// re-asserts this on entry anyway.
	a.applyDefaultAgentZen()
	a.agentPane.SetSession(nil)
	a.agentPane.SetFocused(false)
	a.agentPane.ExitDiffMode()
	a.agentPane.ResetVT()
	a.worktreeDir = ""
	// Restore the tab header when returning to root views.
	a.root.ResizeItem(a.header, 1, 0)
	a.header.SetTab(widget.TabTasks)
	a.statusbar.SetTab(widget.TabTasks)
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
	a.statusbar.ClearError()
	// Also clear any transient info notice (e.g. the remote-fork
	// "context not carried" message) so it doesn't linger on the task list.
	a.statusbar.ClearInfo()
}

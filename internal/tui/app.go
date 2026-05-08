package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	dclient "github.com/drn/argus/internal/daemon/client"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/github"
	"github.com/drn/argus/internal/gitutil"
	"github.com/drn/argus/internal/launchagent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/scheduler"
	"github.com/drn/argus/internal/tui/gitpanel"
	"github.com/drn/argus/internal/tui/modal"
	"github.com/drn/argus/internal/tui/taskview"
	"github.com/drn/argus/internal/tui/terminal"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

var prURLRe = regexp.MustCompile(`https://github\.com/[a-zA-Z0-9_.\-]+/[a-zA-Z0-9_.\-]+/pull/\d+`)

const prScanTailSize = 32 * 1024 // bytes of session output to scan for PR URLs

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
	modeBackendForm
	modeScheduleForm
	modeForkTask
	modeRenameTask
	modeLinkPicker
	modeFuzzyLinkPicker
	modeQuickAdd
	modeConfirmDeleteProject
	modeRestartDaemonPrompt
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
	db     *db.DB
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

	// Tabs
	reviews      *ReviewsView
	settings     *SettingsView
	settingsPage *SettingsPage

	// New task form (created on demand)
	newTaskForm *NewTaskForm

	// Confirm delete modal (created on demand)
	confirmDeleteModal        *modal.ConfirmDeleteModal
	confirmDeleteProjectModal *modal.ConfirmDeleteProjectModal

	// Restart-daemon prompt (created on demand when binary mtime mismatch
	// is detected at startup). daemonStale is set by main before Run() and
	// read once inside Run() — no concurrent access, no lock needed.
	restartDaemonModal *modal.RestartDaemonModal
	daemonStale        bool

	// Link picker modals (created on demand)
	linkPickerModal      *LinkPickerModal
	linkPickerPrevPage   string
	fuzzyLinkPickerModal *FuzzyLinkPickerModal

	// Fork task modal (created on demand)
	forkModal *ForkTaskModal

	// Rename task modal (created on demand)
	renameModal *RenameTaskForm
	renameTask  *model.Task

	// Settings forms (created on demand)
	projectForm  *ProjectForm
	backendForm  *BackendForm
	scheduleForm *ScheduleForm
	quickAddForm *QuickAddForm

	// Layout containers
	root      *tview.Flex
	taskPage  *taskview.TaskPage
	agentPage *tview.Flex
	pages     *tview.Pages

	// State
	mode               viewMode
	agentFocus         agentFocus
	agentState         agentview.State
	daemonConnected    bool
	tasks              []*model.Task
	runningIDs         []string
	idleIDs            []string
	worktreeDir        string // resolved worktree dir for current agent view task
	lastGitRefresh     time.Time
	lastTaskGitRefresh time.Time
	lastPreviewTW      uint64            // TotalWritten when preview was last refreshed
	lastPreviewTaskID  string            // task ID for the cached TotalWritten
	lastPreviewLogSize int64             // log file size when dead-session preview was last refreshed
	prScanTW           map[string]uint64 // per-session TotalWritten for PR URL scan throttling

	// Idle-unvisited tracking (for visual InReview promotion)
	idleUnvisited    map[string]bool // task IDs idle since user last opened their agent view
	viewedWhileAgent map[string]bool // tasks viewed in agent view; suppresses idleUnvisited re-add

	// Daemon health
	daemonFailures    int
	daemonRestarting  bool
	lastDaemonRestart time.Time // cooldown: minimum 30s between restart attempts
	daemonClient      *dclient.Client
	restartedClient   *dclient.Client // set after daemon restart

	// Tick control
	tickDone            chan struct{}
	tickCallbackPending atomic.Bool          // debounce: skip enqueue if prior callback hasn't run
	startGen            atomic.Uint64        // double-bumped by startSession (before+after Start RPC); tick captures before its RPC and skips reconciliation on mismatch
	recentStarts        map[string]time.Time // task ID → time of last startSession; grace period prevents false reconciliation

	// pendingNarrowRestart marks tasks whose live session was killed by the
	// auto-rerender path (started with a too-narrow PTY due to the
	// computePTYSize bug). When `handleSessionExitUI` sees an entry in this
	// map, it immediately restarts the session via `--session-id` so Claude
	// re-renders the conversation history at the current (wider) PTY.
	pendingNarrowRestart map[string]bool

	// Worktree root for orphan sweep (default: ~/.argus/worktrees/).
	// Overridden in tests to avoid scanning real worktrees.
	wtRoot string

	// Cached agent-staged clipboard text for the currently-active agent-view
	// task. Polled from the daemon on each tick; used to (a) gate the ctrl+y
	// hotkey so PTY pass-through wins when nothing is staged, (b) toggle the
	// agentHeader hint. Empty string when nothing is staged.
	clipboardPending     string
	clipboardPendingTask string // task ID the cached payload belongs to

	// Screen wrapper. lazyScreen is a passthrough today (see its doc for
	// history); the named type is retained so smoke tests can inject a
	// SimulationScreen through the same indirection production uses.
	screen *lazyScreen
}

// New creates the tui application shell.
//
// Stale-session reconciliation is owned by the runner-holder (daemon Serve, or
// in-process startup in cmd/argus/main.go) — they sweep InProgress → InReview
// before the TUI sees the DB, so the TUI's tick reconciler only handles
// "session exited while we were watching" (always Complete).
func New(database *db.DB, runner agent.SessionProvider, daemonConnected bool) *App {
	// Use the terminal's default background instead of tview's hard-coded black.
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault

	app := &App{
		tapp:             tview.NewApplication(),
		db:               database,
		runner:           runner,
		daemonConnected:  daemonConnected,
		agentState:       agentview.New(),
		tickDone:         make(chan struct{}),
		recentStarts:         make(map[string]time.Time),
		idleUnvisited:        make(map[string]bool),
		viewedWhileAgent:     make(map[string]bool),
		pendingNarrowRestart: make(map[string]bool),
		wtRoot:           filepath.Join(db.DataDir(), "worktrees"),
	}

	if dc, ok := runner.(*dclient.Client); ok {
		app.daemonClient = dc
	}

	app.settings = NewSettingsView(database)
	app.settings.SetDaemonConnected(daemonConnected)
	app.settings.OnRestartDaemon = func() {
		app.mu.Lock()
		app.daemonRestarting = true
		app.lastDaemonRestart = time.Now()
		app.mu.Unlock()
		go app.restartDaemon()
	}
	app.settings.OnUpdateArgus = func() { go app.updateArgus() }
	app.settings.OnToggleAutoStart = func(installed bool) { go app.toggleAutoStart(installed) }
	app.settings.OnNewProject = func() { app.openProjectForm(false, "", config.Project{}) }
	app.settings.OnEditProject = func(name string, p config.Project) { app.openProjectForm(true, name, p) }
	app.settings.OnDeleteProject = func(name string) { app.deleteProject(name) }
	app.settings.OnNewBackend = func() { app.openBackendForm(false, "", config.Backend{}) }
	app.settings.OnEditBackend = func(name string, b config.Backend) { app.openBackendForm(true, name, b) }
	app.settings.OnQuickAdd = func() { app.openQuickAddForm() }
	app.settings.OnNewSchedule = func() { app.openScheduleForm(nil) }
	app.settings.OnEditSchedule = func(s *model.ScheduledTask) { app.openScheduleForm(s) }
	app.settings.OnDeleteSchedule = func(id string) { app.deleteSchedule(id) }
	app.settings.OnRunSchedule = func(id string) { app.runScheduleNow(id) }
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
	a.tasklist.OnStatusChange = func(t *model.Task) {
		uxlog.Log("[tui] manual status change: task %s (%s) → %s", t.ID, t.Name, t.Status)
		a.db.Update(t) //nolint:errcheck // best-effort; display is source of truth
		a.refreshTasksAsync()
	}
	a.tasklist.OnArchive = func(t *model.Task) {
		uxlog.Log("[tui] archive toggle: task %s (%s) archived=%v", t.ID, t.Name, t.Archived)
		a.db.Update(t) //nolint:errcheck // best-effort; display is source of truth
		a.refreshTasksAsync()
	}
	a.tasklist.OnWaitingReview = func(t *model.Task) {
		uxlog.Log("[tui] waiting-for-review toggle: task %s (%s) waiting_review=%v", t.ID, t.Name, t.WaitingReview)
		a.db.Update(t) //nolint:errcheck // best-effort; display is source of truth
		a.refreshTasksAsync()
	}
	a.tasklist.OnOpenPR = func(t *model.Task) {
		exec.Command("open", t.PRURL).Start() //nolint:errcheck
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
	a.taskPreview = NewTaskPreviewPanel()
	a.taskDetail = taskview.NewTaskDetailPanel()

	a.gitPanel = gitpanel.NewGitPanel()
	a.filePanel = gitpanel.NewFilePanel()
	a.agentPane = terminal.NewTerminalPane()
	a.agentHeader = widget.NewAgentHeader()

	// Wire mouse click callbacks so clicking a panel switches agentFocus.
	a.filePanel.OnClick = func() {
		a.agentFocus = focusFiles
		a.updateFocusIndicators()
	}
	a.agentPane.OnClick = func() {
		a.agentFocus = focusTerminal
		a.updateFocusIndicators()
	}
	a.agentPane.OnNeedRedraw = func() {
		a.tapp.QueueUpdateDraw(func() {})
	}
	a.reviews = NewReviewsView()
	a.reviews.SetOnFetch(func(fn func()) {
		go fn()
	})

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

	// Agent page — header + three-panel layout
	agentPanels := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(a.gitPanel, 0, 1, false).
		AddItem(a.agentPane, 0, 3, false).
		AddItem(a.filePanel, 0, 1, false)
	a.agentPage = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.agentHeader, 1, 0, false).
		AddItem(agentPanels, 0, 1, true)

	a.pages = tview.NewPages().
		AddPage("tasks", a.taskPage, true, true).
		AddPage("agent", a.agentPage, true, false).
		AddPage("reviews", a.reviews, true, false).
		AddPage("settings", a.settingsPage, true, false)
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

	a.tapp.SetInputCapture(a.handleGlobalKey)
	a.tapp.SetRoot(a.root, true)
}

// SetDaemonStale records that the connected daemon's binary differs from the
// TUI's. Must be called before Run() — the flag is consumed there.
func (a *App) SetDaemonStale(stale bool) {
	a.daemonStale = stale
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
		go a.restartDaemon()
	} else {
		uxlog.Log("[tui] user skipped daemon restart")
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
	// Note: pages.SetChangedFunc fires forceRedraw → tapp.Sync() during
	// AddPage/SwitchToPage. Sync() pushes onto the buffered (cap-100) updates
	// channel without blocking on a done-channel, so it's safe pre-Run; the
	// queued Sync drains on the first event-loop iteration. The deadlock
	// above is specific to QueueUpdateDraw's per-call done-channel.
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

	// Scan running sessions for GitHub PR URLs (last 32KB of output).
	// Skip sessions whose output hasn't changed since last scan.
	if a.prScanTW == nil {
		a.prScanTW = make(map[string]uint64)
	}
	for _, rid := range runningIDs {
		if sess := runner.Get(rid); sess != nil {
			tw := sess.TotalWritten()
			if prev, ok := a.prScanTW[rid]; ok && prev == tw {
				continue // no new output since last scan
			}
			a.prScanTW[rid] = tw
			tail := sess.RecentOutputTail(prScanTailSize)
			if matches := prURLRe.FindAll(tail, -1); len(matches) > 0 {
				url := string(matches[len(matches)-1])
				if t, err := a.db.Get(rid); err == nil && t.PRURL != url {
					t.PRURL = url
					a.db.Update(t) //nolint:errcheck
					uxlog.Log("[tui] PR detected for task %s: %s", rid, url)
					taskID := rid
					a.tapp.QueueUpdateDraw(func() {
						if a.agentState.TaskID == taskID {
							a.agentPane.SetPRURL(url)
						}
					})
				}
			}
		}
	}

	// Read daemon state for health check BEFORE QueueUpdateDraw — daemon
	// fields are protected by a.mu and don't touch tview widgets.
	a.mu.Lock()
	checkDaemon := a.daemonConnected && a.daemonClient != nil
	a.mu.Unlock()

	// All UI state modifications must run on the tview main goroutine.
	// TaskListView (rows, cursor, expanded), preview panels, agent pane,
	// and reviews have no internal mutex — concurrent access from the tick
	// goroutine races with Draw() and InputHandler() on the tview goroutine.
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
		// reconciliation to wrongly mark it Complete. Pass nil to skip
		// reconciliation this tick; the next tick will have fresh IDs.
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

		// Reviews tab: check diff/comment staleness.
		if a.header.ActiveTab() == widget.TabReviews && a.reviews.SelectedPR() != nil {
			if a.reviews.IsDiffStale() && !a.reviews.DiffFetching() {
				a.reviews.fetchDiffAndComments(a)
			} else if a.reviews.AreCommentsStale() && !a.reviews.CommentsFetching() {
				pr := a.reviews.SelectedPR()
				a.reviews.commentsFetching = true
				go func() {
					comments, err := github.FetchPRComments(pr.RepoOwner, pr.Repo, pr.Number)
					a.tapp.QueueUpdateDraw(func() {
						if err != nil {
							uxlog.Log("[reviews] tick comment refresh error: %v", err)
							a.reviews.commentsFetching = false
							return
						}
						a.reviews.SetComments(comments)
					})
				}()
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
					go a.restartDaemon()
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
	a.restartDaemon()
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
	// Scan last output for PR URL in case agent finished before tick detected it.
	a.scanAndStorePRURL(taskID, lastOutput)
	a.tapp.QueueUpdateDraw(func() {
		a.handleSessionExitUI(taskID, stopped)
	})
}

// HandleSessionExit is called from the daemon client's OnSessionExit callback.
// It updates task status and refreshes the UI.
func (a *App) HandleSessionExit(taskID string, info daemon.ExitInfo) {
	if info.StreamLost {
		uxlog.Log("[tui] stream lost: task=%s — status unchanged, process may still be alive", taskID)
		return
	}
	uxlog.Log("[tui] session exit (daemon): task=%s err=%s stopped=%v lastOutput=%d bytes",
		taskID, info.Err, info.Stopped, len(info.LastOutput))
	// Scan last output for PR URL in case agent finished before tick detected it.
	a.scanAndStorePRURL(taskID, info.LastOutput)
	a.tapp.QueueUpdateDraw(func() {
		a.handleSessionExitUI(taskID, info.Stopped)
	})
}

// handleSessionExitUI runs on the tview main goroutine (inside QueueUpdateDraw).
// Called by both NotifySessionExit (in-process) and HandleSessionExit (daemon).
func (a *App) handleSessionExitUI(taskID string, stopped bool) {
	// Update task status in DB.
	var captureWorktree, captureTaskID string
	tasks, err := a.db.Tasks()
	if err != nil {
		uxlog.Log("[tui] handleSessionExitUI: failed to load tasks: %v", err)
		return
	}
	for _, t := range tasks {
		if t.ID == taskID && t.Status == model.StatusInProgress {
			if stopped {
				t.SetStatus(model.StatusInReview)
			} else {
				t.SetStatus(model.StatusComplete)
			}
			// Check if we need to capture a Codex session ID (done off-thread below).
			if t.SessionID == "" && t.Worktree != "" {
				cfg := a.db.Config()
				if backend, berr := agent.ResolveBackend(t, cfg); berr == nil && agent.IsCodexBackend(backend.Command) {
					captureWorktree = t.Worktree
					captureTaskID = t.ID
				}
			}
			a.db.Update(t) //nolint:errcheck
			uxlog.Log("[tui] task %s (%s) → %s", t.ID, t.Name, t.Status)
			break
		}
	}

	// Capture Codex session ID in a background goroutine — CaptureCodexSessionID
	// opens a SQLite connection which must not block the tview main goroutine.
	if captureWorktree != "" {
		go func(wtPath, tID string) {
			sid, err := agent.CaptureCodexSessionID(wtPath)
			if err != nil {
				uxlog.Log("[tui] codex session ID capture failed for task %s: %v", tID, err)
				return
			}
			uxlog.Log("[tui] captured codex session ID %s for task %s", sid, tID)
			a.tapp.QueueUpdateDraw(func() {
				if t, gerr := a.db.Get(tID); gerr == nil && t != nil {
					t.SessionID = sid
					a.db.Update(t) //nolint:errcheck
				}
			})
		}(captureWorktree, captureTaskID)
	}

	// If maybeKickNarrowRerender flagged this task, immediately resume it
	// at the current (wider) PTY before the user-visible "exited" state has
	// a chance to render. The new session inherits SessionID, so Claude
	// re-loads the conversation and renders history at the wider size. Skip
	// the post-exit clearing/navigation below — startSession will reattach
	// the agent pane in place.
	//
	// Only restart if the user is still viewing this task. If they navigated
	// away after the kick, fall through to the normal exit path so the task
	// settles at InReview and the user can resume it manually later.
	if stopped && a.pendingNarrowRestart[taskID] {
		delete(a.pendingNarrowRestart, taskID)
		a.mu.Lock()
		stillViewing := a.mode == modeAgent && a.agentState.TaskID == taskID
		a.mu.Unlock()
		if !stillViewing {
			uxlog.Log("[tui] narrow-rerender: user navigated away from task=%s, skipping auto-restart", taskID)
			a.statusbar.ClearInfo()
		} else if t, err := a.db.Get(taskID); err == nil && t != nil {
			uxlog.Log("[tui] narrow-rerender: restarting task=%s session=%s", t.ID, t.SessionID)
			// Force the resumed task back into InProgress; handleSessionExitUI
			// flipped it to InReview a moment ago.
			t.SetStatus(model.StatusInProgress)
			a.db.Update(t) //nolint:errcheck
			a.startSession(t)
			a.statusbar.ClearInfo()
			a.refreshTasksAsync()
			return
		} else {
			uxlog.Log("[tui] narrow-rerender: task %s vanished from DB, falling through", taskID)
			a.statusbar.ClearInfo()
		}
	}

	// If we're viewing this task's agent pane and it completed, navigate back
	// to the task list. If stopped (set to in-review), just clear the session.
	a.mu.Lock()
	viewing := a.mode == modeAgent && a.agentState.TaskID == taskID
	a.mu.Unlock()
	if viewing {
		if !stopped {
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

// scanAndStorePRURL scans output for a GitHub PR URL and persists it on the task.
// Safe to call from any goroutine.
func (a *App) scanAndStorePRURL(taskID string, output []byte) {
	if len(output) == 0 {
		return
	}
	matches := prURLRe.FindAll(output, -1)
	if len(matches) == 0 {
		return
	}
	url := string(matches[len(matches)-1])
	t, err := a.db.Get(taskID)
	if err != nil || t.PRURL == url {
		return
	}
	t.PRURL = url
	a.db.Update(t) //nolint:errcheck
	uxlog.Log("[tui] PR detected on exit for task %s: %s", taskID, url)
	a.tapp.QueueUpdateDraw(func() {
		if a.agentState.TaskID == taskID {
			a.agentPane.SetPRURL(url)
		}
	})
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
			// refresh marks a newly-started task Complete.
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
	a.runningIDs = runningIDs
	a.idleIDs = idleIDs

	// Reconcile stale in-progress tasks: if a task is InProgress in the DB
	// but has no running session, mark it Complete. This is the safety net
	// for "session exited while we were watching but the OnSessionExit
	// notification didn't make it through" — the genuine exit path, so
	// Complete is correct. Stale rows from a daemon crash/restart are flipped
	// to InReview by ReconcileStaleSessions before the listener opens, so
	// they shouldn't reach this path in practice.
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
				t.SetStatus(model.StatusComplete)
				a.db.Update(t) //nolint:errcheck
				uxlog.Log("[tui] reconciled stale task %s (%s) → complete (no running session)", t.ID, t.Name)
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

// handleGlobalKey processes key events at the application level.
func (a *App) handleGlobalKey(event *tcell.EventKey) *tcell.EventKey {
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

	// Confirm delete project modal
	if a.mode == modeConfirmDeleteProject && a.confirmDeleteProjectModal != nil {
		a.handleConfirmDeleteProjectKey(event)
		return nil
	}

	// Restart-daemon prompt — shown on startup when daemon binary is stale.
	if a.mode == modeRestartDaemonPrompt && a.restartDaemonModal != nil {
		a.handleRestartDaemonKey(event)
		return nil
	}

	// Project form mode — delegate everything to the form
	if a.mode == modeProjectForm && a.projectForm != nil {
		a.handleProjectFormKey(event)
		return nil
	}

	// Backend form mode — delegate everything to the form
	if a.mode == modeBackendForm && a.backendForm != nil {
		a.handleBackendFormKey(event)
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

	// Rename task modal — delegate everything to the modal
	if a.mode == modeRenameTask && a.renameModal != nil {
		a.handleRenameTaskKey(event)
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
		a.tapp.Stop()
		return nil
	case tcell.KeyCtrlL:
		// Manual refresh — force a full screen re-emit to wipe ghost
		// cells that the diff-based Show() failed to overwrite. Only
		// active outside agent view; in agent mode we fall through so
		// handleAgentKey's Ctrl+L → link-picker binding runs instead.
		if a.mode != modeAgent {
			a.forceRedraw("ctrl+l")
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
	case tcell.KeyCtrlP:
		if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks {
			if t := a.tasklist.SelectedTask(); t != nil && t.PRURL != "" && a.tasklist.OnOpenPR != nil {
				a.tasklist.OnOpenPR(t)
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
	case tcell.KeyCtrlR:
		if a.mode == modeTaskList && a.header.ActiveTab() == widget.TabTasks {
			a.pruneCompletedTasks()
			return nil
		}
	case tcell.KeyLeft:
		if a.mode != modeAgent {
			cur := a.header.ActiveTab()
			if cur > widget.TabTasks {
				a.switchTab(cur - 1)
			}
			return nil
		}
	case tcell.KeyRight:
		if a.mode != modeAgent {
			cur := a.header.ActiveTab()
			if cur < widget.TabSettings {
				a.switchTab(cur + 1)
			}
			return nil
		}
	case tcell.KeyRune:
		// When the task list filter or settings prompt editor is active,
		// let all rune keys through instead of handling global shortcuts.
		if a.mode == modeTaskList && a.tasklist.Filtering() {
			break
		}
		if a.mode == modeTaskList && a.settings.IsEditing() {
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
				a.switchTab(widget.TabReviews)
				return nil
			}
		case '3':
			if a.mode != modeAgent {
				a.switchTab(widget.TabSettings)
				return nil
			}
		}
	}

	switch a.mode {
	case modeAgent:
		return a.handleAgentKey(event)
	}

	// Reviews tab key routing.
	if a.header.ActiveTab() == widget.TabReviews {
		if a.reviews.HandleKey(event, a) {
			return nil
		}
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
	case tcell.KeyCtrlP:
		a.agentPane.OpenPR()
		return nil
	case tcell.KeyCtrlL: // Overrides typical "clear screen" — intercepted before PTY
		a.openAgentLinks()
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
			if a.agentFocus > focusTerminal {
				a.agentFocus--
				a.updateFocusIndicators()
			}
			return nil
		}
	case tcell.KeyRight:
		if event.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			if a.agentFocus < focusFiles {
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

	// 'o' to open PR when finished
	if event.Key() == tcell.KeyRune && event.Rune() == 'o' && (sess == nil || !sess.Alive()) {
		a.agentPane.OpenPR()
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
		case 'o':
			a.openInFinder()
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

func (a *App) openInFinder() {
	f := a.filePanel.SelectedFile()
	if f == nil || a.worktreeDir == "" {
		return
	}
	exec.Command("open", "-R", a.worktreeDir+"/"+f.Path).Start() //nolint:errcheck
}

func (a *App) openInEditor() {
	f := a.filePanel.SelectedFile()
	if f == nil || a.worktreeDir == "" {
		return
	}
	exec.Command("tmux", "new-window", "nvim", a.worktreeDir+"/"+f.Path).Start() //nolint:errcheck
}

func (a *App) openTerminal() {
	if a.worktreeDir == "" {
		return
	}
	exec.Command("tmux", "new-window", "-c", a.worktreeDir).Start() //nolint:errcheck
}

// tcellKeyToBytes converts a tcell key event to raw terminal bytes for PTY input.
func tcellKeyToBytes(ev *tcell.EventKey) []byte {
	if ev.Key() == tcell.KeyRune {
		r := ev.Rune()
		if ev.Modifiers()&tcell.ModAlt != 0 {
			return append([]byte{0x1b}, []byte(string(r))...)
		}
		return []byte(string(r))
	}

	alt := ev.Modifiers()&tcell.ModAlt != 0

	if alt {
		switch ev.Key() {
		case tcell.KeyUp:
			return []byte("\x1b[1;3A")
		case tcell.KeyDown:
			return []byte("\x1b[1;3B")
		case tcell.KeyRight:
			return []byte("\x1b[1;3C")
		case tcell.KeyLeft:
			return []byte("\x1b[1;3D")
		case tcell.KeyDelete:
			return []byte{0x1b, 0x7f}
		}
	}

	switch ev.Key() {
	case tcell.KeyEnter:
		return []byte{'\r'}
	case tcell.KeyTab:
		return []byte{'\t'}
	case tcell.KeyBacktab:
		return []byte("\x1b[Z")
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if alt {
			return []byte{0x1b, 0x7f}
		}
		return []byte{0x7f}
	case tcell.KeyDelete:
		return []byte("\x1b[3~")
	case tcell.KeyUp:
		return []byte("\x1b[A")
	case tcell.KeyDown:
		return []byte("\x1b[B")
	case tcell.KeyRight:
		return []byte("\x1b[C")
	case tcell.KeyLeft:
		return []byte("\x1b[D")
	case tcell.KeyHome:
		return []byte("\x1b[H")
	case tcell.KeyEnd:
		return []byte("\x1b[F")
	case tcell.KeyPgUp:
		return []byte("\x1b[5~")
	case tcell.KeyPgDn:
		return []byte("\x1b[6~")
	case tcell.KeyCtrlA:
		return []byte{0x01}
	case tcell.KeyCtrlB:
		return []byte{0x02}
	case tcell.KeyCtrlC:
		return []byte{0x03}
	case tcell.KeyCtrlD:
		return []byte{0x04}
	case tcell.KeyCtrlE:
		return []byte{0x05}
	case tcell.KeyCtrlF:
		return []byte{0x06}
	case tcell.KeyCtrlG:
		return []byte{0x07}
	case tcell.KeyCtrlH:
		return []byte{0x08}
	case tcell.KeyCtrlK:
		return []byte{0x0b}
	case tcell.KeyCtrlL:
		return []byte{0x0c}
	case tcell.KeyCtrlN:
		return []byte{0x0e}
	case tcell.KeyCtrlO:
		return []byte{0x0f}
	case tcell.KeyCtrlP:
		return []byte{0x10}
	case tcell.KeyCtrlR:
		return []byte{0x12}
	case tcell.KeyCtrlS:
		return []byte{0x13}
	case tcell.KeyCtrlT:
		return []byte{0x14}
	case tcell.KeyCtrlU:
		return []byte{0x15}
	case tcell.KeyCtrlV:
		return []byte{0x16}
	case tcell.KeyCtrlW:
		return []byte{0x17}
	case tcell.KeyCtrlX:
		return []byte{0x18}
	case tcell.KeyCtrlY:
		return []byte{0x19}
	case tcell.KeyCtrlZ:
		return []byte{0x1a}
	case tcell.KeyEscape:
		return []byte{0x1b}
	}
	return nil
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
	case widget.TabReviews:
		a.mode = modeTaskList // reuse task list mode for non-agent tabs
		a.pages.SwitchToPage("reviews")
		a.tapp.SetFocus(a.reviews)
		if a.reviews.CanFetchPRList() {
			a.reviews.StartLoading()
			a.reviews.fetchPRList(a)
		}
	case widget.TabSettings:
		a.mode = modeTaskList
		a.settings.Refresh()
		a.pages.SwitchToPage("settings")
		a.tapp.SetFocus(a.settingsPage)
	}
}

// forceRedraw queues a tcell Sync on tview's update channel; it fires on the
// next event cycle after any in-flight Draw completes. `Sync()` invalidates
// tcell's dirty-cell tracking so every cell is re-emitted, which overwrites
// ghost content that the diff-based `Show()` considered up-to-date.
//
// Wired in two places (don't add a third without thinking hard):
//   - `pages.SetChangedFunc` — fires on every AddPage/RemovePage/SwitchToPage,
//     covering modal open/close, tab switch, and agent view enter/exit.
//   - `tasklist.OnLayoutChange` — fires when row composition changes
//     (auto-rename, status flips, archive toggles); tview can't observe these.
//
// The only intentional direct callsite is Ctrl+L (user-initiated refresh
// where no page mutation occurs).
func (a *App) forceRedraw(reason string) {
	uxlog.Log("[tui] force redraw: %s", reason)
	a.tapp.Sync()
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

// refreshPreview fetches output for the selected task and pre-renders cells.
// Called from the tview main goroutine (via QueueUpdateDraw in onTick).
// The TotalWritten/LogSize cache short-circuits on repeated calls; first
// load of a large dead session may briefly block the UI.
func (a *App) refreshPreview(taskID string) {
	w, h := a.taskPreview.DrawSize()
	if w <= 0 || h <= 0 {
		return
	}

	sess := a.runner.Get(taskID)
	if sess != nil {
		// Skip 256KB ring buffer copy when output hasn't changed.
		// Protected by a.mu — accessed from tick goroutine and onTaskCursorChange goroutine.
		tw := sess.TotalWritten()
		a.mu.Lock()
		if taskID == a.lastPreviewTaskID && tw == a.lastPreviewTW {
			a.mu.Unlock()
			return
		}
		a.lastPreviewTaskID = taskID
		a.lastPreviewTW = tw
		a.lastPreviewLogSize = 0 // reset so dead-session path re-reads log after session exit
		a.mu.Unlock()
		raw := sess.RecentOutput()
		// Use the PTY's actual dimensions for the emulator so cursor
		// positioning and text wrapping match the agent view. The preview
		// viewport (w x h) selects which rows to display.
		emuCols, emuRows := w, h
		if ptyCols, ptyRows := sess.PTYSize(); ptyCols > 0 {
			emuCols = ptyCols
			if ptyRows > 0 {
				emuRows = ptyRows
			}
		}
		a.taskPreview.RefreshOutput(raw, emuCols, emuRows, w, h)
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
			a.taskPreview.RefreshOutput(logData, w, h, w, h)
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

	a.agentHeader.SetTaskName(task.Name)
	// Leave pane taskID empty — task isn't in the DB yet, no log to replay.
	a.agentPane.SetTaskID("")
	a.agentPane.SetPRURL("")
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
	a.mode = modeAgent
	a.agentFocus = focusTerminal
	a.agentState.Reset(task.ID, task.Name)
	a.mu.Unlock()
	a.agentHeader.SetTaskName(task.Name)
	a.agentPane.SetTaskID(task.ID)
	a.agentPane.SetPRURL(task.PRURL)
	a.agentPane.ResetVT()
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
		// Detect narrow-stuck sessions (initial PTY too narrow to ever recover
		// via SIGWINCH). If the session is idle, kill it so the deferred
		// restart in handleSessionExitUI brings it back at the current wider
		// PTY and Claude re-renders the conversation history.
		a.maybeKickNarrowRerender(task, sess)
		return
	}
	// No live session — clear any leaked pending-restart marker so a future
	// re-entry isn't silently blocked from kicking again.
	a.reapStaleNarrowRestart(task.ID, sess)

	// Auto-start sessions when entering agent view for a non-running task.
	// Covers both fresh tasks (no SessionID) and interrupted sessions
	// (e.g., daemon restart with a preserved SessionID). Excludes completed,
	// archived, and waiting-for-review tasks — those are view-only until the
	// user explicitly presses Enter to restart.
	// After the sess.Alive() early-return above, any session here is dead.
	if autoStart && task.Status != model.StatusComplete && !task.Archived && !task.WaitingReview {
		sid := task.SessionID
		if sid == "" {
			sid = "(none)"
		}
		uxlog.Log("[tui] auto-starting session for task %s (sessionID=%s)", task.ID, sid)
		a.startSession(task)
	}
}

// narrowInitialPTYThreshold is the cutoff below which a session's initial
// PTY size is considered "buggy small". The original `computePTYSize` bug
// produced 20x8; a real laid-out agent pane on the smallest reasonable
// terminal is wider. 60 leaves headroom while still excluding any plausible
// real-world layout.
const narrowInitialPTYThreshold = 60

// narrowRerenderMargin is the minimum delta between current panel cols and
// the session's initial cols required to bother restarting. Avoids ping-pong
// when the session was started a hair too narrow.
const narrowRerenderMargin = 30

// narrowRerenderDecision is the outcome of shouldKickNarrowRerender — kept
// separate so the gating logic is testable in isolation from the live RPCs.
type narrowRerenderDecision int

const (
	narrowRerenderSkip narrowRerenderDecision = iota
	narrowRerenderDeferBusy                   // criteria match but session is busy; retry next entry
	narrowRerenderKick                        // stop the session; handleSessionExitUI will restart it
)

// shouldKickNarrowRerender decides whether the live session should be
// stopped+resumed to re-render its scrollback at a wider PTY. Pure function
// for testability — see maybeKickNarrowRerender for the wired version.
//
// initCols=0 means "unknown" (e.g., daemon predates the field) and is
// treated as already-sane to avoid surprise restarts.
func shouldKickNarrowRerender(hasSessionID bool, initCols, panelCols int, idle, alreadyPending bool) narrowRerenderDecision {
	if !hasSessionID || alreadyPending {
		return narrowRerenderSkip
	}
	if initCols <= 0 || initCols >= narrowInitialPTYThreshold {
		return narrowRerenderSkip
	}
	if panelCols-initCols < narrowRerenderMargin {
		return narrowRerenderSkip
	}
	if !idle {
		return narrowRerenderDeferBusy
	}
	return narrowRerenderKick
}

// maybeKickNarrowRerender detects sessions whose initial PTY was so narrow
// that Claude rendered the entire conversation history at narrow column
// positions, and triggers a kill+resume cycle so the resumed session
// re-renders at the current (wider) panel size. Stops the session when the
// criteria match — the deferred restart fires in handleSessionExitUI via
// pendingNarrowRestart. No-op for backends that can't resume (no SessionID),
// for already-restarted tasks, or when the session is busy (don't kill mid
// tool-call).
//
// The decision RPCs (`InitialPTYSize`, `IsIdle`) hit the daemon over the
// Unix socket, so we do them on a background goroutine and dispatch the
// kick back via QueueUpdateDraw — never block the tview main goroutine on
// network I/O. The panel size and the session pointer are captured up front
// on the main goroutine where it's safe to read them.
func (a *App) maybeKickNarrowRerender(task *model.Task, sess agent.SessionHandle) {
	if task == nil || sess == nil || !sess.Alive() {
		return
	}
	if task.SessionID == "" {
		return // backend doesn't support --session-id resume; nothing to do
	}
	if a.pendingNarrowRestart[task.ID] {
		return // a kick is already in flight for this task
	}
	taskID := task.ID
	_, panelCols := a.computePTYSize() // safe: GetInnerRect on the main goroutine

	go func() {
		// RPC calls — must NOT happen on the tview main goroutine.
		initCols, _ := sess.InitialPTYSize()
		idle := sess.IsIdle()
		a.tapp.QueueUpdateDraw(func() {
			// Re-check liveness and the pending flag — anything could have
			// changed during the RPC round-trip.
			if !sess.Alive() || a.pendingNarrowRestart[taskID] {
				return
			}
			decision := shouldKickNarrowRerender(true, initCols, int(panelCols), idle, false)
			switch decision {
			case narrowRerenderSkip:
				return
			case narrowRerenderDeferBusy:
				uxlog.Log("[tui] narrow-rerender deferred: task=%s busy (init=%d panel=%d)", taskID, initCols, panelCols)
				return
			case narrowRerenderKick:
				uxlog.Log("[tui] narrow-rerender: stopping task=%s session=%s (init=%dx panel=%dx)", taskID, task.SessionID, initCols, panelCols)
				a.statusbar.SetInfo("Re-rendering at full width…")
				a.pendingNarrowRestart[taskID] = true
				if err := sess.Stop(); err != nil {
					uxlog.Log("[tui] narrow-rerender: stop failed task=%s err=%v", taskID, err)
					delete(a.pendingNarrowRestart, taskID)
					a.statusbar.ClearInfo()
				}
			}
		})
	}()
}

// reapStaleNarrowRestart clears a leaked pendingNarrowRestart entry when the
// session it referred to has died without firing handleSessionExitUI (daemon
// crash mid-stop, lost stream notification, etc.). Called from onTaskSelect
// before maybeKickNarrowRerender so a stuck flag can't permanently block
// recovery.
func (a *App) reapStaleNarrowRestart(taskID string, sess agent.SessionHandle) {
	if !a.pendingNarrowRestart[taskID] {
		return
	}
	if sess != nil && sess.Alive() {
		return // exit notification still pending; let it run
	}
	uxlog.Log("[tui] narrow-rerender: reaping stale pending flag for task=%s", taskID)
	delete(a.pendingNarrowRestart, taskID)
	a.statusbar.ClearInfo()
}

// onNewTask opens the new task form.
func (a *App) onNewTask() {
	cfg := a.db.Config()

	a.newTaskForm = NewNewTaskForm(
		cfg.Projects, a.tasklist.SelectedProject(),
		cfg.Backends, cfg.Defaults.Backend,
	)
	a.newTaskForm.OnBranchFocus = func(path string) {
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
	// Trigger initial branch load for the default project.
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
		var projCfg config.Project
		if p, ok := a.db.Config().Projects[proj]; ok {
			projCfg = p
		}

		// Close form immediately so the UI feels responsive.
		a.closeNewTaskForm()

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
			BaseBranch: task.Branch,
			// INVARIANT: the new-task form has no name field — task.Name is
			// always GenerateNameFromPrompt(prompt). If a name field is added
			// later, gate this on whether the user typed one.
			AutoName: true,
			Rows:       rows,
			Cols:       cols,
			BeforeStart: func() { a.startGen.Add(1) },
			AfterStart:  func() { a.startGen.Add(1) },
		}

		go func() {
			created, _, err := agent.CreateAndStart(a.db, a.runner, input)
			if err != nil {
				a.tapp.QueueUpdateDraw(func() {
					a.statusbar.ClearInfo()
					a.statusbar.SetError("Create failed: " + err.Error())
					// pending agent view has empty agentState.TaskID — only
					// exit if the user is still there (hasn't navigated away).
					if a.mode == modeAgent && a.agentState.TaskID == "" {
						a.exitAgentView()
					}
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

// computePTYSize returns the best available PTY dimensions for the agent
// terminal pane. Prefers the host terminal size with the 1:3:1 agent-page
// layout ratio (always accurate when stdout is a TTY); falls back to the
// pane's actual inner rect; finally defaults to 24x80.
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

// ptySizeFromHostTerm derives the agent PTY size from the host terminal,
// applying the agent page's 1:3:1 column flex and the header/footer/border
// row deductions. Returns 0,0 when the input is unusable.
func ptySizeFromHostTerm(tw, th int, err error) (rows, cols uint16) {
	if err != nil || tw <= 0 || th <= 0 {
		return 0, 0
	}
	// Agent page column flex: 1 (gitPanel) + 3 (agentPane) + 1 (filePanel)
	// → center gets 3/5 of width. Deduct 2 for the agent pane's border.
	centerW := max(tw*3/5-2, 20)
	// Row layout: tab header(1) is hidden in agent view, agent header(1),
	// statusbar(1), and pane border(2) ⇒ 5 row deduction. The tab header is
	// hidden by the time the session starts, but accounting for it would
	// over-shoot when this runs from the new-task flow before the resize.
	centerH := max(th-5, 5)
	return uint16(centerH), uint16(centerW)
}

// ptySizeFromPaneRect derives the agent PTY size from the agent pane's full
// box rect (as returned by GetInnerRect — the agent pane has no native tview
// border, so its inner rect equals its outer rect). The pane draws its own
// 1-cell border via widget.DrawBorderedPanel, so the visible content area is
// pw-2 by ph-2.
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
	return uint16(max(ph-2, 5)), uint16(max(pw-2, 20))
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
	// resume the conversation later. Codex captures its ID post-exit
	// (in handleSessionExitUI → CaptureCodexSessionID).
	if !resume {
		backend, berr := agent.ResolveBackend(task, cfg)
		if berr == nil && !agent.IsCodexBackend(backend.Command) {
			task.SessionID = model.GenerateSessionID()
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
		task.SessionID = ""
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
	a.pages.RemovePage("newtask")
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
}

// resolveProjectForRepo finds the Argus project whose name or directory basename
// matches the given GitHub repo name (case-insensitive). Name matches take
// priority over basename matches for deterministic results. Returns ("", zero)
// if no match is found.
func resolveProjectForRepo(projects map[string]config.Project, repo string) (string, config.Project) {
	repo = strings.ToLower(repo)
	// First pass: exact name match (highest priority).
	for name, proj := range projects {
		if strings.ToLower(name) == repo {
			return name, proj
		}
	}
	// Second pass: directory basename match.
	for name, proj := range projects {
		if proj.Path != "" && strings.ToLower(filepath.Base(proj.Path)) == repo {
			return name, proj
		}
	}
	return "", config.Project{}
}

// truncateRunes truncates s to at most maxRunes runes.
func truncateRunes(s string, maxRunes int) string {
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

// startReviewTask creates a task to review the given PR, or navigates to
// the existing task if one is already linked. Called from Ctrl+R in reviews tab.
func (a *App) startReviewTask(pr *github.PR) {
	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", pr.RepoOwner, pr.Repo, pr.Number)

	// Check for existing task linked to this PR.
	existing, err := a.db.TaskByPRURL(prURL)
	if err != nil {
		uxlog.Log("[reviews] failed to look up task by PR URL: %v", err)
	}
	if existing != nil {
		uxlog.Log("[reviews] found existing review task %s for %s", existing.ID, prURL)
		a.switchTab(widget.TabTasks)
		a.refreshTasksLocal()
		a.tasklist.SelectByID(existing.ID)
		a.onTaskSelect(existing, true)
		return
	}

	cfg := a.db.Config()
	projName, projCfg := resolveProjectForRepo(cfg.Projects, pr.Repo)
	if projName == "" {
		a.statusbar.SetError("No project matches repo " + pr.Repo)
		return
	}

	reviewPrompt := cfg.Defaults.ReviewPrompt
	if reviewPrompt == "" {
		reviewPrompt = "/review"
	}
	prompt := fmt.Sprintf("%s %s", reviewPrompt, prURL)
	taskName := truncateRunes(
		fmt.Sprintf("review-pr-%d-%s", pr.Number, model.GenerateNameFromPrompt(pr.Title)),
		50,
	)
	backend := cfg.Defaults.Backend

	if projCfg.Path == "" {
		// No worktree needed — create task directly on the UI thread.
		task := &model.Task{
			Name:    taskName,
			Status:  model.StatusPending,
			Project: projName,
			Prompt:  prompt,
			Backend: backend,
			PRURL:   prURL,
		}
		task.Sandboxed = a.resolveSandboxed(task)
		if err := a.db.Add(task); err != nil {
			uxlog.Log("[reviews] failed to persist review task: %v", err)
			a.statusbar.SetError("Failed to create task: " + err.Error())
			return
		}
		uxlog.Log("[reviews] created review task %s (%s) for %s", task.ID, task.Name, prURL)
		a.switchTab(widget.TabTasks)
		a.refreshTasksLocal()
		a.tasklist.SelectByID(task.ID)
		a.onTaskSelect(task, true)
		return
	}

	// CreateAndStart runs in a background goroutine to avoid blocking the
	// tview event loop — same pattern as executeFork.
	uxlog.Log("[reviews] starting review task creation for %s", prURL)
	rows, cols := a.computePTYSize()
	input := agent.CreateInput{
		Name:        taskName,
		Prompt:      prompt,
		Project:     projName,
		Backend:     backend,
		PRURL:       prURL,
		Rows:        rows,
		Cols:        cols,
		BeforeStart: func() { a.startGen.Add(1) },
		AfterStart:  func() { a.startGen.Add(1) },
	}

	go func() {
		created, _, err := agent.CreateAndStart(a.db, a.runner, input)
		if err != nil {
			a.tapp.QueueUpdateDraw(func() {
				a.statusbar.SetError("Review task failed: " + err.Error())
			})
			uxlog.Log("[reviews] create-and-start failed: %v", err)
			return
		}

		a.tapp.QueueUpdateDraw(func() {
			a.recentStarts[created.ID] = time.Now()
			uxlog.Log("[reviews] created review task %s (%s) for %s", created.ID, created.Name, prURL)
			a.switchTab(widget.TabTasks)
			a.refreshTasksLocal()
			a.tasklist.SelectByID(created.ID)
			a.onTaskSelect(created, true)
		})
	}()
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
		// Extract context from the source task (reads session log + git diff).
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

		created, _, err := agent.CreateAndStart(a.db, a.runner, input)
		if err != nil {
			a.tapp.QueueUpdateDraw(func() {
				a.statusbar.SetError("Fork failed: " + err.Error())
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

// --- Backend form ---

func (a *App) openBackendForm(edit bool, name string, b config.Backend) {
	a.backendForm = NewBackendForm()
	if edit {
		a.backendForm.LoadBackend(name, b)
	}
	a.mode = modeBackendForm
	a.pages.AddPage("backendform", a.backendForm, true, true)
	a.pages.SwitchToPage("backendform")
	a.tapp.SetFocus(a.backendForm)
}

func (a *App) handleBackendFormKey(event *tcell.EventKey) {
	a.backendForm.HandleKey(event)

	if a.backendForm.Canceled() {
		a.closeBackendForm()
		return
	}

	if a.backendForm.Done() {
		name, backend := a.backendForm.Result()
		if name == "" {
			a.backendForm.SetError("Name cannot be empty")
			a.backendForm.done = false
			return
		}
		if backend.Command == "" {
			a.backendForm.SetError("Command cannot be empty")
			a.backendForm.done = false
			return
		}
		if err := a.db.SetBackend(name, backend); err != nil {
			a.backendForm.SetError("Save error: " + err.Error())
			a.backendForm.done = false
			return
		}
		uxlog.Log("[settings] saved backend %s (cmd=%s)", name, backend.Command)
		a.closeBackendForm()
	}
}

func (a *App) closeBackendForm() {
	a.mode = modeTaskList
	a.backendForm = nil
	a.pages.RemovePage("backendform")
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
		task, _, err := agent.CreateAndStart(a.db, a.runner, agent.CreateInput{
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

	// Delete from database first so the UI updates immediately.
	if err := a.db.Delete(t.ID); err != nil {
		uxlog.Log("[tui] failed to delete task %s: %v", t.ID, err)
	}
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

// pruneCompletedTasks removes all completed tasks, cleaning up worktrees and branches.
// Progress is shown via a non-blocking header notice while cleanup runs in background goroutines.
func (a *App) pruneCompletedTasks() {
	// Guard against re-entrancy (rapid double Ctrl+R).
	if a.header.Notice() != "" {
		return
	}
	pruned, err := a.db.PruneCompleted()
	if err != nil {
		uxlog.Log("[tui] prune error: %v", err)
		return
	}
	if len(pruned) == 0 {
		return
	}

	uxlog.Log("[tui] pruning %d completed tasks", len(pruned))

	// Stop sessions synchronously (fast, in-process).
	for _, t := range pruned {
		if a.runner.HasSession(t.ID) {
			_ = a.runner.Stop(t.ID)
		}
	}

	// Remove session logs for all pruned tasks.
	for _, t := range pruned {
		os.Remove(agent.SessionLogPath(t.ID)) //nolint:errcheck
	}

	cfg := a.db.Config()

	var toClean []*model.Task
	for _, t := range pruned {
		if t.Worktree != "" {
			toClean = append(toClean, t)
		}
	}

	// Count orphaned worktrees not tracked in the DB.
	// Skip orphan sweep if WorktreePaths fails — an empty map would
	// misidentify all worktrees as orphans.
	knownPaths, err := a.db.WorktreePaths()
	orphanCount := 0
	if err != nil {
		uxlog.Log("[tui] WorktreePaths failed, skipping orphan sweep: %v", err)
	} else {
		// PruneCompleted already deleted these from the DB, so their
		// worktree dirs would be misidentified as orphans. Mark them
		// known so they aren't double-counted.
		for _, t := range toClean {
			knownPaths[t.Worktree] = true
		}
		orphanCount = countOrphanedWorktrees(a.wtRoot, knownPaths)
	}

	// Each task worktree is one unit; orphan sweep is one batch unit.
	orphanUnits := 0
	if orphanCount > 0 {
		orphanUnits = 1
	}
	totalClean := len(toClean) + orphanUnits

	if totalClean == 0 {
		a.refreshTasksLocal()
		return
	}

	// Show progress as a header notice (non-blocking).
	a.header.SetNotice(fmt.Sprintf("Cleaning worktrees (0/%d)", totalClean))

	// Build project name → path map for orphan sweep.
	projects := make(map[string]string)
	for name, p := range cfg.Projects {
		projects[name] = p.Path
	}

	// Refresh task list immediately so pruned tasks disappear.
	a.refreshTasksLocal()

	// Parallel cleanup in background goroutines.
	go func() {
		var wg sync.WaitGroup
		var cleaned atomic.Int32

		// Clean up each pruned task's worktree in parallel.
		for _, t := range toClean {
			wg.Add(1)
			go func(t *model.Task) {
				defer wg.Done()
				repoDir := agent.ResolveDir(t, cfg)
				uxlog.Log("[tui] prune cleanup: task=%s name=%q worktree=%q branch=%q repoDir=%q project=%q",
					t.ID, t.Name, t.Worktree, t.Branch, repoDir, t.Project)
				agent.RemoveWorktreeAndBranch(t.Worktree, t.Branch, repoDir)
				n := cleaned.Add(1)
				a.tapp.QueueUpdateDraw(func() {
					a.header.SetNotice(fmt.Sprintf("Cleaning worktrees (%d/%d)", n, totalClean))
				})
			}(t)
		}

		// Sweep orphaned worktrees in parallel with task cleanup.
		if orphanCount > 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				swept := sweepOrphanedWorktrees(a.wtRoot, knownPaths, projects)
				uxlog.Log("[tui] orphan sweep cleaned %d directories", swept)
				n := cleaned.Add(1)
				a.tapp.QueueUpdateDraw(func() {
					a.header.SetNotice(fmt.Sprintf("Cleaning worktrees (%d/%d)", n, totalClean))
				})
			}()
		}

		wg.Wait()

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
	a.mu.Unlock()
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
}

package tui2

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

var prURLRe = regexp.MustCompile(`https://github\.com/[a-zA-Z0-9_.\-]+/[a-zA-Z0-9_.\-]+/pull/\d+`)

// viewMode identifies the active view.
type viewMode int

const (
	modeTaskList viewMode = iota
	modeAgent
	modeNewTask
	modeConfirmDelete
	modePruning
	modeProjectForm
	modeBackendForm
	modeLaunchToDo
	modeForkTask
	modeConfirmCleanupToDos
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
	header       *Header
	statusbar    *StatusBar
	tasklist     *TaskListView
	taskGitPanel *GitPanel // git status for selected task (task list center-top)
	taskPreview  *TaskPreviewPanel
	taskDetail   *TaskDetailPanel
	agentPane    *TerminalPane
	agentHeader  *AgentHeader
	gitPanel     *GitPanel // git status for agent view (left panel)
	filePanel    *FilePanel

	// Tabs
	todos        *ToDosView
	reviews      *ReviewsView
	settings     *SettingsView
	settingsPage *SettingsPage

	// New task form (created on demand)
	newTaskForm *NewTaskForm

	// Confirm delete modal (created on demand)
	confirmDeleteModal *ConfirmDeleteModal

	// Prune modal (created on demand)
	pruneModal *PruneModal

	// Launch to-do modal (created on demand)
	launchToDoModal    *LaunchToDoModal
	cleanupToDosModal *ConfirmCleanupToDosModal

	// Fork task modal (created on demand)
	forkModal *ForkTaskModal

	// Settings forms (created on demand)
	projectForm *ProjectForm
	backendForm *BackendForm

	// Layout containers
	root      *tview.Flex
	taskPage  *TaskPage
	agentPage *tview.Flex
	pages     *tview.Pages

	// State
	mode            viewMode
	agentFocus      agentFocus
	agentState      agentview.State
	daemonConnected bool
	tasks           []*model.Task
	runningIDs      []string
	idleIDs         []string
	worktreeDir     string // resolved worktree dir for current agent view task
	lastGitRefresh     time.Time
	lastTaskGitRefresh time.Time

	// Idle-unvisited tracking (for visual InReview promotion)
	idleUnvisited    map[string]bool // task IDs idle since user last opened their agent view
	viewedWhileAgent map[string]bool // tasks viewed in agent view; suppresses idleUnvisited re-add

	// Daemon health
	daemonFailures   int
	daemonRestarting bool
	daemonClient     *dclient.Client
	restartedClient  *dclient.Client // set after daemon restart

	// Tick control
	tickDone chan struct{}

	// Worktree root for orphan sweep (default: ~/.argus/worktrees/).
	// Overridden in tests to avoid scanning real worktrees.
	wtRoot string
}

// New creates the tui2 application shell.
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
		idleUnvisited:    make(map[string]bool),
		viewedWhileAgent: make(map[string]bool),
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
		app.mu.Unlock()
		go app.restartDaemon()
	}
	app.settings.OnNewProject = func() { app.openProjectForm(false, "", config.Project{}) }
	app.settings.OnEditProject = func(name string, p config.Project) { app.openProjectForm(true, name, p) }
	app.settings.OnNewBackend = func() { app.openBackendForm(false, "", config.Backend{}) }
	app.settings.OnEditBackend = func(name string, b config.Backend) { app.openBackendForm(true, name, b) }
	app.settingsPage = NewSettingsPage(app.settings)

	app.todos = NewToDosView()
	app.todos.SetApp(app.tapp)
	cfg := database.Config()
	vaultPath := cfg.KB.ArgusVaultPath
	if vaultPath == "" {
		vaultPath = config.DefaultArgusVaultPath()
	}
	app.todos.SetVaultPath(vaultPath)
	app.todos.OnLaunch = func(item ToDoItem) {
		app.openLaunchToDoModal(item)
	}
	app.todos.OnStatusChange = func(t *model.Task) {
		uxlog.Log("[todos] manual status change: task %s (%s) → %s", t.ID, t.Name, t.Status)
		app.db.Update(t) //nolint:errcheck // best-effort; display is source of truth
		app.refreshTasksAsync()
	}

	app.buildUI()
	app.refreshTasks()

	return app
}

// buildUI constructs the tview widget tree.
func (a *App) buildUI() {
	a.header = NewHeader()
	a.statusbar = NewStatusBar()

	a.tasklist = NewTaskListView()
	a.tasklist.OnSelect = a.onTaskSelect
	a.tasklist.OnNew = a.onNewTask
	a.tasklist.OnCursorChange = a.onTaskCursorChange
	a.tasklist.OnStatusChange = func(t *model.Task) {
		uxlog.Log("[tui2] manual status change: task %s (%s) → %s", t.ID, t.Name, t.Status)
		a.db.Update(t) //nolint:errcheck // best-effort; display is source of truth
		a.refreshTasksAsync()
	}
	a.tasklist.OnArchive = func(t *model.Task) {
		uxlog.Log("[tui2] archive toggle: task %s (%s) archived=%v", t.ID, t.Name, t.Archived)
		a.db.Update(t) //nolint:errcheck // best-effort; display is source of truth
		a.refreshTasksAsync()
	}
	a.tasklist.OnOpenPR = func(t *model.Task) {
		exec.Command("open", t.PRURL).Start() //nolint:errcheck
	}

	a.taskGitPanel = NewGitPanel()
	a.taskPreview = NewTaskPreviewPanel()
	a.taskDetail = NewTaskDetailPanel()

	a.gitPanel = NewGitPanel()
	a.filePanel = NewFilePanel()
	a.agentPane = NewTerminalPane()
	a.agentHeader = NewAgentHeader()

	// Wire mouse click callbacks so clicking a panel switches agentFocus.
	a.filePanel.OnClick = func() {
		a.agentFocus = focusFiles
		a.updateFocusIndicators()
	}
	a.agentPane.OnClick = func() {
		a.agentFocus = focusTerminal
		a.updateFocusIndicators()
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
	a.taskPage = NewTaskPage(taskFlex, a.tasklist)

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
		AddPage("todos", a.todos, true, false).
		AddPage("agent", a.agentPage, true, false).
		AddPage("reviews", a.reviews, true, false).
		AddPage("settings", a.settingsPage, true, false)

	a.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 1, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.statusbar, 1, 0, false)

	a.tapp.SetInputCapture(a.handleGlobalKey)
	a.tapp.SetRoot(a.root, true)
	a.tapp.EnableMouse(true)
	a.tapp.EnablePaste(true)
}

// Run starts the application event loop.
func (a *App) Run() error {
	go a.tickLoop()
	defer close(a.tickDone)

	uxlog.Log("[tui2] starting tcell/tview application")
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

// onTick handles periodic updates.
func (a *App) onTick() {
	// Fetch running IDs OUTSIDE the lock — this is an RPC call that can take
	// up to 5 seconds on timeout, and holding a.mu during that blocks the
	// entire UI (QueueUpdateDraw callbacks can't run while the tick goroutine
	// holds the mutex and waits for RPC).
	runningIDs, idleIDs := a.runner.RunningAndIdle()

	// Scan running sessions for GitHub PR URLs (last 32KB of output).
	for _, rid := range runningIDs {
		if sess := a.runner.Get(rid); sess != nil {
			tail := sess.RecentOutputTail(32 * 1024)
			if matches := prURLRe.FindAll(tail, -1); len(matches) > 0 {
				url := string(matches[len(matches)-1])
				if t, err := a.db.Get(rid); err == nil && t.PRURL != url {
					t.PRURL = url
					a.db.Update(t) //nolint:errcheck
					uxlog.Log("[tui2] PR detected for task %s: %s", rid, url)
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

	a.mu.Lock()
	a.refreshTasksWithIDs(runningIDs, idleIDs)
	checkDaemon := a.daemonConnected && a.daemonClient != nil
	taskID := ""
	if a.mode == modeAgent {
		taskID = a.agentState.TaskID
	}
	if a.pruneModal != nil {
		a.pruneModal.Tick()
	}
	a.mu.Unlock()

	// Daemon health check
	if checkDaemon {
		a.mu.Lock()
		restarting := a.daemonRestarting
		a.mu.Unlock()
		if !restarting {
			if err := a.daemonClient.Ping(); err != nil {
				a.mu.Lock()
				a.daemonFailures++
				failures := a.daemonFailures
				a.mu.Unlock()
				if failures >= 3 {
					uxlog.Log("[tui2] daemon unreachable after %d pings, restarting...", failures)
					a.mu.Lock()
					a.daemonRestarting = true
					a.mu.Unlock()
					go a.restartDaemon()
				}
			} else {
				a.mu.Lock()
				a.daemonFailures = 0
				a.mu.Unlock()
			}
		}
	}

	// Refresh task list side panels.
	if previewTaskID := a.taskPreview.TaskID(); previewTaskID != "" && a.mode == modeTaskList {
		a.refreshPreview(previewTaskID)
		// Also refresh git status for the selected task periodically.
		if sel := a.tasklist.SelectedTask(); sel != nil && sel.Worktree != "" && time.Since(a.lastTaskGitRefresh) > 3*time.Second {
			a.lastTaskGitRefresh = time.Now()
			go a.fetchTaskGitStatus(sel.ID, sel.Worktree)
		}
	}

	// Update agent pane session
	if taskID != "" {
		sess := a.runner.Get(taskID)

		// PTY size sync moved to startAgentRedrawLoop (200ms) for faster
		// initial resize. The tick no longer calls SyncPTYSize.

		a.tapp.QueueUpdateDraw(func() {
			if sess != nil {
				a.agentPane.SetSession(sess)
			}
			// Refresh git status periodically
			if a.worktreeDir != "" && time.Since(a.lastGitRefresh) > 3*time.Second {
				go a.fetchGitStatus(taskID, a.worktreeDir)
			}
		})
	} else {
		a.tapp.QueueUpdateDraw(func() {})
	}

	// Reviews tab: check diff/comment staleness.
	if a.header.ActiveTab() == TabReviews && a.reviews.SelectedPR() != nil {
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
}

// restartDaemon kills the old daemon, auto-starts a new one, and reconnects.
// Must be called from a goroutine (not UI thread).
func (a *App) restartDaemon() {
	uxlog.Log("[tui2] restarting daemon...")

	// Try graceful shutdown via RPC.
	if a.daemonClient != nil {
		a.daemonClient.Close()
	}

	sockPath := daemon.DefaultSocketPath()
	dclient.WaitForShutdown(sockPath, 3*time.Second)

	// Auto-start new daemon.
	newClient, err := dclient.AutoStart(sockPath)
	if err != nil {
		uxlog.Log("[tui2] daemon restart failed: %v", err)
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

	uxlog.Log("[tui2] daemon restarted, reconnected")

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
		a.mu.Unlock()

		a.settings.SetDaemonRestarting(false)

		// Reset in-progress tasks to pending, preserving SessionID for resume.
		for _, t := range a.db.Tasks() {
			if t.Status == model.StatusInProgress {
				t.SetStatus(model.StatusPending)
				a.db.Update(t) //nolint:errcheck
				uxlog.Log("[tui2] reset task %s to pending (daemon restarted)", t.ID)
			}
		}
		a.refreshTasksAsync()
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
	uxlog.Log("[tui2] session exit (in-process): task=%s stopped=%v err=%v", taskID, stopped, err)
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
		uxlog.Log("[tui2] stream lost: task=%s — status unchanged, process may still be alive", taskID)
		return
	}
	uxlog.Log("[tui2] session exit (daemon): task=%s err=%s stopped=%v lastOutput=%d bytes",
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
	tasks := a.db.Tasks()
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
			uxlog.Log("[tui2] task %s (%s) → %s", t.ID, t.Name, t.Status)
			break
		}
	}

	// Capture Codex session ID in a background goroutine — CaptureCodexSessionID
	// opens a SQLite connection which must not block the tview main goroutine.
	if captureWorktree != "" {
		go func(wtPath, tID string) {
			sid, err := agent.CaptureCodexSessionID(wtPath)
			if err != nil {
				uxlog.Log("[tui2] codex session ID capture failed for task %s: %v", tID, err)
				return
			}
			uxlog.Log("[tui2] captured codex session ID %s for task %s", sid, tID)
			a.tapp.QueueUpdateDraw(func() {
				if t, gerr := a.db.Get(tID); gerr == nil && t != nil {
					t.SessionID = sid
					a.db.Update(t) //nolint:errcheck
				}
			})
		}(captureWorktree, captureTaskID)
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
	uxlog.Log("[tui2] PR detected on exit for task %s: %s", taskID, url)
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
	go func() {
		runningIDs, idleIDs := a.runner.RunningAndIdle()
		a.tapp.QueueUpdateDraw(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
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
	a.tasks = a.db.Tasks()
	a.runningIDs = runningIDs
	a.idleIDs = idleIDs

	// Sync todo-task associations so the ToDos tab shows linked task status.
	a.todos.SyncTasks(a.db.TasksByTodoPath())

	// Reconcile stale in-progress tasks: if a task is InProgress in the DB
	// but has no running session, mark it Complete. This handles cases where
	// the exit callback didn't fire (daemon restart, TUI restart, etc.).
	// Only reconcile when connected to a daemon — the daemon is the source of
	// truth for running sessions. In-process mode has its own onFinish callback.
	// Note: InReview tasks are intentionally non-running (set by handleSessionExitUI
	// when the user stops an agent) and must NOT be reconciled to Complete.
	// IMPORTANT: runningIDs is nil when the daemon RPC failed — skip reconciliation
	// to avoid marking all active tasks as Complete due to a transient error.
	// Also skip during daemon restart — the new daemon has no sessions yet, so
	// reconciliation would incorrectly mark all InProgress tasks as Complete.
	if a.daemonConnected && runningIDs != nil && !a.daemonRestarting {
		runningSet := make(map[string]bool, len(runningIDs))
		for _, id := range runningIDs {
			runningSet[id] = true
		}
		for _, t := range a.tasks {
			if t.Status == model.StatusInProgress && !runningSet[t.ID] {
				t.SetStatus(model.StatusComplete)
				a.db.Update(t) //nolint:errcheck
				uxlog.Log("[tui2] reconciled stale task %s (%s) → complete (no running session)", t.ID, t.Name)
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
	a.tasklist.Tick()
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

	// Prune modal — absorb all keys while cleanup is running
	if a.mode == modePruning {
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

	// Launch to-do modal — delegate everything to the modal
	if a.mode == modeLaunchToDo && a.launchToDoModal != nil {
		a.handleLaunchToDoKey(event)
		return nil
	}

	// Fork task modal — delegate everything to the modal
	if a.mode == modeForkTask && a.forkModal != nil {
		a.handleForkTaskKey(event)
		return nil
	}

	// Cleanup to-dos confirmation modal
	if a.mode == modeConfirmCleanupToDos && a.cleanupToDosModal != nil {
		a.handleCleanupToDosKey(event)
		return nil
	}

	switch event.Key() {
	case tcell.KeyCtrlC:
		if a.mode == modeAgent {
			// Forward ctrl+c to the PTY if session is alive; otherwise ignore
			if sess := a.agentPane.Session(); sess != nil && sess.Alive() {
				if _, err := sess.WriteInput([]byte{0x03}); err != nil {
					uxlog.Log("[tui2] write ctrl+c to PTY failed: %v", err)
				}
			}
			return nil
		}
		a.tapp.Stop()
		return nil
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
		if a.mode == modeTaskList && a.header.ActiveTab() == TabTasks {
			if t := a.tasklist.SelectedTask(); t != nil {
				a.openConfirmDelete(t)
				return nil
			}
		}
	case tcell.KeyCtrlP:
		if a.mode == modeTaskList && a.header.ActiveTab() == TabTasks {
			if t := a.tasklist.SelectedTask(); t != nil && t.PRURL != "" && a.tasklist.OnOpenPR != nil {
				a.tasklist.OnOpenPR(t)
				return nil
			}
		}
	case tcell.KeyCtrlF:
		if a.mode == modeTaskList && a.header.ActiveTab() == TabTasks {
			if t := a.tasklist.SelectedTask(); t != nil && t.Worktree != "" {
				a.openForkModal(t)
				return nil
			}
		}
	case tcell.KeyCtrlR:
		if a.mode == modeTaskList && a.header.ActiveTab() == TabTasks {
			a.pruneCompletedTasks()
			return nil
		}
		if a.mode == modeTaskList && a.header.ActiveTab() == TabToDos {
			a.cleanupCompletedToDos()
			return nil
		}
	case tcell.KeyLeft:
		if a.mode != modeAgent {
			cur := a.header.ActiveTab()
			if cur > TabTasks {
				a.switchTab(cur - 1)
			}
			return nil
		}
	case tcell.KeyRight:
		if a.mode != modeAgent {
			cur := a.header.ActiveTab()
			if cur < TabSettings {
				a.switchTab(cur + 1)
			}
			return nil
		}
	case tcell.KeyRune:
		switch event.Rune() {
		case 'q':
			if a.mode == modeTaskList {
				a.tapp.Stop()
				return nil
			}
		case '1':
			if a.mode != modeAgent {
				a.switchTab(TabTasks)
				return nil
			}
		case '2':
			if a.mode != modeAgent {
				a.switchTab(TabToDos)
				return nil
			}
		case '3':
			if a.mode != modeAgent {
				a.switchTab(TabReviews)
				return nil
			}
		case '4':
			if a.mode != modeAgent {
				a.switchTab(TabSettings)
				return nil
			}
		}
	}

	switch a.mode {
	case modeAgent:
		return a.handleAgentKey(event)
	}

	// To Dos tab key routing.
	if a.header.ActiveTab() == TabToDos {
		if a.todos.HandleKey(event) {
			return nil
		}
	}

	// Reviews tab key routing.
	if a.header.ActiveTab() == TabReviews {
		if a.reviews.HandleKey(event, a) {
			return nil
		}
	}

	// Settings tab key routing.
	if a.header.ActiveTab() == TabSettings {
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
				uxlog.Log("[tui2] write escape to PTY failed: %v", err)
			}
			a.agentPane.ResetScroll()
		}
		return nil
	case tcell.KeyCtrlP:
		a.agentPane.OpenPR()
		return nil
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
			uxlog.Log("[tui2] enter-to-restart: db.Get(%s) failed: %v", taskID, err)
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
				uxlog.Log("[tui2] write to PTY failed: %v", err)
			}
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
		a.filePanel.SetFiles(files)
		uxlog.Log("[tui2] git status refreshed: %d files", len(files))
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
func (a *App) switchTab(t Tab) {
	a.header.SetTab(t)
	a.statusbar.SetTab(t)

	switch t {
	case TabTasks:
		if a.mode == modeAgent {
			a.exitAgentView()
		}
		a.mode = modeTaskList
		a.pages.SwitchToPage("tasks")
		a.tapp.SetFocus(a.tasklist)
	case TabToDos:
		a.mode = modeTaskList
		a.todos.RefreshAsync(a.tapp)
		a.pages.SwitchToPage("todos")
	case TabReviews:
		a.mode = modeTaskList // reuse task list mode for non-agent tabs
		a.pages.SwitchToPage("reviews")
		if a.reviews.CanFetchPRList() {
			a.reviews.StartLoading()
			a.reviews.fetchPRList(a)
		}
	case TabSettings:
		a.mode = modeTaskList
		a.settings.Refresh()
		a.pages.SwitchToPage("settings")
	}
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
// Called from the tick goroutine — RPC and file I/O are safe here.
func (a *App) refreshPreview(taskID string) {
	w, h := a.taskPreview.DrawSize()
	if w <= 0 || h <= 0 {
		return
	}

	sess := a.runner.Get(taskID)
	if sess != nil {
		raw := sess.RecentOutput()
		// Use the PTY's actual width for the emulator so text wraps at
		// the same column as in the agent view. Draw() clips to panel size.
		emuCols := w
		if ptyCols, _ := sess.PTYSize(); ptyCols > 0 {
			emuCols = ptyCols
		}
		a.taskPreview.RefreshOutput(raw, emuCols, h)
		return
	}

	// No live session — try session log file.
	logData := LoadSessionLog(taskID)
	if len(logData) > 0 {
		a.taskPreview.RefreshOutput(logData, w, h)
		return
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

// onTaskSelect handles Enter on a task — enters the agent view.
func (a *App) onTaskSelect(task *model.Task) {
	uxlog.Log("[tui2] entering agent view for task %s (%s)", task.ID, task.Name)

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

	// Resolve worktree dir
	a.worktreeDir = task.Worktree
	a.lastGitRefresh = time.Time{}
	a.gitPanel.Clear()

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

	// Kick off initial git status
	if a.worktreeDir != "" {
		go a.fetchGitStatus(task.ID, a.worktreeDir)
	}

	// Start continuous redraw loop for existing running sessions.
	if sess != nil && sess.Alive() {
		a.startAgentRedrawLoop(task.ID, sess)
	}
}

// onNewTask opens the new task form.
func (a *App) onNewTask() {
	cfg := a.db.Config()

	a.newTaskForm = NewNewTaskForm(
		cfg.Projects, a.tasklist.SelectedProject(),
		cfg.Backends, cfg.Defaults.Backend,
	)

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

		// Create worktree and persist task
		proj := a.newTaskForm.SelectedProject()
		var projCfg config.Project
		if p, ok := a.db.Config().Projects[proj]; ok {
			projCfg = p
		}

		if projCfg.Path != "" {
			wtPath, finalName, branchName, err := agent.CreateWorktree(projCfg.Path, proj, task.Name, task.Branch)
			if err != nil {
				a.newTaskForm.SetError("Worktree error: " + err.Error())
				return
			}
			task.Worktree = wtPath
			task.Name = finalName
			task.Branch = branchName
		}

		a.db.Add(task)
		uxlog.Log("[tui2] created task %s (%s)", task.ID, task.Name)

		a.closeNewTaskForm()

		// NOTE: Do NOT call refreshTasksAsync() here. The task was just created
		// as Pending and startSession will set it to InProgress. An async refresh
		// races: its RPC snapshot captures running IDs before the session exists,
		// then reconciliation sees InProgress + not-in-running-set → marks Complete.
		// Use refreshTasksLocal (no RPC) to make the task list consistent.
		a.refreshTasksLocal()

		// Enter agent view FIRST so panel has real dimensions for PTY sizing.
		a.onTaskSelect(task)
		a.startSession(task)
	}
}

// startSession starts a session for the current task (agent view must already be active).
func (a *App) startSession(task *model.Task) {
	cfg := a.db.Config()

	// Use actual panel dimensions so the agent process starts at the correct
	// width — agents format their initial output for the PTY size at launch,
	// and existing output isn't reflowed on later resize. GetInnerRect may
	// return 0 before the first Draw(), so fall back to computing from the
	// terminal size and the 1:3:1 agent page layout ratio.
	rows, cols := uint16(24), uint16(80)
	_, _, pw, ph := a.agentPane.GetInnerRect()
	if pw > 0 && ph > 0 {
		// Border is drawn inside the box rect — content area is 2 smaller each dimension.
		cols = uint16(max(pw-2, 20))
		rows = uint16(max(ph-2, 5))
	} else if tw, th, err := term.GetSize(int(os.Stdout.Fd())); err == nil && tw > 0 && th > 0 {
		// Agent page is a 1:3:1 flex — center panel gets 3/5 of width.
		// Border is drawn inside, deduct 2 for border cells.
		centerW := tw*3/5 - 2
		if centerW < 20 {
			centerW = 20
		}
		// Height minus header(1), agent header(1), statusbar(1), and border(2).
		centerH := th - 5
		if centerH < 5 {
			centerH = 5
		}
		cols = uint16(centerW)
		rows = uint16(centerH)
	}

	resume := task.SessionID != ""

	// For Claude-style backends, generate a session ID on first run so we can
	// resume the conversation later. Codex captures its ID post-exit
	// (in handleSessionExitUI → CaptureCodexSessionID).
	if !resume {
		backend, berr := agent.ResolveBackend(task, cfg)
		if berr == nil && !agent.IsCodexBackend(backend.Command) {
			task.SessionID = model.GenerateSessionID()
			a.db.Update(task) //nolint:errcheck
			uxlog.Log("[tui2] generated session ID %s for task %s", task.SessionID, task.ID)
		}
	}

	sess, err := a.runner.Start(task, cfg, rows, cols, resume)
	if err != nil {
		uxlog.Log("[tui2] failed to start session: %v", err)
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
	a.db.Update(task) //nolint:errcheck

	// Now that the session exists, attach it to the terminal pane.
	a.agentPane.SetSession(sess)

	a.startAgentRedrawLoop(task.ID, sess)
}

// startAgentRedrawLoop runs a goroutine that triggers redraws every 200ms
// while the session is alive and the agent view is active. The 1-second tick
// is too slow for a live terminal. Self-terminates when the session exits or
// the user leaves the agent view.
func (a *App) startAgentRedrawLoop(taskID string, sess agent.SessionHandle) {
	uxlog.Log("[tui2] startAgentRedrawLoop: taskID=%s", taskID)
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

// openLaunchToDoModal shows the project selection modal for launching a to-do as a task.
func (a *App) openLaunchToDoModal(item ToDoItem) {
	cfg := a.db.Config()
	a.launchToDoModal = NewLaunchToDoModal(item, cfg.Projects, cfg.Defaults.TodoProject)
	a.mode = modeLaunchToDo
	a.pages.AddPage("launchtodo", a.launchToDoModal, true, true)
	a.pages.SwitchToPage("launchtodo")
	a.tapp.SetFocus(a.launchToDoModal)
}

// handleLaunchToDoKey processes keys in the launch to-do modal.
func (a *App) handleLaunchToDoKey(event *tcell.EventKey) {
	handler := a.launchToDoModal.InputHandler()
	handler(event, func(p tview.Primitive) {})

	if a.launchToDoModal.Canceled() {
		a.closeLaunchToDoModal()
		return
	}

	if a.launchToDoModal.Done() {
		item := a.launchToDoModal.Item()
		proj := a.launchToDoModal.SelectedProject()

		cfg := a.db.Config()
		var projCfg config.Project
		if p, ok := cfg.Projects[proj]; ok {
			projCfg = p
		}

		// Build the prompt: user instructions + note context.
		userPrompt := a.launchToDoModal.Prompt()
		prompt := buildToDoPrompt(userPrompt, item.Content)

		task := &model.Task{
			Name:     item.Name,
			Status:   model.StatusPending,
			Project:  proj,
			Prompt:   prompt,
			Backend:  cfg.Defaults.Backend,
			TodoPath: item.Path,
		}

		if projCfg.Path != "" {
			task.Branch = projCfg.Branch
			wtPath, finalName, branchName, err := agent.CreateWorktree(projCfg.Path, proj, task.Name, task.Branch)
			if err != nil {
				a.launchToDoModal.SetError("Worktree error: " + err.Error())
				return
			}
			task.Worktree = wtPath
			task.Name = finalName
			task.Branch = branchName
		}

		a.db.Add(task)
		uxlog.Log("[todos] launched to-do %q as task %s (%s)", item.Name, task.ID, task.Name)

		a.closeLaunchToDoModal()
		// Use refreshTasksLocal (not refreshTasks/refreshTasksAsync) to avoid
		// reconciliation race: the session doesn't exist yet, so async RPC would
		// see InProgress + no running session → incorrectly mark Complete.
		a.refreshTasksLocal()
		a.onTaskSelect(task)
		a.startSession(task)
	}
}

// closeLaunchToDoModal closes the launch to-do modal and returns to the To Dos tab.
func (a *App) closeLaunchToDoModal() {
	a.mode = modeTaskList
	a.launchToDoModal = nil
	a.pages.RemovePage("launchtodo")
	a.pages.SwitchToPage("todos")
}

// cleanupCompletedToDos shows a confirmation modal to delete vault files for completed to-dos.
func (a *App) cleanupCompletedToDos() {
	items := a.todos.CompletedItems()
	if len(items) == 0 {
		return
	}
	a.cleanupToDosModal = NewConfirmCleanupToDosModal(len(items))
	a.mode = modeConfirmCleanupToDos
	a.pages.AddPage("cleanuptodos", a.cleanupToDosModal, true, true)
	a.pages.SwitchToPage("cleanuptodos")
	a.tapp.SetFocus(a.cleanupToDosModal)
}

// handleCleanupToDosKey processes keys in the cleanup confirmation modal.
func (a *App) handleCleanupToDosKey(event *tcell.EventKey) {
	handler := a.cleanupToDosModal.InputHandler()
	handler(event, func(p tview.Primitive) {})

	if a.cleanupToDosModal.Canceled() {
		a.closeCleanupToDosModal()
		return
	}
	if a.cleanupToDosModal.Confirmed() {
		a.executeToDoCleanup()
		a.closeCleanupToDosModal()
	}
}

// executeToDoCleanup deletes vault .md files for completed to-dos.
func (a *App) executeToDoCleanup() {
	items := a.todos.CompletedItems()
	vaultPath := a.todos.VaultPath()
	for _, item := range items {
		// Guard: only remove files within the configured vault directory.
		if vaultPath == "" || !strings.HasPrefix(item.Path, vaultPath+string(os.PathSeparator)) {
			uxlog.Log("[todos] cleanup: skipping %s (not in vault %s)", item.Path, vaultPath)
			continue
		}
		if err := os.Remove(item.Path); err != nil {
			uxlog.Log("[todos] cleanup: failed to remove %s: %v", item.Path, err)
		} else {
			uxlog.Log("[todos] cleanup: removed %s", item.Path)
		}
	}
	// Refresh vault scan async to avoid blocking the UI thread on disk I/O.
	a.todos.RefreshAsync(a.tapp)
}

// closeCleanupToDosModal closes the cleanup confirmation modal.
func (a *App) closeCleanupToDosModal() {
	a.mode = modeTaskList
	a.cleanupToDosModal = nil
	a.pages.RemovePage("cleanuptodos")
	a.pages.SwitchToPage("todos")
}

// openConfirmDelete shows the confirm delete modal for the given task.
func (a *App) openConfirmDelete(t *model.Task) {
	a.confirmDeleteModal = NewConfirmDeleteModal(t)
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
	a.forkModal = NewForkTaskModal(t)
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
		a.closeForkModal()
		a.executeFork(source)
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

// executeFork creates a new task forked from the source, extracting context
// and starting a new agent session. Worktree creation and context extraction
// run in a background goroutine to avoid blocking the UI thread.
func (a *App) executeFork(source *model.Task) {
	cfg := a.db.Config()
	proj := source.Project
	var projCfg config.Project
	if p, ok := cfg.Projects[proj]; ok {
		projCfg = p
	}

	if projCfg.Path == "" {
		uxlog.Log("[fork] aborted: no project path for %s", proj)
		a.statusbar.SetError("Fork failed: no project path configured")
		return
	}

	uxlog.Log("[fork] starting fork of task %s (%s)", source.ID, source.Name)

	go func() {
		// Extract context from the source task (reads session log + git diff).
		ctx := extractForkContext(source)

		// Create worktree for the new task.
		baseBranch := projCfg.Branch
		forkName := "fork-" + strings.TrimPrefix(source.Name, "fork-")
		wtPath, finalName, branchName, err := agent.CreateWorktree(projCfg.Path, proj, forkName, baseBranch)
		if err != nil {
			a.tapp.QueueUpdateDraw(func() {
				a.statusbar.SetError("Fork worktree error: " + err.Error())
			})
			uxlog.Log("[fork] worktree creation failed: %v", err)
			return
		}

		// Write .context/ files into the new worktree.
		if err := writeForkContextFiles(wtPath, ctx); err != nil {
			a.tapp.QueueUpdateDraw(func() {
				a.statusbar.SetError("Fork context error: " + err.Error())
			})
			uxlog.Log("[fork] context file write failed: %v", err)
			return
		}

		uxlog.Log("[fork] context files written to %s/.context/", wtPath)

		// Create the task and start the session on the tview thread.
		a.tapp.QueueUpdateDraw(func() {
			task := &model.Task{
				Name:     finalName,
				Status:   model.StatusPending,
				Project:  proj,
				Prompt:   buildForkPrompt(source, ctx),
				Backend:  source.Backend,
				Branch:   branchName,
				Worktree: wtPath,
			}

			if err := a.db.Add(task); err != nil {
				uxlog.Log("[fork] db.Add failed: %v — cleaning up worktree", err)
				a.statusbar.SetError("Fork failed: " + err.Error())
				go removeWorktreeAndBranch(wtPath, branchName, projCfg.Path)
				return
			}
			uxlog.Log("[fork] created task %s (%s) forked from %s", task.ID, task.Name, source.ID)

			a.refreshTasksLocal()
			a.onTaskSelect(task)
			a.startSession(task)
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
		uxlog.Log("[settings] saved project %s (path=%s, branch=%s)", name, proj.Path, proj.Branch)
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

// deleteTask stops the agent, cleans up the worktree/branch, and removes the task from DB.
// Worktree/branch cleanup runs in a background goroutine to avoid blocking the UI.
func (a *App) deleteTask(t *model.Task) {
	uxlog.Log("[tui2] deleting task %s (%s)", t.ID, t.Name)

	// Stop the agent if running.
	if a.runner.HasSession(t.ID) {
		if err := a.runner.Stop(t.ID); err != nil {
			uxlog.Log("[tui2] failed to stop session for task %s: %v", t.ID, err)
		}
	}

	// Remove session log file.
	os.Remove(agent.SessionLogPath(t.ID)) //nolint:errcheck

	// Delete from database first so the UI updates immediately.
	if err := a.db.Delete(t.ID); err != nil {
		uxlog.Log("[tui2] failed to delete task %s: %v", t.ID, err)
	}
	a.refreshTasksLocal()

	// Clean up worktree and branch in background — git operations can take seconds.
	cfg := a.db.Config()
	worktree, branch := t.Worktree, t.Branch
	go func() {
		repoDir := agent.ResolveDir(t, cfg)
		if worktree != "" {
			removeWorktreeAndBranch(worktree, branch, repoDir)
		} else if branch != "" && repoDir != "" {
			deleteBranch(repoDir, branch)
			deleteRemoteBranch(repoDir, branch)
		}
	}()
}

// pruneCompletedTasks removes all completed tasks, cleaning up worktrees and branches.
// Shows a progress modal during cleanup to prevent premature TUI exit.
func (a *App) pruneCompletedTasks() {
	pruned, err := a.db.PruneCompleted()
	if err != nil {
		uxlog.Log("[tui2] prune error: %v", err)
		return
	}
	if len(pruned) == 0 {
		return
	}

	uxlog.Log("[tui2] pruning %d completed tasks", len(pruned))

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
		uxlog.Log("[tui2] WorktreePaths failed, skipping orphan sweep: %v", err)
	} else {
		// PruneCompleted already deleted these from the DB, so their
		// worktree dirs would be misidentified as orphans. Mark them
		// known so they aren't double-counted.
		for _, t := range toClean {
			knownPaths[t.Worktree] = true
		}
		orphanCount = countOrphanedWorktrees(a.wtRoot, knownPaths)
	}

	totalClean := len(toClean) + orphanCount

	if totalClean == 0 {
		a.refreshTasksLocal()
		return
	}

	// Show the prune progress modal.
	a.pruneModal = NewPruneModal(totalClean)
	a.mode = modePruning
	a.pages.AddPage("pruning", a.pruneModal, true, true)
	a.pages.SwitchToPage("pruning")
	a.tapp.SetFocus(a.pruneModal)

	// Build project name → path map for orphan sweep.
	projects := make(map[string]string)
	for name, p := range cfg.Projects {
		projects[name] = p.Path
	}

	// Parallel cleanup in background goroutines.
	go func() {
		var wg sync.WaitGroup

		// Clean up each pruned task's worktree in parallel.
		for _, t := range toClean {
			wg.Add(1)
			go func(t *model.Task) {
				defer wg.Done()
				repoDir := agent.ResolveDir(t, cfg)
				uxlog.Log("[tui2] prune cleanup: task=%s name=%q worktree=%q branch=%q repoDir=%q project=%q",
					t.ID, t.Name, t.Worktree, t.Branch, repoDir, t.Project)
				removeWorktreeAndBranch(t.Worktree, t.Branch, repoDir)
				a.tapp.QueueUpdateDraw(func() {
					if a.pruneModal != nil {
						a.pruneModal.Increment()
					}
				})
			}(t)
		}

		// Sweep orphaned worktrees in parallel with task cleanup.
		if orphanCount > 0 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				swept := sweepOrphanedWorktrees(a.wtRoot, knownPaths, projects)
				uxlog.Log("[tui2] orphan sweep cleaned %d directories", swept)
				a.tapp.QueueUpdateDraw(func() {
					if a.pruneModal != nil {
						a.pruneModal.Increment()
					}
				})
			}()
		}

		wg.Wait()

		// Fetch session state off UI thread, then close modal + refresh together.
		runningIDs, idleIDs := a.runner.RunningAndIdle()
		a.tapp.QueueUpdateDraw(func() {
			a.closePruneModal()
			a.mu.Lock()
			a.refreshTasksWithIDs(runningIDs, idleIDs)
			a.mu.Unlock()
		})
	}()
}

// closePruneModal dismisses the prune modal and returns to the task list.
func (a *App) closePruneModal() {
	a.mode = modeTaskList
	a.pruneModal = nil
	a.pages.RemovePage("pruning")
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
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
	a.onTaskSelect(next)
}

// exitAgentView returns to the task list.
func (a *App) exitAgentView() {
	uxlog.Log("[tui2] exiting agent view")
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
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
	a.statusbar.ClearError()
}


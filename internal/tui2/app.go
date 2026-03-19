package tui2

import (
	"os"
	"os/exec"
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

// viewMode identifies the active view.
type viewMode int

const (
	modeTaskList viewMode = iota
	modeAgent
	modeNewTask
	modeConfirmDelete
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
	gitPanel     *GitPanel // git status for agent view (left panel)
	filePanel    *FilePanel

	// Reviews and settings tabs
	reviews      *ReviewsView
	settings     *SettingsView
	settingsPage *SettingsPage

	// New task form (created on demand)
	newTaskForm *NewTaskForm

	// Confirm delete modal (created on demand)
	confirmDeleteModal *ConfirmDeleteModal

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
	worktreeDir     string // resolved worktree dir for current agent view task
	lastGitRefresh  time.Time

	// Daemon health
	daemonFailures   int
	daemonRestarting bool
	daemonClient     *dclient.Client
	restartedClient  *dclient.Client // set after daemon restart

	// Tick control
	tickDone chan struct{}
}

// New creates the tui2 application shell.
func New(database *db.DB, runner agent.SessionProvider, daemonConnected bool) *App {
	// Use the terminal's default background instead of tview's hard-coded black.
	tview.Styles.PrimitiveBackgroundColor = tcell.ColorDefault

	app := &App{
		tapp:            tview.NewApplication(),
		db:              database,
		runner:          runner,
		daemonConnected: daemonConnected,
		agentState:      agentview.New(),
		tickDone:        make(chan struct{}),
	}

	if dc, ok := runner.(*dclient.Client); ok {
		app.daemonClient = dc
	}

	app.settings = NewSettingsView(database)
	app.settings.SetDaemonConnected(daemonConnected)
	app.settingsPage = NewSettingsPage(app.settings)
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

	a.taskGitPanel = NewGitPanel()
	a.taskGitPanel.BorderInside = true
	a.taskPreview = NewTaskPreviewPanel()
	a.taskDetail = NewTaskDetailPanel()

	a.gitPanel = NewGitPanel()
	a.filePanel = NewFilePanel()
	a.agentPane = NewTerminalPane()

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

	// Agent page — three-panel layout
	a.agentPage = tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(a.gitPanel, 0, 1, false).
		AddItem(a.agentPane, 0, 3, false).
		AddItem(a.filePanel, 0, 1, false)

	a.pages = tview.NewPages().
		AddPage("tasks", a.taskPage, true, true).
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
	runningIDs := a.runner.Running()

	a.mu.Lock()
	a.refreshTasksWithIDs(runningIDs)
	checkDaemon := a.daemonConnected && a.daemonClient != nil
	taskID := ""
	if a.mode == modeAgent {
		taskID = a.agentState.TaskID
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

	// Refresh task list side panels (runs on tick goroutine — blocking is OK here).
	if previewTaskID := a.taskPreview.TaskID(); previewTaskID != "" && a.mode == modeTaskList {
		a.refreshPreview(previewTaskID)
		// Also refresh git status for the selected task periodically.
		if sel := a.tasklist.SelectedTask(); sel != nil && sel.Worktree != "" {
			a.fetchTaskGitStatus(sel.ID, sel.Worktree)
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
			a.statusbar.SetError("Daemon restart failed: " + err.Error())
		})
		return
	}

	uxlog.Log("[tui2] daemon restarted, reconnected")
	a.tapp.QueueUpdateDraw(func() {
		a.mu.Lock()
		a.daemonRestarting = false
		a.daemonFailures = 0
		a.daemonClient = newClient
		a.runner = newClient
		a.restartedClient = newClient
		a.mu.Unlock()

		// Reset in-progress tasks to pending, preserving SessionID for resume.
		for _, t := range a.db.Tasks() {
			if t.Status == model.StatusInProgress {
				t.SetStatus(model.StatusPending)
				a.db.Update(t) //nolint:errcheck
				uxlog.Log("[tui2] reset task %s to pending (daemon restarted)", t.ID)
			}
		}
		a.refreshTasks()
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
func (a *App) NotifySessionExit(taskID string, err error, stopped bool) {
	uxlog.Log("[tui2] session exit (in-process): task=%s stopped=%v err=%v", taskID, stopped, err)
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
	a.tapp.QueueUpdateDraw(func() {
		a.handleSessionExitUI(taskID, info.Stopped)
	})
}

// handleSessionExitUI runs on the tview main goroutine (inside QueueUpdateDraw).
// Called by both NotifySessionExit (in-process) and HandleSessionExit (daemon).
func (a *App) handleSessionExitUI(taskID string, stopped bool) {
	// Update task status in DB.
	tasks := a.db.Tasks()
	for _, t := range tasks {
		if t.ID == taskID && t.Status == model.StatusInProgress {
			if stopped {
				t.SetStatus(model.StatusPending)
			} else {
				t.SetStatus(model.StatusComplete)
			}
			a.db.Update(t) //nolint:errcheck
			uxlog.Log("[tui2] task %s (%s) → %s", t.ID, t.Name, t.Status)
			break
		}
	}

	// If we're viewing this task's agent pane, clear the session.
	a.mu.Lock()
	viewing := a.mode == modeAgent && a.agentState.TaskID == taskID
	a.mu.Unlock()
	if viewing {
		a.agentPane.SetSession(nil)
	}

	// Refresh task list — fetch running IDs in a goroutine to avoid
	// blocking the tview main goroutine with an RPC call.
	go func() {
		runningIDs := a.runner.Running()
		a.tapp.QueueUpdateDraw(func() {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.refreshTasksWithIDs(runningIDs)
		})
	}()
}

func (a *App) refreshTasks() {
	// Fetch running IDs OUTSIDE the lock — Running() is an RPC call that
	// can block for up to 5s on timeout. Holding a.mu during that blocks
	// the entire UI.
	runningIDs := a.runner.Running()
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshTasksWithIDs(runningIDs)
}

// refreshTasksWithIDs updates the task list with pre-fetched running IDs.
// Used by onTick to avoid calling Running() (RPC) while holding a.mu.
func (a *App) refreshTasksWithIDs(runningIDs []string) {
	a.tasks = a.db.Tasks()
	a.runningIDs = runningIDs

	a.tasklist.SetTasks(a.tasks)
	a.tasklist.SetRunning(a.runningIDs)
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

	switch event.Key() {
	case tcell.KeyCtrlC:
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
	case tcell.KeyCtrlR:
		if a.mode == modeTaskList && a.header.ActiveTab() == TabTasks {
			a.pruneCompletedTasks()
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
				a.switchTab(TabReviews)
				return nil
			}
		case '3':
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
			a.agentPane.ScrollUp(1)
			return nil
		case tcell.KeyDown:
			a.agentPane.ScrollDown(1)
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
	// Kick off git status fetch for the selected task's worktree.
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
	_, _, w, h := a.taskPreview.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}

	sess := a.runner.Get(taskID)
	if sess != nil {
		raw := sess.RecentOutput()
		a.taskPreview.RefreshOutput(raw, w, h)
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

	a.mu.Lock()
	a.mode = modeAgent
	a.agentFocus = focusTerminal
	a.agentState.Reset(task.ID, task.Name)
	a.mu.Unlock()
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
		cfg.Projects, "", // TODO: default to currently selected project
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
			wtPath, finalName, err := agent.CreateWorktree(projCfg.Path, proj, task.Name, task.Branch)
			if err != nil {
				a.newTaskForm.SetError("Worktree error: " + err.Error())
				a.newTaskForm.done = false
				return
			}
			task.Worktree = wtPath
			task.Name = finalName
		}

		a.db.Add(task)
		uxlog.Log("[tui2] created task %s (%s)", task.ID, task.Name)

		a.closeNewTaskForm()
		a.refreshTasks()

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
		cols = uint16(max(pw, 20))
		rows = uint16(max(ph, 5))
	} else if tw, th, err := term.GetSize(int(os.Stdout.Fd())); err == nil && tw > 0 && th > 0 {
		// Agent page is a 1:3:1 flex — center panel gets 3/5 of width.
		// Border is drawn outside the box rect, so no deduction needed.
		centerW := tw * 3 / 5
		if centerW < 20 {
			centerW = 20
		}
		// Height minus header(1) and statusbar(1).
		centerH := th - 2
		if centerH < 5 {
			centerH = 5
		}
		cols = uint16(centerW)
		rows = uint16(centerH)
	}

	resume := task.SessionID != ""
	sess, err := a.runner.Start(task, cfg, rows, cols, resume)
	if err != nil {
		uxlog.Log("[tui2] failed to start session: %v", err)
		a.statusbar.SetError("Start failed: " + err.Error())
		return
	}

	task.Status = model.StatusInProgress
	task.AgentPID = sess.PID()
	a.db.Update(task)

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
			a.tapp.QueueUpdateDraw(func() {})
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
	a.refreshTasks()

	// Clean up worktree and branch in background — git operations can take seconds.
	cfg := a.db.Config()
	worktree, branch := t.Worktree, t.Branch
	go func() {
		if worktree != "" && cfg.UI.ShouldCleanupWorktrees() {
			repoDir := agent.ResolveDir(t, cfg)
			removeWorktreeAndBranch(worktree, branch, repoDir)
		} else if branch != "" {
			if repoDir := agent.ResolveDir(t, cfg); repoDir != "" {
				deleteBranch(repoDir, branch)
				deleteRemoteBranch(repoDir, branch)
			}
		}
	}()
}

// pruneCompletedTasks removes all completed tasks, cleaning up worktrees and branches.
// Matches the old Ctrl+R behavior from the Bubble Tea UI.
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

	// Clean up worktrees in a background goroutine so the UI stays responsive.
	cfg := a.db.Config()
	needsCleanup := cfg.UI.ShouldCleanupWorktrees()

	var toClean []*model.Task
	if needsCleanup {
		for _, t := range pruned {
			if t.Worktree != "" {
				toClean = append(toClean, t)
			}
		}
	}

	// Remove session logs for all pruned tasks.
	for _, t := range pruned {
		os.Remove(agent.SessionLogPath(t.ID)) //nolint:errcheck
	}

	if len(toClean) == 0 {
		a.refreshTasks()
		return
	}

	// Run worktree cleanup in a background goroutine.
	go func() {
		for _, t := range toClean {
			repoDir := agent.ResolveDir(t, cfg)
			removeWorktreeAndBranch(t.Worktree, t.Branch, repoDir)
		}
		a.tapp.QueueUpdateDraw(func() {
			a.refreshTasks()
		})
	}()

	a.refreshTasks()
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
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
	a.statusbar.ClearError()
}


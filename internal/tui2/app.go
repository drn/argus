package tui2

import (
	"os/exec"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/config"
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
	header    *Header
	statusbar *StatusBar
	tasklist  *TaskListView
	agentPane *TerminalPane
	gitPanel  *GitPanel
	filePanel *FilePanel

	// Reviews and settings tabs
	reviews  *ReviewsView
	settings *SettingsView

	// New task form (created on demand)
	newTaskForm *NewTaskForm

	// Layout containers
	root      *tview.Flex
	taskPage  *tview.Flex
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
	daemonFailures int
	daemonClient   *dclient.Client

	// Tick control
	tickDone chan struct{}
}

// New creates the tui2 application shell.
func New(database *db.DB, runner agent.SessionProvider, daemonConnected bool) *App {
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

	a.gitPanel = NewGitPanel()
	a.filePanel = NewFilePanel()
	a.agentPane = NewTerminalPane()
	a.reviews = NewReviewsView()
	a.reviews.SetOnFetch(func(fn func()) {
		go fn()
	})

	// Task list page
	taskListCenter := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(tview.NewBox(), 0, 1, false).
		AddItem(a.tasklist, 0, 3, true).
		AddItem(tview.NewBox(), 0, 1, false)
	a.taskPage = taskListCenter

	// Agent page — three-panel layout
	a.agentPage = tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(a.gitPanel, 0, 1, false).
		AddItem(a.agentPane, 0, 3, false).
		AddItem(a.filePanel, 0, 1, false)

	a.pages = tview.NewPages().
		AddPage("tasks", a.taskPage, true, true).
		AddPage("agent", a.agentPage, true, false).
		AddPage("reviews", a.reviews, true, false).
		AddPage("settings", a.settings, true, false)

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
	a.mu.Lock()
	a.refreshTasksLocked()
	checkDaemon := a.daemonConnected && a.daemonClient != nil
	taskID := ""
	if a.mode == modeAgent {
		taskID = a.agentState.TaskID
	}
	a.mu.Unlock()

	// Daemon health check
	if checkDaemon {
		if err := a.daemonClient.Ping(); err != nil {
			a.mu.Lock()
			a.daemonFailures++
			failures := a.daemonFailures
			a.mu.Unlock()
			if failures >= 3 {
				uxlog.Log("[tui2] daemon unreachable after %d pings", failures)
			}
		} else {
			a.mu.Lock()
			a.daemonFailures = 0
			a.mu.Unlock()
		}
	}

	// Update agent pane session
	if taskID != "" {
		sess := a.runner.Get(taskID)
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

func (a *App) refreshTasks() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshTasksLocked()
}

func (a *App) refreshTasksLocked() {
	a.tasks = a.db.Tasks()
	a.runningIDs = a.runner.Running()

	a.tasklist.SetTasks(a.tasks)
	a.tasklist.SetRunning(a.runningIDs)
	a.statusbar.SetTasks(a.tasks)
	a.statusbar.SetRunning(a.runningIDs)
}

// handleGlobalKey processes key events at the application level.
func (a *App) handleGlobalKey(event *tcell.EventKey) *tcell.EventKey {
	// New task form mode — delegate everything to the form
	if a.mode == modeNewTask && a.newTaskForm != nil {
		a.handleNewTaskKey(event)
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
		a.agentPane.DiffScrollUp(1)
		return nil
	case tcell.KeyDown:
		a.agentPane.DiffScrollDown(1)
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

// onTaskSelect handles Enter on a task — enters the agent view.
func (a *App) onTaskSelect(task *model.Task) {
	uxlog.Log("[tui2] entering agent view for task %s (%s)", task.ID, task.Name)

	a.mode = modeAgent
	a.agentFocus = focusTerminal
	a.agentState.Reset(task.ID, task.Name)
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

		// Start the agent session
		a.startAndAttach(task)
	}
}

// startAndAttach starts a session for the task and enters agent view.
func (a *App) startAndAttach(task *model.Task) {
	cfg := a.db.Config()

	// Compute PTY size from agent pane dimensions
	_, _, w, h := a.agentPane.GetInnerRect()
	rows, cols := uint16(max(h-2, 5)), uint16(max(w-4, 20))

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

	a.onTaskSelect(task)
}

func (a *App) closeNewTaskForm() {
	a.mode = modeTaskList
	a.newTaskForm = nil
	a.pages.RemovePage("newtask")
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
}

// exitAgentView returns to the task list.
func (a *App) exitAgentView() {
	uxlog.Log("[tui2] exiting agent view")
	a.mode = modeTaskList
	a.agentFocus = focusTerminal
	a.agentPane.SetSession(nil)
	a.agentPane.SetFocused(false)
	a.agentPane.ExitDiffMode()
	a.agentPane.ResetVT()
	a.worktreeDir = ""
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
	a.statusbar.ClearError()
}

// RestartedClient returns nil — daemon restart not yet implemented in tui2.
func (a *App) RestartedClient() *dclient.Client {
	return nil
}

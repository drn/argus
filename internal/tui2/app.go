package tui2

import (
	"os/exec"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	dclient "github.com/drn/argus/internal/daemon/client"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/uxlog"
)

// viewMode identifies the active view.
type viewMode int

const (
	modeTaskList viewMode = iota
	modeAgent
)

// focusPanel identifies which panel has focus in the agent view.
type focusPanel int

const (
	focusTerminal focusPanel = iota
	focusFiles
)

// App is the top-level tview application shell.
// It mirrors the Bubble Tea Model's responsibilities: routing events,
// managing sub-views, running periodic ticks, and coordinating with the
// daemon/runner.
type App struct {
	tapp   *tview.Application
	db     *db.DB
	runner agent.SessionProvider
	mu     sync.Mutex // guards state accessed from tick goroutine

	// Sub-views
	header    *Header
	statusbar *StatusBar
	tasklist  *TaskListView
	agentPane *AgentPane
	gitPanel  *SidePanel
	filePanel *SidePanel

	// Layout containers
	root      *tview.Flex  // vertical: header + content + statusbar
	taskPage  *tview.Flex  // task list content
	agentPage *tview.Flex  // agent view: git | terminal | files
	pages     *tview.Pages

	// State
	mode            viewMode
	focus           focusPanel
	agentState      agentview.State
	daemonConnected bool
	tasks           []*model.Task
	runningIDs      []string
	currentTaskPR   string // PR URL for the current agent view task

	// Daemon health
	daemonFailures int
	daemonClient   *dclient.Client // non-nil when daemon-connected

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

	a.gitPanel = NewSidePanel("Git Status")
	a.filePanel = NewSidePanel("Files")
	a.agentPane = NewAgentPane()

	// Task list page — centered task list with side panels
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
		AddPage("agent", a.agentPage, true, false)

	// Root layout: header (1 row) + content + status bar (1 row)
	a.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 1, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.statusbar, 1, 0, false)

	a.tapp.SetInputCapture(a.handleGlobalKey)
	a.tapp.SetRoot(a.root, true)

	// Mouse support for scroll wheel
	a.tapp.EnableMouse(true)
}

// Run starts the application event loop. Blocks until exit.
func (a *App) Run() error {
	go a.tickLoop()
	defer close(a.tickDone)

	uxlog.Log("[tui2] starting tcell/tview application")
	return a.tapp.Run()
}

// tickLoop runs periodic updates (task refresh, daemon health check).
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

	// Update agent pane session via QueueUpdateDraw so writes happen on
	// the tview event loop thread, avoiding data races with Draw().
	if taskID != "" {
		sess := a.runner.Get(taskID)
		a.tapp.QueueUpdateDraw(func() {
			if sess != nil {
				a.agentPane.SetSession(sess)
			}
		})
	} else {
		a.tapp.QueueUpdateDraw(func() {})
	}
}

// refreshTasks loads tasks from DB and updates sub-views.
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
	switch event.Key() {
	case tcell.KeyCtrlC:
		a.tapp.Stop()
		return nil
	case tcell.KeyCtrlQ:
		if a.mode == modeAgent {
			if a.agentPane.InDiffMode() {
				a.agentPane.ExitDiffMode()
				a.focus = focusTerminal
				return nil
			}
			if a.focus == focusFiles {
				a.focus = focusTerminal
				a.agentPane.SetFocused(true)
				a.filePanel.SetFocused(false)
				return nil
			}
			a.exitAgentView()
			return nil
		}
	case tcell.KeyRune:
		if a.mode == modeAgent {
			// Agent view rune keys are handled by handleAgentKey
			break
		}
		switch event.Rune() {
		case 'q':
			if a.mode == modeTaskList {
				a.tapp.Stop()
				return nil
			}
		case '1':
			a.switchTab(TabTasks)
			return nil
		case '2':
			a.switchTab(TabReviews)
			return nil
		case '3':
			a.switchTab(TabSettings)
			return nil
		}
	}

	// Mode-specific handling
	if a.mode == modeAgent {
		return a.handleAgentKey(event)
	}

	return event
}

// handleAgentKey handles keys when the agent view is active.
func (a *App) handleAgentKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEscape:
		if a.agentPane.InDiffMode() {
			a.agentPane.ExitDiffMode()
			a.focus = focusTerminal
			return nil
		}
		if a.focus == focusFiles {
			a.focus = focusTerminal
			a.agentPane.SetFocused(true)
			a.filePanel.SetFocused(false)
			return nil
		}
		a.exitAgentView()
		return nil

	case tcell.KeyLeft:
		// Ctrl+Left or Alt+Left: focus left panel
		if event.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			if a.focus > focusTerminal {
				a.focus--
				a.updateFocusIndicators()
			}
			return nil
		}

	case tcell.KeyRight:
		// Ctrl+Right or Alt+Right: focus right panel
		if event.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0 {
			if a.focus < focusFiles {
				a.focus++
				a.updateFocusIndicators()
			}
			return nil
		}

	case tcell.KeyPgUp:
		if event.Modifiers()&tcell.ModShift != 0 {
			_, _, _, h := a.agentPane.GetInnerRect()
			a.agentPane.ScrollUp(h)
			return nil
		}
	case tcell.KeyPgDn:
		if event.Modifiers()&tcell.ModShift != 0 {
			_, _, _, h := a.agentPane.GetInnerRect()
			a.agentPane.ScrollDown(h)
			return nil
		}

	case tcell.KeyUp:
		if event.Modifiers()&tcell.ModShift != 0 {
			a.agentPane.ScrollUp(1)
			return nil
		}
		if a.focus == focusFiles {
			// File cursor up
			return nil
		}
	case tcell.KeyDown:
		if event.Modifiers()&tcell.ModShift != 0 {
			a.agentPane.ScrollDown(1)
			return nil
		}
		if a.focus == focusFiles {
			// File cursor down
			return nil
		}

	case tcell.KeyCtrlP:
		// Open PR URL
		if a.currentTaskPR != "" {
			exec.Command("open", a.currentTaskPR).Start() //nolint:errcheck
			return nil
		}

	case tcell.KeyRune:
		switch event.Rune() {
		case 'o':
			// Open PR when session is not active
			if a.agentPane.session == nil || !a.agentPane.session.Alive() {
				if a.currentTaskPR != "" {
					exec.Command("open", a.currentTaskPR).Start() //nolint:errcheck
					return nil
				}
			}
		case 's':
			if a.agentPane.InDiffMode() {
				a.agentPane.ToggleDiffSplit()
				return nil
			}
		case 'q':
			if a.agentPane.InDiffMode() {
				a.agentPane.ExitDiffMode()
				a.focus = focusTerminal
				return nil
			}
		}
	}

	// In diff mode, don't forward to PTY
	if a.agentPane.InDiffMode() {
		return nil
	}

	// Forward to PTY if session is active and terminal is focused
	if a.focus == focusTerminal && a.agentPane.session != nil && a.agentPane.session.Alive() {
		// Reset scroll on any input (follow tail)
		a.agentPane.ScrollToBottom()
		b := tcellKeyToBytes(event)
		if len(b) > 0 {
			if _, err := a.agentPane.session.WriteInput(b); err != nil {
				uxlog.Log("[tui2] write to PTY failed: %v", err)
			}
			return nil
		}
	}

	return event
}

// updateFocusIndicators updates the focused state of panels.
func (a *App) updateFocusIndicators() {
	a.agentPane.SetFocused(a.focus == focusTerminal)
	a.filePanel.SetFocused(a.focus == focusFiles)
}

// tcellKeyToBytes converts a tcell key event to raw terminal bytes for PTY input.
func tcellKeyToBytes(ev *tcell.EventKey) []byte {
	// Rune keys
	if ev.Key() == tcell.KeyRune {
		r := ev.Rune()
		if ev.Modifiers()&tcell.ModAlt != 0 {
			return append([]byte{0x1b}, []byte(string(r))...)
		}
		return []byte(string(r))
	}

	alt := ev.Modifiers()&tcell.ModAlt != 0

	// Arrow keys with Alt modifier
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
		a.pages.SwitchToPage("tasks")
		a.tapp.SetFocus(a.tasklist)
	case TabReviews:
		a.statusbar.SetError("Reviews tab not yet ported to tcell runtime")
		a.pages.SwitchToPage("tasks")
	case TabSettings:
		a.statusbar.SetError("Settings tab not yet ported to tcell runtime")
		a.pages.SwitchToPage("tasks")
	}
}

// onTaskSelect handles Enter on a task — enters the agent view.
func (a *App) onTaskSelect(task *model.Task) {
	uxlog.Log("[tui2] entering agent view for task %s (%s)", task.ID, task.Name)

	a.mode = modeAgent
	a.focus = focusTerminal
	a.agentState.Reset(task.ID, task.Name)
	a.agentPane.SetTaskID(task.ID)
	a.currentTaskPR = task.PRURL

	// Look up session
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
}

// onNewTask handles the 'n' key — placeholder for new task form.
func (a *App) onNewTask() {
	a.statusbar.SetError("New task form not yet ported to tcell runtime")
}

// exitAgentView returns to the task list.
func (a *App) exitAgentView() {
	uxlog.Log("[tui2] exiting agent view")
	a.mode = modeTaskList
	a.focus = focusTerminal
	a.agentPane.SetSession(nil)
	a.agentPane.SetFocused(false)
	a.agentPane.ExitDiffMode()
	a.currentTaskPR = ""
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
	a.statusbar.ClearError()
}

// RestartedClient returns nil — daemon restart not yet implemented in tui2.
func (a *App) RestartedClient() *dclient.Client {
	return nil
}

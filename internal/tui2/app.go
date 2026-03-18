package tui2

import (
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

// App is the top-level tview application shell.
// It mirrors the Bubble Tea Model's responsibilities: routing events,
// managing sub-views, running periodic ticks, and coordinating with the
// daemon/runner.
type App struct {
	tapp    *tview.Application
	db      *db.DB
	runner  agent.SessionProvider
	mu      sync.Mutex // guards state accessed from tick goroutine

	// Sub-views
	header    *Header
	statusbar *StatusBar
	tasklist  *TaskListView
	agentPane *AgentPane
	gitPanel  *SidePanel
	filePanel *SidePanel

	// Layout containers
	root      *tview.Flex // vertical: header + content + statusbar
	taskPage  *tview.Flex // task list content
	agentPage *tview.Flex // agent view: git | terminal | files
	pages     *tview.Pages

	// State
	mode            viewMode
	agentState      agentview.State
	daemonConnected bool
	tasks           []*model.Task
	runningIDs      []string

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
	// Header
	a.header = NewHeader()

	// Status bar
	a.statusbar = NewStatusBar()

	// Task list
	a.tasklist = NewTaskListView()
	a.tasklist.OnSelect = a.onTaskSelect
	a.tasklist.OnNew = a.onNewTask

	// Agent view panels
	a.gitPanel = NewSidePanel("Git Status")
	a.filePanel = NewSidePanel("Files")
	a.agentPane = NewAgentPane()

	// Task list page — centered task list with side panels
	taskListCenter := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(tview.NewBox(), 0, 1, false).         // left spacer
		AddItem(a.tasklist, 0, 3, true).               // task list
		AddItem(tview.NewBox(), 0, 1, false)            // right spacer
	a.taskPage = taskListCenter

	// Agent page — three-panel layout
	a.agentPage = tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(a.gitPanel, 0, 1, false).
		AddItem(a.agentPane, 0, 3, false).
		AddItem(a.filePanel, 0, 1, false)

	// Pages overlay for switching between task list and agent view
	a.pages = tview.NewPages().
		AddPage("tasks", a.taskPage, true, true).
		AddPage("agent", a.agentPage, true, false)

	// Root layout: header (1 row) + content + status bar (1 row)
	a.root = tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(a.header, 1, 0, false).
		AddItem(a.pages, 0, 1, true).
		AddItem(a.statusbar, 1, 0, false)

	// Global key handler
	a.tapp.SetInputCapture(a.handleGlobalKey)
	a.tapp.SetRoot(a.root, true)
}

// Run starts the application event loop. Blocks until exit.
func (a *App) Run() error {
	// Start periodic tick
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
	// Snapshot state under lock, then release before calling runner methods
	// to avoid lock inversion (runner has its own internal lock).
	a.mu.Lock()
	a.refreshTasksLocked()
	checkDaemon := a.daemonConnected && a.daemonClient != nil
	taskID := ""
	if a.mode == modeAgent {
		taskID = a.agentState.TaskID
	}
	a.mu.Unlock()

	// Daemon health check (outside lock — Ping acquires its own lock)
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

	// Update agent pane session via QueueUpdateDraw so the write to
	// agentPane.session happens on the tview event loop thread — avoiding
	// a data race with Draw() which reads it on that same thread.
	if taskID != "" {
		sess := a.runner.Get(taskID)
		a.tapp.QueueUpdateDraw(func() {
			if sess != nil {
				a.agentPane.SetSession(sess)
			}
		})
	} else {
		// Still trigger a redraw for task list updates
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

	// Get running sessions
	a.runningIDs = a.runner.Running()

	// Update sub-views
	a.tasklist.SetTasks(a.tasks)
	a.tasklist.SetRunning(a.runningIDs)
	a.statusbar.SetTasks(a.tasks)
	a.statusbar.SetRunning(a.runningIDs)
}

// handleGlobalKey processes key events at the application level.
func (a *App) handleGlobalKey(event *tcell.EventKey) *tcell.EventKey {
	// Global keys available in all modes
	switch event.Key() {
	case tcell.KeyCtrlC:
		a.tapp.Stop()
		return nil
	case tcell.KeyCtrlQ:
		if a.mode == modeAgent {
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
	switch a.mode {
	case modeAgent:
		return a.handleAgentKey(event)
	}

	return event
}

// handleAgentKey handles keys when the agent view is active.
func (a *App) handleAgentKey(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEscape:
		a.exitAgentView()
		return nil
	}

	// Forward to PTY if session is active
	if a.agentPane.session != nil && a.agentPane.session.Alive() {
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

	// Arrow keys with Alt modifier use CSI sequences with modifier parameter.
	// Alt+arrow sends \x1b[1;3X (modifier 3 = Alt) for word navigation.
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
			return []byte{0x1b, 0x7f} // Alt+Delete = word delete
		}
	}

	// Special keys (no Alt)
	switch ev.Key() {
	case tcell.KeyEnter:
		return []byte{'\r'}
	case tcell.KeyTab:
		return []byte{'\t'}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if alt {
			return []byte{0x1b, 0x7f} // Alt+Backspace = word delete
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
		// Phase 2 placeholder: show tasks page with a message
		// Reviews will be ported in Phase 5
		a.statusbar.SetError("Reviews tab not yet ported to tcell runtime")
		a.pages.SwitchToPage("tasks")
	case TabSettings:
		// Phase 2 placeholder
		a.statusbar.SetError("Settings tab not yet ported to tcell runtime")
		a.pages.SwitchToPage("tasks")
	}
}

// onTaskSelect handles Enter on a task — enters the agent view.
func (a *App) onTaskSelect(task *model.Task) {
	uxlog.Log("[tui2] entering agent view for task %s (%s)", task.ID, task.Name)

	a.mode = modeAgent
	a.agentState.Reset(task.ID, task.Name)
	a.agentPane.SetTaskID(task.ID)

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
	// Phase 2 placeholder: new task form will be ported in Phase 5
	a.statusbar.SetError("New task form not yet ported to tcell runtime")
}

// exitAgentView returns to the task list.
func (a *App) exitAgentView() {
	uxlog.Log("[tui2] exiting agent view")
	a.mode = modeTaskList
	a.agentPane.SetSession(nil)
	a.agentPane.SetFocused(false)
	a.pages.SwitchToPage("tasks")
	a.tapp.SetFocus(a.tasklist)
	a.statusbar.ClearError()
}

// RestartedClient returns nil — daemon restart not yet implemented in tui2.
func (a *App) RestartedClient() *dclient.Client {
	return nil
}

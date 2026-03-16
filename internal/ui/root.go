package ui

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/daemon"
	dclient "github.com/drn/argus/internal/daemon/client"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

type view int

const (
	viewTaskList view = iota
	viewNewTask
	viewHelp
	viewPrompt
	viewConfirmDelete
	viewNewProject
	viewConfirmDeleteProject
	viewConfirmDestroy
	viewPruning
	viewAgent
	viewSandboxInstall
	viewDaemonRestart
)

type tab int

const (
	tabTasks tab = iota
	tabSettings
)

// minAgentRunTime is the minimum time an agent must run before a clean exit
// is treated as a successful completion. Exits faster than this are treated
// as startup or configuration errors (the session ID is cleared so the user
// can retry).
const minAgentRunTime = 3 * time.Second

// TickMsg is sent periodically to update elapsed times.
type TickMsg struct{}

// AgentFinishedMsg is sent when an agent process exits.
type AgentFinishedMsg struct {
	TaskID     string
	Err        error
	Stopped    bool   // true if the process was explicitly stopped via Runner.Stop
	LastOutput []byte // final ring buffer contents for displaying errors
}

// AgentDetachedMsg is sent when the user detaches from a running agent.
type AgentDetachedMsg struct {
	TaskID string
}

// ResolveTaskDirMsg carries the result of async worktree directory resolution.
type ResolveTaskDirMsg struct {
	TaskID string
	Dir    string
}

// SessionResumedMsg is sent when a background session resume completes.
type SessionResumedMsg struct {
	TaskID string
	PID    int
	Err    error
}

// PruneProgressMsg signals that one worktree was cleaned; remaining work follows.
type PruneProgressMsg struct {
	Current   int           // 1-based index of the one just completed
	Total     int           // total worktrees to clean
	Remaining []*model.Task // tasks still to clean
	Cfg       config.Config // config snapshot for resolving dirs
}

// PruneDoneMsg signals that all prune cleanup is finished.
type PruneDoneMsg struct {
	Count int
}

// SandboxInstallMsg carries the result of an async srt install.
type SandboxInstallMsg struct {
	Success bool
	Output  string
}

// DaemonRestartedMsg carries the result of a daemon restart attempt.
type DaemonRestartedMsg struct {
	Client *dclient.Client
	Err    error
}

// Model is the top-level Bubble Tea model.
type Model struct {
	db          *db.DB
	runner      agent.SessionProvider
	keys        KeyMap
	theme       Theme
	tasklist    TaskList
	settings    SettingsView
	statusbar   StatusBar
	helpview    HelpView
	newtask     NewTaskForm
	newproject  NewProjectForm
	preview     Preview
	gitstatus   *GitStatus
	detail      TaskDetail
	agentview   *AgentView
	taskLayout  PanelLayout
	current      view
	activeTab    tab
	width        int
	height       int
	quitting           bool
	agentTickActive    bool              // true while the 100ms AgentViewTickMsg chain is running
	daemonConnected    bool              // true when connected to daemon (sessions persist)
	daemonRestarting   bool              // true while daemon restart is in progress
	resolvedDirs       map[string]string // taskID → resolved worktree dir (cache)

	// Prune progress state (shown in viewPruning modal)
	pruneTotal   int // total worktrees being cleaned up
	pruneCurrent int // number completed so far (0 = starting)

	// Sandbox install modal state
	sandboxInstallPending *model.Task // task to start after install completes
	sandboxInstalling     bool        // true while npm install is running
	sandboxInstallResult  string      // install output or error

	// program is set by SetProgram so daemon restart can register
	// OnSessionExit on the new client. Shared via pointer indirection
	// so Bubble Tea's value-receiver copies all see the same reference.
	program **tea.Program

	// restartedClient holds the new daemon client after a restart so
	// runTUI can close it on exit. Shared via pointer indirection.
	restartedClient **dclient.Client
}

// NewModel creates the top-level model. Set daemonConnected to true when the
// TUI is backed by a daemon process (sessions persist across restarts).
func NewModel(database *db.DB, runner agent.SessionProvider, daemonConnected bool) Model {
	theme := DefaultTheme()
	keys := DefaultKeyMap()

	tl := NewTaskList(theme)
	sv := NewSettingsView(theme)
	sb := NewStatusBar(theme)
	hv := NewHelpView(keys, theme)

	pv := NewPreview(theme, runner)
	gs := NewGitStatus(theme)
	dt := NewTaskDetail(theme)
	avv := NewAgentView(theme, runner)
	av := &avv

	m := Model{
		db:              database,
		runner:          runner,
		keys:            keys,
		theme:           theme,
		daemonConnected: daemonConnected,
		resolvedDirs:    make(map[string]string),
		program:         new(*tea.Program),
		restartedClient: new(*dclient.Client),
		tasklist:    tl,
		settings:    sv,
		statusbar:   sb,
		helpview:    hv,
		preview:     pv,
		gitstatus:   &gs,
		detail:      dt,
		agentview:   av,
		taskLayout: NewPanelLayout([]PanelConfig{
			{Pct: 20, Min: 20},
			{Pct: 60, Min: 60},
			{Pct: 20, Min: 20},
		}),
		current:     viewTaskList,
		activeTab:   tabTasks,
	}
	m.refreshTasks()
	m.refreshSettings()
	return m
}

// RestartedClient returns the daemon client created during a restart, or nil.
// Called by runTUI after the program exits to ensure proper cleanup.
func (m Model) RestartedClient() *dclient.Client {
	if m.restartedClient == nil {
		return nil
	}
	return *m.restartedClient
}

// SetProgram stores the tea.Program reference so daemon restart can register
// the OnSessionExit callback on the new client. The `program` field is a
// double-pointer allocated in NewModel, so all Bubble Tea value copies
// share the same underlying *tea.Program slot.
func (m *Model) SetProgram(p *tea.Program) {
	*m.program = p
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.Tick(time.Second, func(_ time.Time) tea.Msg {
			return TickMsg{}
		}),
	}

	// Check if the runner already has sessions (e.g., daemon client discovered them).
	// If so, just sync state — don't try to resume.
	daemonRunning := len(m.runner.Running()) > 0

	if !daemonRunning {
		// Resume sessions for in-progress tasks that have a saved session ID.
		// Each resume runs in a background goroutine so the UI stays responsive.
		for _, t := range m.db.Tasks() {
			if t.Status == model.StatusInProgress && t.SessionID != "" {
				task := t // capture loop variable

				// Kill any orphaned process from a previous Argus session.
				// When Argus exits, PTY master fds close and children get SIGHUP,
				// but we clean up any that might linger before resuming.
				if task.AgentPID > 0 {
					killStaleProcess(task.AgentPID)
					task.AgentPID = 0
				}

				// Backfill worktree path for tasks that don't have one stored.
				if task.Worktree == "" && task.Name != "" && task.Project != "" {
					if wt := discoverWorktree(task.Project, task.Name); wt != "" {
						task.Worktree = wt
						m.resolvedDirs[task.ID] = wt
					}
				}

				// Reset StartedAt before starting so the quick-exit check in
				// handleAgentFinished uses the resume time, not the original start.
				task.StartedAt = time.Now()
				_ = m.db.Update(task)

				cmds = append(cmds, func() tea.Msg {
					sess, err := m.runner.Start(task, m.db.Config(), 24, 80, true)
					if err != nil {
						return SessionResumedMsg{TaskID: task.ID, Err: err}
					}
					return SessionResumedMsg{TaskID: task.ID, PID: sess.PID()}
				})
			}
		}
	}

	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Reserve space: tab header(1) + statusbar(1)
		contentHeight := msg.Height - 2

		// Tasks tab: three-panel layout
		m.taskLayout.SetSize(msg.Width, contentHeight)
		widths := m.taskLayout.SplitWidths()
		m.tasklist.SetSize(widths[0]-2, contentHeight-2)
		gitH, previewH := m.splitCenterHeights(contentHeight)
		m.gitstatus.SetSize(widths[1], gitH)
		m.preview.SetSize(widths[1], previewH)
		m.detail.SetSize(widths[2], contentHeight)

		// Settings tab uses two-panel split
		settingsLeft, _ := m.splitWidths()
		m.settings.SetSize(settingsLeft, contentHeight)

		m.statusbar.SetWidth(msg.Width)
		m.newtask.SetSize(msg.Width, msg.Height)
		m.newproject.SetSize(msg.Width, msg.Height)
		m.agentview.SetSize(msg.Width, msg.Height)
		return m, nil

	case TickMsg:
		var cmds []tea.Cmd
		cmds = append(cmds, tea.Tick(time.Second, func(_ time.Time) tea.Msg {
			return TickMsg{}
		}))
		// Skip task list refresh while in agent view or daemon restart —
		// in agent view it's not visible, during restart the runner is
		// being swapped and RPCs would timeout against the dead socket.
		if m.current != viewAgent && !m.daemonRestarting {
			m.refreshTasks()
			m.tasklist.Tick()
			if m.activeTab == tabSettings {
				m.settings.SetTasks(m.db.Tasks())
			}
		}
		if !m.daemonRestarting {
			if cmd := m.scheduleGitRefresh(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		return m, tea.Batch(cmds...)

	case ResolveTaskDirMsg:
		if msg.Dir != "" {
			m.resolvedDirs[msg.TaskID] = msg.Dir
			// Persist discovered worktree only if it's actually inside the
			// worktree directory — never persist the project dir as a worktree.
			if t, err := m.db.Get(msg.TaskID); err == nil && t.Worktree == "" && isWorktreeSubdir(msg.Dir) {
				t.Worktree = msg.Dir
				_ = m.db.Update(t)
			}
			// Set worktree dir on agent view immediately.
			if m.current == viewAgent && m.agentview.taskID == msg.TaskID {
				m.agentview.SetWorktreeDir(msg.Dir)
			}
		}
		// Now that we have the dir, kick off git status fetch
		if t := m.selectedTaskForGit(); t != nil && t.ID == msg.TaskID && msg.Dir != "" {
			m.gitstatus.SetTask(t.ID)
			taskID := t.ID
			dir := msg.Dir
			return m, func() tea.Msg {
				return FetchGitStatus(taskID, dir)
			}
		}
		return m, nil

	case AgentViewTickMsg:
		// Fast tick for agent view — just triggers a re-render.
		// The chain stops itself when we leave agent view and clears
		// the flag so startOrAttach can restart it next time.
		if m.current == viewAgent {
			return m, tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
				return AgentViewTickMsg{}
			})
		}
		m.agentTickActive = false
		return m, nil

	case GitStatusRefreshMsg:
		m.gitstatus.Update(msg)
		m.agentview.UpdateGitStatus(msg)
		return m, nil

	case FileDiffMsg:
		m.agentview.UpdateFileDiff(msg)
		return m, nil

	case tea.MouseMsg:
		if m.current == viewAgent {
			m.agentview.HandleMouse(msg)
		}
		return m, nil

	case SessionResumedMsg:
		return m.handleSessionResumed(msg)

	case AgentFinishedMsg:
		return m.handleAgentFinished(msg)

	case PruneProgressMsg:
		m.pruneCurrent = msg.Current
		if len(msg.Remaining) == 0 {
			// All done.
			m.current = viewTaskList
			m.refreshTasks()
			return m, nil
		}
		// Clean next worktree.
		return m, pruneNextCmd(msg.Current, msg.Total, msg.Remaining, msg.Cfg)

	case PruneDoneMsg:
		m.current = viewTaskList
		m.refreshTasks()
		return m, nil

	case SandboxInstallMsg:
		m.sandboxInstalling = false
		if msg.Success {
			m.sandboxInstallResult = "Installed successfully"
			// Reset the cache so IsSandboxAvailable() rechecks
			agent.ResetSandboxCache()
			m.refreshSettings()
		} else {
			m.sandboxInstallResult = "Install failed: " + msg.Output
		}
		return m, nil

	case DaemonRestartedMsg:
		m.daemonRestarting = false
		if msg.Err != nil {
			m.daemonConnected = false
			m.statusbar.SetError("daemon restart failed: " + msg.Err.Error())
			m.current = viewTaskList
			m.refreshSettings()
			return m, nil
		}
		// Swap runner to new daemon client. Don't close the old client
		// here — runTUI's defer client.Close() handles that.
		if msg.Client != nil && *m.program != nil {
			p := *m.program
			msg.Client.OnSessionExit(func(taskID string, info daemon.ExitInfo) {
				var exitErr error
				if info.Err != "" {
					exitErr = errors.New(info.Err)
				}
				p.Send(AgentFinishedMsg{
					TaskID:     taskID,
					Err:        exitErr,
					Stopped:    info.Stopped,
					LastOutput: info.LastOutput,
				})
			})
		}
		m.runner = msg.Client
		m.daemonConnected = true
		m.preview.runner = msg.Client
		m.agentview.runner = msg.Client
		*m.restartedClient = msg.Client

		// Reset in-progress tasks — daemon restart killed all sessions.
		for _, t := range m.db.Tasks() {
			if t.Status == model.StatusInProgress {
				t.SetStatus(model.StatusPending)
				t.SessionID = ""
				t.AgentPID = 0
				t.StartedAt = time.Time{}
				_ = m.db.Update(t)
			}
		}
		m.current = viewTaskList
		m.refreshTasks()
		m.refreshSettings()
		return m, nil

	case AgentDetachedMsg:
		// User detached — agent still running in background
		m.refreshTasks()
		return m, nil

	case tea.KeyMsg:
		m.statusbar.ClearError()
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.current {
	case viewNewTask:
		return m.handleNewTaskKey(msg)
	case viewNewProject:
		return m.handleNewProjectKey(msg)
	case viewHelp:
		m.current = viewTaskList
		return m, nil
	case viewPrompt:
		m.current = viewTaskList
		return m, nil
	case viewConfirmDelete:
		return m.handleConfirmDeleteKey(msg)
	case viewConfirmDestroy:
		return m.handleConfirmDestroyKey(msg)
	case viewConfirmDeleteProject:
		return m.handleConfirmDeleteProjectKey(msg)
	case viewPruning, viewDaemonRestart:
		// Absorb all keys while pruning or restarting — no interaction allowed.
		return m, nil
	case viewSandboxInstall:
		return m.handleSandboxInstallKey(msg)
	case viewAgent:
		return m.handleAgentViewKey(msg)
	default:
		// Tab switching with 1/2 keys or left/right arrows
		switch msg.String() {
		case "1":
			m.activeTab = tabTasks
			return m, nil
		case "2":
			m.activeTab = tabSettings
			return m, nil
		}
		switch {
		case key.Matches(msg, m.keys.TabLeft):
			if m.activeTab > tabTasks {
				m.activeTab--
			}
			return m, nil
		case key.Matches(msg, m.keys.TabRight):
			if m.activeTab < tabSettings {
				m.activeTab++
			}
			return m, nil
		}
		if m.activeTab == tabSettings {
			return m.handleSettingsKey(msg)
		}
		return m.handleTaskListKey(msg)
	}
}

func (m Model) handleAgentViewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Cmd+Up/Down (sent as Alt+Up/Down by terminals) switches to adjacent task.
	if msg.Alt {
		switch msg.Type {
		case tea.KeyUp:
			return m.switchAgentTask(-1)
		case tea.KeyDown:
			return m.switchAgentTask(+1)
		}
	}

	detach, cmd := m.agentview.HandleKey(msg)
	if detach {
		m.current = viewTaskList
		m.refreshTasks()
		return m, nil
	}
	return m, cmd
}

// switchAgentTask navigates to the adjacent task's agent view (dir: -1=prev, +1=next).
func (m Model) switchAgentTask(dir int) (tea.Model, tea.Cmd) {
	next := m.tasklist.AdjacentTask(m.agentview.taskID, dir)
	if next == nil {
		return m, nil
	}
	return m.startOrAttach(next)
}

func (m Model) handleTaskListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		return m, tea.Quit

	case key.Matches(msg, m.keys.Up):
		m.tasklist.CursorUp()
		return m, m.scheduleGitRefresh()

	case key.Matches(msg, m.keys.Down):
		m.tasklist.CursorDown()
		return m, m.scheduleGitRefresh()

	case key.Matches(msg, m.keys.New):
		defaultProject := ""
		if t := m.tasklist.Selected(); t != nil {
			defaultProject = t.Project
		}
		m.newtask = NewNewTaskForm(m.theme, m.db.Projects(), defaultProject)
		m.newtask.SetSize(m.width, m.height)
		m.current = viewNewTask
		return m, nil

	case key.Matches(msg, m.keys.StatusFwd):
		if t := m.tasklist.Selected(); t != nil {
			t.SetStatus(t.Status.Next())
			_ = m.db.Update(t)
			m.refreshTasks()
		}
		return m, nil

	case key.Matches(msg, m.keys.StatusRev):
		if t := m.tasklist.Selected(); t != nil {
			t.SetStatus(t.Status.Prev())
			_ = m.db.Update(t)
			m.refreshTasks()
		}
		return m, nil

	case key.Matches(msg, m.keys.Delete):
		if m.tasklist.Selected() != nil {
			m.current = viewConfirmDelete
		}
		return m, nil

	case key.Matches(msg, m.keys.Help):
		m.current = viewHelp
		return m, nil

	case key.Matches(msg, m.keys.Attach):
		return m.attachAgent()

	case key.Matches(msg, m.keys.Prompt):
		if t := m.tasklist.Selected(); t != nil && t.Status != model.StatusComplete {
			m.current = viewPrompt
		}
		return m, nil

	case key.Matches(msg, m.keys.Destroy):
		if m.tasklist.Selected() != nil {
			m.current = viewConfirmDestroy
		}
		return m, nil

	case key.Matches(msg, m.keys.Prune):
		pruned, err := m.db.PruneCompleted()
		if err != nil {
			m.statusbar.SetError(err.Error())
			return m, nil
		}
		if len(pruned) == 0 {
			return m, nil
		}

		// Stop sessions synchronously (fast, in-process).
		for _, t := range pruned {
			if m.runner.HasSession(t.ID) {
				_ = m.runner.Stop(t.ID)
			}
		}

		// Check if any worktree cleanup is needed.
		cfg := m.db.Config()
		needsCleanup := cfg.UI.ShouldCleanupWorktrees()
		var toClean []*model.Task
		if needsCleanup {
			for _, t := range pruned {
				if t.Worktree != "" {
					toClean = append(toClean, t)
				}
			}
		}

		if len(toClean) == 0 {
			// No worktree cleanup needed — just refresh immediately.
			m.refreshTasks()
			return m, nil
		}

		// Show progress modal and run cleanup iteratively.
		m.pruneTotal = len(toClean)
		m.pruneCurrent = 0
		m.current = viewPruning

		return m, pruneNextCmd(0, len(toClean), toClean, cfg)
	}

	return m, nil
}

// pruneNextCmd cleans one worktree and returns a PruneProgressMsg with the remainder.
func pruneNextCmd(done, total int, remaining []*model.Task, cfg config.Config) tea.Cmd {
	return func() tea.Msg {
		t := remaining[0]
		repoDir := agent.ResolveDir(t, cfg)
		removeWorktreeAndBranch(t.Worktree, t.Branch, repoDir)
		return PruneProgressMsg{
			Current:   done + 1,
			Total:     total,
			Remaining: remaining[1:],
			Cfg:       cfg,
		}
	}
}

func (m Model) attachAgent() (tea.Model, tea.Cmd) {
	t := m.tasklist.Selected()
	if t == nil {
		return m, nil
	}
	if t.Status == model.StatusComplete {
		m.statusbar.SetError("cannot attach to a completed task")
		return m, nil
	}
	return m.startOrAttach(t)
}

func (m Model) startOrAttach(t *model.Task) (tea.Model, tea.Cmd) {
	// If session already exists in runner, switch to agent view
	if m.runner.Get(t.ID) != nil {
		return m, m.enterAgentView(t.ID, t.Name)
	}

	// If sandbox is enabled but srt is not installed, show install modal
	cfg := m.db.Config()
	if cfg.Sandbox.Enabled && !agent.IsSandboxAvailable() {
		m.sandboxInstallPending = t
		m.sandboxInstalling = false
		m.sandboxInstallResult = ""
		m.current = viewSandboxInstall
		return m, nil
	}

	// If the task already has a session ID, resume that conversation;
	// otherwise generate a new one for a fresh start.
	resume := t.SessionID != ""
	if !resume {
		t.SessionID = model.GenerateSessionID()
	}

	// Cache the worktree dir (already set during task creation).
	if t.Worktree != "" {
		m.resolvedDirs[t.ID] = t.Worktree
	}

	// Persist status and StartedAt BEFORE starting the process so that
	// handleAgentFinished always sees fresh data even if the process exits
	// immediately (race between Start returning and the wait goroutine).
	t.SetStatus(model.StatusInProgress)
	t.StartedAt = time.Now()
	_ = m.db.Update(t)

	// Start a new session — use agent view panel dimensions for PTY size
	avWidths := m.agentview.layout.SplitWidths()
	centerW := avWidths[1]
	contentH := m.height - 1
	ptyRows := uint16(max(contentH-4, 10))
	ptyCols := uint16(max(centerW-4, 40))
	sess, err := m.runner.Start(t, m.db.Config(), ptyRows, ptyCols, resume)
	if err != nil {
		// Start failed — revert status and session ID so the task
		// doesn't appear as "in progress" with no running agent.
		t.SetStatus(model.StatusPending)
		t.SessionID = ""
		t.StartedAt = time.Time{}
		_ = m.db.Update(t)
		m.statusbar.SetError(err.Error())
		return m, nil
	}

	t.AgentPID = sess.PID()
	_ = m.db.Update(t)
	m.refreshTasks()

	return m, m.enterAgentView(t.ID, t.Name)
}

// enterAgentView is the single entry point for switching to agent view.
// It initializes the agent view state and starts the 100ms tick chain
// (if not already running). All agent view entry must go through here
// to prevent tick accumulation from multiple start points.
func (m *Model) enterAgentView(taskID, taskName string) tea.Cmd {
	m.agentview.Enter(taskID, taskName)
	m.agentview.SetSize(m.width, m.height)
	if dir, ok := m.resolvedDirs[taskID]; ok && dir != "" {
		m.agentview.SetWorktreeDir(dir)
	}
	m.current = viewAgent
	// Start the 100ms tick chain if not already running. The chain
	// self-perpetuates via the AgentViewTickMsg handler and stops
	// itself when m.current leaves viewAgent.
	if m.agentTickActive {
		return nil
	}
	m.agentTickActive = true
	return tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
		return AgentViewTickMsg{}
	})
}

// determinePostExitStatus decides what status a task should have after its
// agent process exits. Returns the new status and whether it was a quick exit
// (error/too-fast) that should keep the agent view open.
func determinePostExitStatus(msg AgentFinishedMsg, t *model.Task) (newStatus model.Status, clearSession bool, quickExit bool) {
	if msg.Stopped {
		return model.StatusInReview, false, false
	}
	if msg.Err != nil {
		return t.Status, true, true
	}
	if !t.StartedAt.IsZero() && time.Since(t.StartedAt) < minAgentRunTime {
		return t.Status, true, true
	}
	if t.Worktree != "" && !dirExists(t.Worktree) {
		return model.StatusComplete, false, false
	}
	return model.StatusComplete, false, false
}

func (m Model) handleAgentFinished(msg AgentFinishedMsg) (tea.Model, tea.Cmd) {
	t, err := m.db.Get(msg.TaskID)
	if err != nil {
		return m, nil
	}

	t.AgentPID = 0
	newStatus, clearSession, quickExit := determinePostExitStatus(msg, t)
	t.SetStatus(newStatus)
	if clearSession {
		t.SessionID = ""
	}

	if m.current == viewAgent && m.agentview.taskID == msg.TaskID {
		m.agentview.SetLastOutput(msg.LastOutput)
	}

	if m.current == viewAgent && m.agentview.taskID == msg.TaskID && !quickExit {
		m.current = viewTaskList
	}
	_ = m.db.Update(t)

	m.refreshTasks()
	return m, nil
}

func (m Model) handleSessionResumed(msg SessionResumedMsg) (tea.Model, tea.Cmd) {
	t, err := m.db.Get(msg.TaskID)
	if err != nil {
		return m, nil
	}

	if msg.Err != nil {
		// Resume failed — clear session ID so next manual start is fresh
		t.SessionID = ""
		t.AgentPID = 0
		_ = m.db.Update(t)
	} else {
		t.AgentPID = msg.PID
		_ = m.db.Update(t)
	}

	m.refreshTasks()
	return m, nil
}

func (m Model) handleNewTaskKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd := m.newtask.Update(msg)

	if m.newtask.Canceled() {
		m.current = viewTaskList
		return m, nil
	}

	if m.newtask.Done() {
		task := m.newtask.Task()

		// Create worktree BEFORE persisting the task. If this fails, keep
		// the form open with the error so the user can retry.
		projDir := agent.ResolveDir(task, m.db.Config())
		if projDir == "" {
			m.newtask.SetError("no project directory configured")
			return m, nil
		}
		wt, finalName, wtErr := agent.CreateWorktree(projDir, task.Project, task.Name, task.Branch)
		if wtErr != nil {
			m.newtask.SetError(wtErr.Error())
			return m, nil
		}
		task.Worktree = wt
		task.Name = finalName
		task.Branch = "argus/" + finalName

		_ = m.db.Add(task)
		m.refreshTasks()
		m.current = viewTaskList
		return m.startOrAttach(task)
	}

	return m, cmd
}

// handleConfirmAction handles enter/esc for confirmation dialogs.
// The cleanup function is called with the selected task on confirmation.
func (m Model) handleConfirmAction(msg tea.KeyMsg, cleanup func(*model.Task)) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		if t := m.tasklist.Selected(); t != nil {
			if m.runner.HasSession(t.ID) {
				_ = m.runner.Stop(t.ID)
			}
			cleanup(t)
			_ = m.db.Delete(t.ID)
			m.refreshTasks()
		}
		m.current = viewTaskList
		return m, nil
	case tea.KeyEsc:
		m.current = viewTaskList
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) handleConfirmDeleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.handleConfirmAction(msg, func(t *model.Task) {
		cfg := m.db.Config()
		if t.Worktree != "" && cfg.UI.ShouldCleanupWorktrees() {
			repoDir := agent.ResolveDir(t, cfg)
			removeWorktreeAndBranch(t.Worktree, t.Branch, repoDir)
		} else if t.Branch != "" {
			if repoDir := agent.ResolveDir(t, cfg); repoDir != "" {
				deleteBranch(repoDir, t.Branch)
				deleteRemoteBranch(repoDir, t.Branch)
			}
		}
	})
}

func (m Model) handleConfirmDestroyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m.handleConfirmAction(msg, func(t *model.Task) {
		cfg := m.db.Config()
		if t.Worktree != "" {
			repoDir := agent.ResolveDir(t, cfg)
			removeWorktreeAndBranch(t.Worktree, t.Branch, repoDir)
		} else if t.Branch != "" {
			if repoDir := agent.ResolveDir(t, cfg); repoDir != "" {
				deleteBranch(repoDir, t.Branch)
				deleteRemoteBranch(repoDir, t.Branch)
			}
		}
	})
}

func (m Model) handleSandboxInstallKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.sandboxInstalling {
		// Absorb all keys while installing
		return m, nil
	}

	switch msg.Type {
	case tea.KeyEnter:
		if m.sandboxInstallResult != "" {
			// Install finished (success or failure) — close modal
			if agent.IsSandboxAvailable() && m.sandboxInstallPending != nil {
				// srt is now available — start the pending task
				t := m.sandboxInstallPending
				m.sandboxInstallPending = nil
				m.current = viewTaskList
				return m.startOrAttach(t)
			}
			m.sandboxInstallPending = nil
			m.current = viewTaskList
			return m, nil
		}
		// User confirmed install — run npm install asynchronously
		m.sandboxInstalling = true
		m.sandboxInstallResult = ""
		return m, func() tea.Msg {
			out, err := exec.Command("npm", "install", "-g", "@anthropic-ai/sandbox-runtime").CombinedOutput()
			if err != nil {
				return SandboxInstallMsg{Success: false, Output: strings.TrimSpace(string(out))}
			}
			return SandboxInstallMsg{Success: true, Output: strings.TrimSpace(string(out))}
		}
	case tea.KeyEsc:
		// Close modal — if pending task, start it unsandboxed
		// (BuildCmd gracefully falls through when srt unavailable)
		m.sandboxInstallPending = nil
		m.current = viewTaskList
		return m, nil
	default:
		return m, nil
	}
}

func (m Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		return m, tea.Quit

	case key.Matches(msg, m.keys.Up):
		m.settings.CursorUp()
		return m, nil

	case key.Matches(msg, m.keys.Down):
		m.settings.CursorDown()
		return m, nil

	case key.Matches(msg, m.keys.New):
		// Only allow new project creation when on a project row (or section).
		sel := m.settings.Selected()
		if sel == nil || sel.kind == settingsRowProject {
			m.newproject = NewNewProjectForm(m.theme)
			m.newproject.SetSize(m.width, m.height)
			m.current = viewNewProject
			return m, m.newproject.inputs[0].Focus()
		}
		return m, nil

	case msg.Type == tea.KeyEnter || msg.Type == tea.KeySpace:
		sel := m.settings.Selected()
		if sel != nil && sel.kind == settingsRowSandbox {
			cfg := m.db.Config()
			_ = m.db.SetSandboxEnabled(!cfg.Sandbox.Enabled)
			agent.ResetSandboxCache()
			m.refreshSettings()
		}
		return m, nil

	case key.Matches(msg, m.keys.Delete):
		if m.settings.SelectedProject() != nil {
			m.current = viewConfirmDeleteProject
		}
		return m, nil

	case key.Matches(msg, m.keys.RestartDaemon):
		if !m.daemonConnected {
			m.statusbar.SetError("no daemon to restart (running in-process)")
			return m, nil
		}
		m.daemonRestarting = true
		m.current = viewDaemonRestart
		return m, m.restartDaemonCmd()

	case key.Matches(msg, m.keys.Help):
		m.current = viewHelp
		return m, nil
	}

	return m, nil
}

// restartDaemonCmd returns a tea.Cmd that shuts down the current daemon,
// waits for cleanup, starts a new one, and returns a DaemonRestartedMsg.
func (m Model) restartDaemonCmd() tea.Cmd {
	sockPath := daemon.DefaultSocketPath()
	client, ok := m.runner.(*dclient.Client)
	return func() tea.Msg {
		// Shut down the current daemon.
		if ok {
			_ = client.Shutdown()
		}
		dclient.WaitForShutdown(sockPath, 3*time.Second)

		// Start a new daemon.
		newClient, err := dclient.AutoStart(sockPath)
		if err != nil {
			return DaemonRestartedMsg{Err: err}
		}
		return DaemonRestartedMsg{Client: newClient}
	}
}

func (m Model) handleNewProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmd := m.newproject.Update(msg)

	if m.newproject.Canceled() {
		m.current = viewTaskList
		return m, nil
	}

	if m.newproject.Done() {
		name, proj := m.newproject.ProjectEntry()
		_ = m.db.SetProject(name, proj)
		m.refreshSettings()
		m.current = viewTaskList
		return m, nil
	}

	return m, cmd
}

func (m Model) handleConfirmDeleteProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		if entry := m.settings.SelectedProject(); entry != nil {
			_ = m.db.DeleteProject(entry.Name)
			m.refreshSettings()
		}
		m.current = viewTaskList
		return m, nil
	case tea.KeyEsc:
		m.current = viewTaskList
		return m, nil
	default:
		// Any other key cancels
		m.current = viewTaskList
		return m, nil
	}
}

func (m *Model) refreshSettings() {
	var warnings []string
	if !m.daemonConnected {
		warnings = append(warnings, "In-process mode: sessions won't persist")
	}
	m.settings.SetWarnings(warnings)
	m.settings.SetProjects(m.db.Projects())
	m.settings.SetBackends(m.db.Backends())
	m.settings.SetTasks(m.db.Tasks())
	cfg := m.db.Config()
	// IsSandboxAvailable() is cached via sync.Once — only slow on the first call.
	// Always probe so the settings view shows correct install status regardless of
	// whether sandbox is currently enabled.
	available := agent.IsSandboxAvailable()
	m.settings.SetSandboxConfig(
		cfg.Sandbox.Enabled,
		available,
		cfg.Sandbox.AllowedDomains,
		cfg.Sandbox.DenyRead,
		cfg.Sandbox.ExtraWrite,
	)
}

// selectedTaskForGit returns the task whose git status should be refreshed.
func (m Model) selectedTaskForGit() *model.Task {
	if m.current == viewAgent {
		if t, err := m.db.Get(m.agentview.taskID); err == nil {
			return t
		}
		return nil
	}
	return m.tasklist.Selected()
}

// scheduleGitRefresh checks the selected task and returns a tea.Cmd to
// refresh git status asynchronously. Directory resolution is cached; on a
// cache miss the slow discovery runs in a background goroutine.
func (m Model) scheduleGitRefresh() tea.Cmd {
	t := m.selectedTaskForGit()
	if t == nil {
		m.gitstatus.SetTask("")
		return nil
	}

	// Fast path: dir already cached.
	if dir, ok := m.resolvedDirs[t.ID]; ok && dir != "" {
		m.gitstatus.SetTask(t.ID)
		if m.current == viewAgent && m.agentview.taskID == t.ID {
			m.agentview.SetWorktreeDir(dir)
		}
		needsMain := m.gitstatus.NeedsRefresh()
		needsAgent := m.current == viewAgent && m.agentview.NeedsGitRefresh()
		if needsMain || needsAgent {
			taskID := t.ID
			return func() tea.Msg {
				return FetchGitStatus(taskID, dir)
			}
		}
		return nil
	}

	// Try cheap resolution first (stored worktree, project config, runner).
	dir := m.resolveTaskDirFast(t)
	if dir != "" {
		m.resolvedDirs[t.ID] = dir
		m.gitstatus.SetTask(t.ID)
		taskID := t.ID
		return func() tea.Msg {
			return FetchGitStatus(taskID, dir)
		}
	}

	// Slow path: need to discover worktree — run async.
	// Compute the base dir cheaply (project path or runner work dir).
	baseDir := agent.ResolveDir(t, m.db.Config())
	if baseDir == "" {
		baseDir = m.runner.WorkDir(t.ID)
	}
	if baseDir == "" {
		m.gitstatus.SetTask("")
		return nil
	}
	taskID := t.ID
	taskName := t.Name
	projectName := t.Project
	return func() tea.Msg {
		return resolveTaskDirAsync(taskID, taskName, projectName, baseDir)
	}
}

// resolveTaskDirFast returns the task's working directory using only cheap
// lookups (cached worktree path, project config, runner). Returns "" if the
// directory cannot be determined without running git commands.
func (m Model) resolveTaskDirFast(t *model.Task) string {
	dir := t.Worktree

	// Validate stored worktree: must exist and match the task name.
	if dir != "" {
		if !dirExists(dir) || filepath.Base(dir) != t.Name {
			t.Worktree = ""
			_ = m.db.Update(t)
			delete(m.resolvedDirs, t.ID)
			dir = ""
		}
	}

	if dir != "" {
		return dir
	}

	// No stored worktree — can't resolve without async discovery.
	return ""
}

// resolveTaskDirAsync performs worktree discovery off the main thread.
func resolveTaskDirAsync(taskID, taskName, projectName, baseDir string) ResolveTaskDirMsg {
	dir := baseDir
	if wt := discoverWorktree(projectName, taskName); wt != "" {
		dir = wt
	}
	return ResolveTaskDirMsg{TaskID: taskID, Dir: dir}
}

func (m *Model) refreshTasks() {
	tasks := m.db.Tasks()
	running := m.runner.Running()
	idle := m.runner.Idle()
	m.tasklist.SetTasks(tasks)
	m.tasklist.SetRunning(running)
	m.tasklist.SetIdle(idle)
	m.statusbar.SetTasks(tasks)
	m.statusbar.SetRunning(running)
}



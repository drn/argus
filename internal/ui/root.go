package ui

import (
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/agent"
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
	viewAgent
)

type tab int

const (
	tabTasks tab = iota
	tabProjects
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

// Model is the top-level Bubble Tea model.
type Model struct {
	db          *db.DB
	runner      *agent.Runner
	keys        KeyMap
	theme       Theme
	tasklist    TaskList
	projectlist ProjectList
	statusbar   StatusBar
	helpview    HelpView
	newtask     NewTaskForm
	newproject  NewProjectForm
	preview     Preview
	gitstatus   *GitStatus
	agentview   *AgentView
	current      view
	activeTab    tab
	width        int
	height       int
	quitting     bool
	resolvedDirs map[string]string // taskID → resolved worktree dir (cache)
}

func NewModel(database *db.DB, runner *agent.Runner) Model {
	theme := DefaultTheme()
	keys := DefaultKeyMap()

	tl := NewTaskList(theme)
	pl := NewProjectList(theme)
	sb := NewStatusBar(theme)
	hv := NewHelpView(keys, theme)

	pv := NewPreview(theme, runner)
	gs := NewGitStatus(theme)
	avv := NewAgentView(theme, runner)
	av := &avv

	m := Model{
		db:           database,
		runner:       runner,
		keys:         keys,
		theme:        theme,
		resolvedDirs: make(map[string]string),
		tasklist:    tl,
		projectlist: pl,
		statusbar:   sb,
		helpview:    hv,
		preview:     pv,
		gitstatus:   &gs,
		agentview:   av,
		current:     viewTaskList,
		activeTab:   tabTasks,
	}
	m.refreshTasks()
	m.refreshProjects()
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.Tick(time.Second, func(_ time.Time) tea.Msg {
			return TickMsg{}
		}),
	}

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

			// Backfill worktree path for tasks created before proactive
			// worktree tracking was added. The session is stored under the
			// worktree's project hash, so --resume needs the correct CWD.
			if task.Worktree == "" && task.Name != "" {
				if projDir := agent.ResolveDir(task, m.db.Config()); projDir != "" {
					expected := filepath.Join(projDir, ".claude", "worktrees", "argus", task.Name)
					if dirExists(expected) {
						task.Worktree = expected
						m.resolvedDirs[task.ID] = expected
					}
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

	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		leftWidth, rightWidth := m.splitWidths()
		// Reserve space: section header(1) + gap(1) + statusbar(1)
		contentHeight := msg.Height - 3
		m.tasklist.SetSize(leftWidth, contentHeight)
		m.projectlist.SetSize(leftWidth, contentHeight)
		gitH, previewH := m.splitRightHeights(contentHeight)
		m.gitstatus.SetSize(rightWidth, gitH)
		m.preview.SetSize(rightWidth, previewH)
		m.statusbar.SetWidth(msg.Width)
		m.newtask.SetSize(msg.Width, msg.Height)
		m.newproject.SetSize(msg.Width, msg.Height)
		m.agentview.SetSize(msg.Width, msg.Height)
		return m, nil

	case TickMsg:
		// Keep running state fresh so idle tasks display correctly.
		m.refreshTasks()
		var cmds []tea.Cmd
		cmds = append(cmds, tea.Tick(time.Second, func(_ time.Time) tea.Msg {
			return TickMsg{}
		}))
		if m.current == viewAgent {
			cmds = append(cmds, tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
				return AgentViewTickMsg{}
			}))
		}
		if cmd := m.scheduleGitRefresh(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case ResolveTaskDirMsg:
		if msg.Dir != "" {
			m.resolvedDirs[msg.TaskID] = msg.Dir
			// Persist discovered worktree to the task record.
			if t, err := m.db.Get(msg.TaskID); err == nil && t.Worktree == "" {
				t.Worktree = msg.Dir
				_ = m.db.Update(t)
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
		// Fast tick for agent view — just triggers a re-render
		if m.current == viewAgent {
			var cmds []tea.Cmd
			cmds = append(cmds, tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
				return AgentViewTickMsg{}
			}))
			return m, tea.Batch(cmds...)
		}
		return m, nil

	case GitStatusRefreshMsg:
		m.gitstatus.Update(msg)
		m.agentview.UpdateGitStatus(msg)
		return m, nil

	case SessionResumedMsg:
		return m.handleSessionResumed(msg)

	case AgentFinishedMsg:
		return m.handleAgentFinished(msg)

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
	case viewAgent:
		return m.handleAgentViewKey(msg)
	default:
		// Tab switching with 1/2 keys or left/right arrows
		switch msg.String() {
		case "1":
			m.activeTab = tabTasks
			return m, nil
		case "2":
			m.activeTab = tabProjects
			return m, nil
		}
		switch {
		case key.Matches(msg, m.keys.TabLeft):
			if m.activeTab > tabTasks {
				m.activeTab--
			}
			return m, nil
		case key.Matches(msg, m.keys.TabRight):
			if m.activeTab < tabProjects {
				m.activeTab++
			}
			return m, nil
		}
		if m.activeTab == tabProjects {
			return m.handleProjectListKey(msg)
		}
		return m.handleTaskListKey(msg)
	}
}

func (m Model) handleAgentViewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if detach := m.agentview.HandleKey(msg); detach {
		m.current = viewTaskList
		m.refreshTasks()
	}
	return m, nil
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
		m.newtask = NewNewTaskForm(m.theme, m.db.Projects())
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
		for _, t := range pruned {
			if m.runner.HasSession(t.ID) {
				_ = m.runner.Stop(t.ID)
			}
			if t.Worktree != "" && m.db.Config().UI.ShouldCleanupWorktrees() {
				removeWorktree(t.Worktree)
			}
		}
		m.refreshTasks()
		return m, nil
	}

	return m, nil
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
		m.agentview.Enter(t.ID, t.Name)
		m.agentview.SetSize(m.width, m.height)
		m.current = viewAgent
		return m, tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
			return AgentViewTickMsg{}
		})
	}

	// If the task already has a session ID, resume that conversation;
	// otherwise generate a new one for a fresh start.
	resume := t.SessionID != ""
	if !resume {
		t.SessionID = model.GenerateSessionID()
	}

	// Proactively compute the expected worktree path for new sessions.
	// Claude Code creates worktrees under <project>/.claude/worktrees/<name>,
	// and sessions are scoped to that directory. We need to store this path
	// so that resume can use the correct working directory.
	if !resume && t.Name != "" && t.Worktree == "" {
		if projDir := agent.ResolveDir(t, m.db.Config()); projDir != "" {
			t.Worktree = filepath.Join(projDir, ".claude", "worktrees", "argus", t.Name)
			m.resolvedDirs[t.ID] = t.Worktree
		}
	}

	// Persist status and StartedAt BEFORE starting the process so that
	// handleAgentFinished always sees fresh data even if the process exits
	// immediately (race between Start returning and the wait goroutine).
	t.SetStatus(model.StatusInProgress)
	t.StartedAt = time.Now()
	_ = m.db.Update(t)

	// Start a new session — use agent view panel dimensions for PTY size
	_, centerW, _ := m.agentview.splitWidths()
	contentH := m.height - 1
	ptyRows := uint16(max(contentH-4, 10))
	ptyCols := uint16(max(centerW-4, 40))
	sess, err := m.runner.Start(t, m.db.Config(), ptyRows, ptyCols, resume)
	if err != nil {
		// Start failed — revert the session ID so the next attempt
		// doesn't try to --resume a session that was never created.
		t.SessionID = ""
		_ = m.db.Update(t)
		m.statusbar.SetError(err.Error())
		return m, nil
	}

	t.AgentPID = sess.PID()
	_ = m.db.Update(t)
	m.refreshTasks()

	m.agentview.Enter(t.ID, t.Name)
	m.agentview.SetSize(m.width, m.height)
	m.current = viewAgent
	return m, tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
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
		if t.Worktree != "" && m.db.Config().UI.ShouldCleanupWorktrees() {
			removeWorktree(t.Worktree)
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
			}
		}
	})
}

func (m Model) handleProjectListKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Quit):
		m.quitting = true
		return m, tea.Quit

	case key.Matches(msg, m.keys.Up):
		m.projectlist.CursorUp()
		return m, nil

	case key.Matches(msg, m.keys.Down):
		m.projectlist.CursorDown()
		return m, nil

	case key.Matches(msg, m.keys.New):
		m.newproject = NewNewProjectForm(m.theme)
		m.newproject.SetSize(m.width, m.height)
		m.current = viewNewProject
		return m, m.newproject.inputs[0].Focus()

	case key.Matches(msg, m.keys.Delete):
		if m.projectlist.Selected() != nil {
			m.current = viewConfirmDeleteProject
		}
		return m, nil

	case key.Matches(msg, m.keys.Help):
		m.current = viewHelp
		return m, nil
	}

	return m, nil
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
		m.refreshProjects()
		m.current = viewTaskList
		return m, nil
	}

	return m, cmd
}

func (m Model) handleConfirmDeleteProjectKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		if entry := m.projectlist.Selected(); entry != nil {
			_ = m.db.DeleteProject(entry.Name)
			m.refreshProjects()
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

func (m *Model) refreshProjects() {
	m.projectlist.SetProjects(m.db.Projects())
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
	return func() tea.Msg {
		return resolveTaskDirAsync(taskID, taskName, baseDir)
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

// resolveTaskDirAsync performs the slow worktree discovery off the main thread.
func resolveTaskDirAsync(taskID, taskName, baseDir string) ResolveTaskDirMsg {
	dir := baseDir
	if wt := discoverClaudeWorktree(baseDir, taskName); wt != "" {
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



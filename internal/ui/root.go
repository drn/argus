package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	TaskID  string
	Err     error
	Stopped bool // true if the process was explicitly stopped via Runner.Stop
}

// AgentDetachedMsg is sent when the user detaches from a running agent.
type AgentDetachedMsg struct {
	TaskID string
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
	gitstatus   GitStatus
	agentview   *AgentView
	current     view
	activeTab   tab
	width       int
	height      int
	quitting    bool
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
		db:          database,
		runner:      runner,
		keys:        keys,
		theme:       theme,
		tasklist:    tl,
		projectlist: pl,
		statusbar:   sb,
		helpview:    hv,
		preview:     pv,
		gitstatus:   gs,
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
		// Kick off git status refresh if needed
		var cmds []tea.Cmd
		cmds = append(cmds, tea.Tick(time.Second, func(_ time.Time) tea.Msg {
			return TickMsg{}
		}))
		// In agent view, also schedule the fast refresh tick
		if m.current == viewAgent {
			cmds = append(cmds, tea.Tick(100*time.Millisecond, func(_ time.Time) tea.Msg {
				return AgentViewTickMsg{}
			}))
		}
		if t := m.selectedTaskForGit(); t != nil {
			dir := m.resolveTaskDir(t)
			if dir != "" {
				m.gitstatus.SetTask(t.ID)
				if m.gitstatus.NeedsRefresh() {
					taskID := t.ID
					cmds = append(cmds, func() tea.Msg {
						return FetchGitStatus(taskID, dir)
					})
				}
				// Also refresh agent view's git status
				if m.current == viewAgent && m.agentview.NeedsGitRefresh() {
					taskID := t.ID
					cmds = append(cmds, func() tea.Msg {
						return FetchGitStatus(taskID, dir)
					})
				}
			} else {
				m.gitstatus.SetTask("")
			}
		} else {
			m.gitstatus.SetTask("")
		}
		return m, tea.Batch(cmds...)

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
		return m, nil

	case key.Matches(msg, m.keys.Down):
		m.tasklist.CursorDown()
		return m, nil

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

func (m Model) handleAgentFinished(msg AgentFinishedMsg) (tea.Model, tea.Cmd) {
	t, err := m.db.Get(msg.TaskID)
	if err != nil {
		// Task was deleted while agent was running — silently ignore
		return m, nil
	}

	t.AgentPID = 0

	quickExit := false

	if msg.Stopped {
		// Explicitly stopped via Runner.Stop — mark for review
		t.SetStatus(model.StatusInReview)
	} else if msg.Err != nil {
		// Process exited with an error (e.g. failed resume, crash) —
		// keep the task in progress so the user can retry.
		t.SessionID = ""
		quickExit = true
	} else if !t.StartedAt.IsZero() && time.Since(t.StartedAt) < minAgentRunTime {
		// Agent exited too quickly — likely a startup or config error.
		// Keep in progress so the user can retry.
		t.SessionID = ""
		quickExit = true
	} else if t.Worktree != "" && !dirExists(t.Worktree) {
		// Worktree removed — auto-complete
		t.SetStatus(model.StatusComplete)
	} else {
		// Agent session exited on its own — task is complete
		t.SetStatus(model.StatusComplete)
	}

	// On normal completion or explicit stop, return to task list.
	// On quick exit or error, stay on agent view so the user can see
	// the terminal output (error messages, stack traces, etc.).
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

func (m Model) handleConfirmDeleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Confirm):
		if t := m.tasklist.Selected(); t != nil {
			// Stop the agent session if running
			if m.runner.HasSession(t.ID) {
				_ = m.runner.Stop(t.ID)
			}
			// Clean up worktree if configured
			if t.Worktree != "" && m.db.Config().UI.ShouldCleanupWorktrees() {
				removeWorktree(t.Worktree)
			}
			_ = m.db.Delete(t.ID)
			m.refreshTasks()
		}
		m.current = viewTaskList
		return m, nil
	default:
		m.current = viewTaskList
		return m, nil
	}
}

func (m Model) handleConfirmDestroyKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Confirm):
		if t := m.tasklist.Selected(); t != nil {
			// Stop the agent session if running
			if m.runner.HasSession(t.ID) {
				_ = m.runner.Stop(t.ID)
			}
			// Remove worktree and delete branch
			cfg := m.db.Config()
			if t.Worktree != "" {
				repoDir := agent.ResolveDir(t, cfg)
				removeWorktreeAndBranch(t.Worktree, t.Branch, repoDir)
			} else if t.Branch != "" {
				// No worktree but has a branch — try to delete it from project dir
				if repoDir := agent.ResolveDir(t, cfg); repoDir != "" {
					deleteBranch(repoDir, t.Branch)
				}
			}
			_ = m.db.Delete(t.ID)
			m.refreshTasks()
		}
		m.current = viewTaskList
		return m, nil
	default:
		m.current = viewTaskList
		return m, nil
	}
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

// resolveTaskDir finds the working directory for a task's git status.
func (m Model) resolveTaskDir(t *model.Task) string {
	dir := t.Worktree
	if dir == "" {
		dir = agent.ResolveDir(t, m.db.Config())
	}
	if dir == "" {
		dir = m.runner.WorkDir(t.ID)
	}
	if dir != "" && t.Worktree == "" {
		if wt := discoverClaudeWorktree(dir, t.ID); wt != "" {
			t.Worktree = wt
			_ = m.db.Update(t)
			dir = wt
		}
	}
	return dir
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

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	// Status bar at the bottom
	m.statusbar.SetProjectTab(m.activeTab == tabProjects)
	bar := m.statusbar.View()

	// Agent view: full-screen three-panel layout
	if m.current == viewAgent {
		return m.agentview.View()
	}

	// Project delete modal is fully placed (centered), render directly with status bar
	if m.current == viewConfirmDeleteProject {
		return m.confirmDeleteProjectView() + "\n" + bar
	}

	// For overlay views, show them without the banner
	switch m.current {
	case viewHelp, viewPrompt, viewConfirmDelete, viewConfirmDestroy:
		var content string
		switch m.current {
		case viewHelp:
			content = m.helpview.View()
		case viewPrompt:
			content = m.promptView()
		case viewConfirmDelete:
			content = m.confirmDeleteView()
		case viewConfirmDestroy:
			content = m.confirmDestroyView()
		}
		return m.padToBottom(content, bar)
	}

	// Overlay modals
	if m.current == viewNewTask {
		return m.newtask.View() + "\n" + bar
	}
	if m.current == viewNewProject {
		return m.newproject.View() + "\n" + bar
	}

	// Tab header
	tabHeader := m.renderTabHeader()

	switch m.activeTab {
	case tabProjects:
		return m.renderProjectsView(tabHeader, bar)
	default:
		return m.renderTasksView(tabHeader, bar)
	}
}

func (m Model) padToBottom(content, bar string) string {
	contentHeight := m.height - lipgloss.Height(bar) - 1
	if contentHeight < 0 {
		contentHeight = 0
	}
	contentLines := lipgloss.Height(content)
	padding := ""
	if contentLines < contentHeight {
		padding = strings.Repeat("\n", contentHeight-contentLines)
	}
	return content + padding + "\n" + bar
}

// splitRightHeights returns the git status and preview pane heights.
// Git status gets ~30% of the right pane, preview gets the rest.
func (m Model) splitRightHeights(total int) (int, int) {
	gitH := total * 3 / 10
	if gitH < 5 {
		gitH = 5
	}
	if gitH > 15 {
		gitH = 15
	}
	previewH := total - gitH
	if previewH < 5 {
		previewH = 5
	}
	return gitH, previewH
}

// splitWidths returns the left (task list) and right (preview) pane widths.
func (m Model) splitWidths() (int, int) {
	// Give ~40% to task list, rest to preview. Minimum 30 for tasks.
	left := m.width * 2 / 5
	if left < 30 {
		left = 30
	}
	if left > m.width-20 {
		left = m.width - 20
	}
	right := m.width - left
	return left, right
}

func (m Model) renderTabHeader() string {
	activeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("87")).
		Underline(false)
	inactiveStyle := m.theme.Dimmed

	tabs := []struct {
		label string
		key   string
		t     tab
	}{
		{"TASKS", "1", tabTasks},
		{"PROJECTS", "2", tabProjects},
	}

	var parts []string
	for _, t := range tabs {
		style := inactiveStyle
		if t.t == m.activeTab {
			style = activeStyle
		}
		parts = append(parts, style.Render("  "+t.label+" "))
	}
	header := strings.Join(parts, "  ")
	return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, header)
}

func (m Model) renderTasksView(tabHeader, bar string) string {
	// Empty state: show banner centered on page
	if len(m.db.Tasks()) == 0 {
		content := m.emptyStateView()
		return m.padToBottom(content, bar)
	}

	// Split layout: task list on left, agent preview on right
	tasks := m.tasklist.View()
	leftContent := tasks

	// Git status + Preview pane for selected task
	var taskID string
	if t := m.tasklist.Selected(); t != nil {
		taskID = t.ID
	}
	gitView := m.gitstatus.View()
	previewView := m.preview.View(taskID)
	rightContent := lipgloss.JoinVertical(lipgloss.Left, gitView, previewView)

	// Join horizontally
	body := lipgloss.JoinHorizontal(lipgloss.Top, leftContent, rightContent)
	content := tabHeader + "\n" + body

	return m.padToBottom(content, bar)
}

func (m Model) renderProjectsView(tabHeader, bar string) string {
	projects := m.projectlist.View()
	leftContent := projects

	// Right pane: project details for selected project
	rightContent := m.renderProjectDetail()

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftContent, rightContent)
	content := tabHeader + "\n" + body
	return m.padToBottom(content, bar)
}

func (m Model) renderProjectDetail() string {
	entry := m.projectlist.Selected()
	_, rightWidth := m.splitWidths()
	contentHeight := m.height - 3

	if entry == nil {
		empty := m.theme.Dimmed.Render("  No project selected")
		return lipgloss.NewStyle().Width(rightWidth).Height(contentHeight).Render(empty)
	}

	var b strings.Builder
	b.WriteString(m.theme.Title.Render("  "+entry.Name) + "\n\n")

	fields := []struct{ label, value string }{
		{"Path", entry.Project.Path},
		{"Branch", entry.Project.Branch},
		{"Backend", entry.Project.Backend},
	}
	for _, f := range fields {
		val := f.value
		if val == "" {
			val = "(default)"
		}
		b.WriteString("  " + m.theme.Dimmed.Render(f.label+": ") + m.theme.Normal.Render(val) + "\n")
	}

	return lipgloss.NewStyle().Width(rightWidth).Height(contentHeight).Render(b.String())
}



func (m Model) emptyStateView() string {
	banner := renderBanner(m.width)
	hint := m.theme.Dimmed.Render("Press [n] to create your first task")
	hint = lipgloss.PlaceHorizontal(m.width, lipgloss.Center, hint)
	block := banner + "\n\n" + hint

	// Center vertically in the available content area
	barHeight := 1
	contentHeight := m.height - barHeight - 1
	blockHeight := lipgloss.Height(block)
	topPad := (contentHeight - blockHeight) / 2
	if topPad < 0 {
		topPad = 0
	}

	return strings.Repeat("\n", topPad) + block
}

func (m Model) promptView() string {
	t := m.tasklist.Selected()
	if t == nil {
		return ""
	}

	title := m.theme.Title.Render("Prompt: " + t.Name)
	prompt := t.Prompt
	if prompt == "" {
		prompt = m.theme.Dimmed.Render("(no prompt set)")
	}

	return title + "\n\n  " + prompt + "\n\n" +
		m.theme.Help.Render("  Press any key to close")
}

func (m Model) confirmDeleteProjectView() string {
	entry := m.projectlist.Selected()
	if entry == nil {
		return ""
	}

	// Build modal content
	title := m.theme.Title.Render("Delete project?")
	name := "  " + m.theme.Normal.Render(entry.Name)
	path := "  " + m.theme.Dimmed.Render(entry.Project.Path)
	hint := m.theme.Help.Render("  [enter] confirm  [esc] cancel")

	body := title + "\n\n" + name + "\n" + path + "\n\n" + hint

	// Render as a bordered modal centered on screen
	modalWidth := 50
	if m.width > 0 && modalWidth > m.width-4 {
		modalWidth = m.width - 4
	}
	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("238")).
		Padding(1, 2).
		Width(modalWidth).
		Render(body)

	// Center horizontally and vertically
	centered := lipgloss.Place(m.width, m.height-1, lipgloss.Center, lipgloss.Center, modal)
	return centered
}

func (m Model) confirmDeleteView() string {
	t := m.tasklist.Selected()
	if t == nil {
		return ""
	}
	return m.theme.Title.Render("Delete task?") + "\n\n" +
		"  " + m.theme.Normal.Render(t.Name) + "\n\n" +
		m.theme.Help.Render("  [y] confirm  [any other key] cancel")
}

func (m Model) confirmDestroyView() string {
	t := m.tasklist.Selected()
	if t == nil {
		return ""
	}
	var details []string
	details = append(details, "  "+m.theme.Normal.Render(t.Name))
	if t.Worktree != "" {
		details = append(details, "  "+m.theme.Dimmed.Render("worktree: "+t.Worktree))
	}
	if t.Branch != "" {
		details = append(details, "  "+m.theme.Dimmed.Render("branch: "+t.Branch))
	}
	return m.theme.Title.Render("Destroy task?") + "\n" +
		m.theme.Help.Render("  This will terminate the agent, remove the worktree and branch, and delete the task.") + "\n\n" +
		strings.Join(details, "\n") + "\n\n" +
		m.theme.Help.Render("  [y] confirm  [any other key] cancel")
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// removeWorktree removes a git worktree directory. It first tries
// "git worktree remove" (which cleans up .git/worktrees metadata),
// falling back to a plain directory removal if the git command fails.
// discoverClaudeWorktree looks for a Claude Code worktree under baseDir/.claude/worktrees/.
// It parses `git worktree list --porcelain` to find worktrees in that subdirectory.
// Falls back to scanning the directory if git fails. Returns empty string if none found.
func discoverClaudeWorktree(baseDir, _ string) string {
	claudeWtDir := filepath.Join(baseDir, ".claude", "worktrees")
	if !dirExists(claudeWtDir) {
		return ""
	}

	// Try git worktree list first for accuracy
	out, err := runGit(baseDir, "worktree", "list", "--porcelain")
	if err == nil {
		for _, block := range strings.Split(out, "\n\n") {
			for _, line := range strings.Split(block, "\n") {
				if strings.HasPrefix(line, "worktree ") {
					wt := strings.TrimPrefix(line, "worktree ")
					if strings.HasPrefix(wt, claudeWtDir+string(filepath.Separator)) || strings.HasPrefix(wt, claudeWtDir+"/") {
						return wt
					}
				}
			}
		}
	}

	// Fallback: scan directory for worktree subdirs
	entries, err := os.ReadDir(claudeWtDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			candidate := filepath.Join(claudeWtDir, e.Name())
			// Verify it's a git worktree (has .git file)
			if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
				return candidate
			}
		}
	}
	return ""
}

// killStaleProcess sends SIGTERM to a process if it's still alive and waits
// briefly for it to exit. Used to clean up orphaned agent processes from a
// previous Argus session before resuming with --resume.
func killStaleProcess(pid int) {
	if pid <= 0 {
		return
	}
	// Signal 0 checks if the process exists without sending a signal.
	if syscall.Kill(pid, 0) != nil {
		return // already dead
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)

	// Wait up to 2 seconds for the process to exit so that any session
	// locks it holds are released before we start a new --resume process.
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if syscall.Kill(pid, 0) != nil {
			return // exited
		}
	}
	// Force-kill if it's still hanging around.
	_ = syscall.Kill(pid, syscall.SIGKILL)
}

// removeWorktreeAndBranch removes a git worktree and deletes its associated branch.
// repoDir is the main repository directory used for branch deletion; if empty,
// the worktree's parent directory is used as a fallback.
func removeWorktreeAndBranch(worktreePath, branch, repoDir string) {
	removeWorktree(worktreePath)
	if branch == "" {
		return
	}
	// Use main repo dir for branch deletion; fall back to worktree parent.
	dir := repoDir
	if dir == "" {
		dir = filepath.Dir(worktreePath)
	}
	deleteBranch(dir, branch)
}

// deleteBranch force-deletes a local git branch.
func deleteBranch(repoDir, branch string) {
	if branch == "" || repoDir == "" {
		return
	}
	cmd := exec.Command("git", "branch", "-D", branch)
	cmd.Dir = repoDir
	_ = cmd.Run()
}

func removeWorktree(worktreePath string) {
	if !dirExists(worktreePath) {
		return
	}
	// Find the parent repo by looking for .git in the worktree's parent chain.
	// Git worktree remove needs to run from within the main repo or the worktree itself.
	cmd := exec.Command("git", "worktree", "remove", "--force", filepath.Clean(worktreePath))
	cmd.Dir = filepath.Dir(worktreePath)
	if err := cmd.Run(); err != nil {
		// Fallback: just remove the directory
		_ = os.RemoveAll(worktreePath)
	}
}

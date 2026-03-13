package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/store"
)

type view int

const (
	viewTaskList view = iota
	viewNewTask
	viewHelp
	viewPrompt
	viewConfirmDelete
)

// TickMsg is sent periodically to update elapsed times.
type TickMsg struct{}

// AgentFinishedMsg is sent when an agent process exits.
type AgentFinishedMsg struct {
	TaskID string
	Err    error
}

// AgentDetachedMsg is sent when the user detaches from a running agent.
type AgentDetachedMsg struct {
	TaskID string
}

// Model is the top-level Bubble Tea model.
type Model struct {
	cfg       config.Config
	store     *store.Store
	runner    *agent.Runner
	keys      KeyMap
	theme     Theme
	tasklist  TaskList
	statusbar StatusBar
	helpview  HelpView
	newtask   NewTaskForm
	preview   Preview
	gitstatus GitStatus
	current   view
	width     int
	height    int
	quitting  bool
}

func NewModel(cfg config.Config, s *store.Store, runner *agent.Runner) Model {
	theme := DefaultTheme()
	keys := DefaultKeyMap()

	tl := NewTaskList(theme)
	sb := NewStatusBar(theme)
	hv := NewHelpView(keys, theme)

	pv := NewPreview(theme, runner)
	gs := NewGitStatus(theme)

	m := Model{
		cfg:       cfg,
		store:     s,
		runner:    runner,
		keys:      keys,
		theme:     theme,
		tasklist:  tl,
		statusbar: sb,
		helpview:  hv,
		preview:   pv,
		gitstatus: gs,
		current:   viewTaskList,
	}
	m.refreshTasks()
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Tick(time.Second, func(_ time.Time) tea.Msg {
		return TickMsg{}
	})
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
		gitH, previewH := m.splitRightHeights(contentHeight)
		m.gitstatus.SetSize(rightWidth, gitH)
		m.preview.SetSize(rightWidth, previewH)
		m.statusbar.SetWidth(msg.Width)
		m.newtask.SetSize(msg.Width, msg.Height)
		return m, nil

	case TickMsg:
		// Kick off git status refresh if needed
		var cmds []tea.Cmd
		cmds = append(cmds, tea.Tick(time.Second, func(_ time.Time) tea.Msg {
			return TickMsg{}
		}))
		if t := m.tasklist.Selected(); t != nil {
			// Use explicit worktree path, or fall back to project path from config
			dir := t.Worktree
			if dir == "" {
				dir = agent.ResolveDir(t, m.cfg)
			}
			if dir != "" {
				m.gitstatus.SetTask(t.ID)
				if m.gitstatus.NeedsRefresh() {
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

	case GitStatusRefreshMsg:
		m.gitstatus.Update(msg)
		return m, nil

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
	case viewHelp:
		m.current = viewTaskList
		return m, nil
	case viewPrompt:
		m.current = viewTaskList
		return m, nil
	case viewConfirmDelete:
		return m.handleConfirmDeleteKey(msg)
	default:
		return m.handleTaskListKey(msg)
	}
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
		m.newtask = NewNewTaskForm(m.theme, m.cfg.Projects)
		m.newtask.SetSize(m.width, m.height)
		m.current = viewNewTask
		return m, m.newtask.inputs[0].Focus()

	case key.Matches(msg, m.keys.StatusFwd):
		if t := m.tasklist.Selected(); t != nil {
			t.SetStatus(t.Status.Next())
			_ = m.store.Update(t)
			m.refreshTasks()
		}
		return m, nil

	case key.Matches(msg, m.keys.StatusRev):
		if t := m.tasklist.Selected(); t != nil {
			t.SetStatus(t.Status.Prev())
			_ = m.store.Update(t)
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
		if m.tasklist.Selected() != nil {
			m.current = viewPrompt
		}
		return m, nil
	}

	return m, nil
}

func (m Model) attachAgent() (tea.Model, tea.Cmd) {
	t := m.tasklist.Selected()
	if t == nil {
		return m, nil
	}
	return m.startOrAttach(t)
}

func (m Model) startOrAttach(t *model.Task) (tea.Model, tea.Cmd) {
	// If session already exists in runner, reattach to it
	if sess := m.runner.Get(t.ID); sess != nil {
		attachCmd := &agent.AttachCmd{Session: sess, TaskName: t.Name}
		return m, tea.Exec(attachCmd, func(err error) tea.Msg {
			// err == nil means user detached; process may still be running
			if err != nil {
				return AgentFinishedMsg{TaskID: t.ID, Err: err}
			}
			return AgentDetachedMsg{TaskID: t.ID}
		})
	}

	// If the task already has a session ID, resume that conversation;
	// otherwise generate a new one for a fresh start.
	resume := t.SessionID != ""
	if !resume {
		t.SessionID = model.GenerateSessionID()
	}

	// Start a new session with current terminal dimensions
	sess, err := m.runner.Start(t, m.cfg, uint16(m.height), uint16(m.width), resume)
	if err != nil {
		m.statusbar.SetError(err.Error())
		return m, nil
	}

	t.AgentPID = sess.PID()
	t.SetStatus(model.StatusInProgress)
	_ = m.store.Update(t)
	m.refreshTasks()

	attachCmd := &agent.AttachCmd{Session: sess, TaskName: t.Name}
	return m, tea.Exec(attachCmd, func(err error) tea.Msg {
		if err != nil {
			return AgentFinishedMsg{TaskID: t.ID, Err: err}
		}
		return AgentDetachedMsg{TaskID: t.ID}
	})
}

func (m Model) handleAgentFinished(msg AgentFinishedMsg) (tea.Model, tea.Cmd) {
	t, err := m.store.Get(msg.TaskID)
	if err != nil {
		// Task was deleted while agent was running — silently ignore
		return m, nil
	}

	t.AgentPID = 0

	// If the worktree has been removed, auto-complete the task
	if t.Worktree != "" && !dirExists(t.Worktree) {
		t.SetStatus(model.StatusComplete)
		_ = m.store.Update(t)
		m.refreshTasks()
		return m, nil
	}

	if msg.Err != nil {
		// If the agent failed and had a session ID, clear it so the next
		// attempt starts a fresh session instead of retrying a broken resume.
		if t.SessionID != "" {
			t.SessionID = ""
			m.statusbar.SetError("session expired — press enter to start a new session")
		} else {
			m.statusbar.SetError("agent error: " + msg.Err.Error())
		}
		_ = m.store.Update(t)
	} else {
		t.SetStatus(model.StatusInReview)
		_ = m.store.Update(t)
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
		_ = m.store.Add(task)
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
			if t.Worktree != "" && m.cfg.UI.ShouldCleanupWorktrees() {
				removeWorktree(t.Worktree)
			}
			_ = m.store.Delete(t.ID)
			m.refreshTasks()
		}
		m.current = viewTaskList
		return m, nil
	default:
		m.current = viewTaskList
		return m, nil
	}
}

func (m *Model) refreshTasks() {
	tasks := m.store.Tasks()
	running := m.runner.Running()
	m.tasklist.SetTasks(tasks)
	m.tasklist.SetRunning(running)
	m.statusbar.SetTasks(tasks)
	m.statusbar.SetRunning(running)
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	// Status bar at the bottom
	bar := m.statusbar.View()

	// For overlay views, show them without the banner
	switch m.current {
	case viewHelp, viewPrompt, viewConfirmDelete:
		var content string
		switch m.current {
		case viewHelp:
			content = m.helpview.View()
		case viewPrompt:
			content = m.promptView()
		case viewConfirmDelete:
			content = m.confirmDeleteView()
		}
		return m.padToBottom(content, bar)
	}

	// Split layout: task list on left, agent preview on right
	section := m.renderSectionHeader()
	tasks := m.tasklist.View()
	leftContent := section + "\n" + tasks

	// Git status + Preview pane for selected task
	var taskID string
	if t := m.tasklist.Selected(); t != nil {
		taskID = t.ID
	}
	gitView := m.gitstatus.View()
	previewView := m.preview.View(taskID)
	rightContent := lipgloss.JoinVertical(lipgloss.Left, gitView, previewView)

	// Join horizontally
	content := lipgloss.JoinHorizontal(lipgloss.Top, leftContent, rightContent)

	// Overlay modal for new task form
	if m.current == viewNewTask {
		return m.newtask.View() + "\n" + bar
	}

	return m.padToBottom(content, bar)
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

func (m Model) renderDivider() string {
	if m.width < 1 {
		return ""
	}
	line := strings.Repeat("─", m.width)
	return m.theme.Divider.Render(line)
}

func (m Model) renderSectionHeader() string {
	running := make(map[string]bool)
	for _, id := range m.runner.Running() {
		running[id] = true
	}
	active := 0
	total := len(m.store.Tasks())
	for _, t := range m.store.Tasks() {
		if t.Status == model.StatusInProgress && running[t.ID] {
			active++
		}
	}

	label := m.theme.Section.Render("  TASKS")
	count := m.theme.Dimmed.Render(fmt.Sprintf("  %d total", total))
	if active > 0 {
		count = m.theme.InProgress.Render(fmt.Sprintf("  %d active", active)) +
			m.theme.Dimmed.Render(fmt.Sprintf("  %d total", total))
	}
	return label + count
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

func (m Model) confirmDeleteView() string {
	t := m.tasklist.Selected()
	if t == nil {
		return ""
	}
	return m.theme.Title.Render("Delete task?") + "\n\n" +
		"  " + m.theme.Normal.Render(t.Name) + "\n\n" +
		m.theme.Help.Render("  [y] confirm  [any other key] cancel")
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// removeWorktree removes a git worktree directory. It first tries
// "git worktree remove" (which cleans up .git/worktrees metadata),
// falling back to a plain directory removal if the git command fails.
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

package ui

import (
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

// Model is the top-level Bubble Tea model.
type Model struct {
	cfg       config.Config
	store     *store.Store
	keys      KeyMap
	theme     Theme
	tasklist  TaskList
	statusbar StatusBar
	helpview  HelpView
	newtask   NewTaskForm
	current   view
	width     int
	height    int
	quitting  bool
}

func NewModel(cfg config.Config, s *store.Store) Model {
	theme := DefaultTheme()
	keys := DefaultKeyMap()

	tl := NewTaskList(theme)
	sb := NewStatusBar(theme)
	hv := NewHelpView(keys, theme)

	m := Model{
		cfg:       cfg,
		store:     s,
		keys:      keys,
		theme:     theme,
		tasklist:  tl,
		statusbar: sb,
		helpview:  hv,
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
		m.tasklist.SetSize(msg.Width, msg.Height-4) // reserve for title + statusbar
		m.statusbar.SetWidth(msg.Width)
		return m, nil

	case TickMsg:
		// Just re-render for elapsed time updates
		return m, tea.Tick(time.Second, func(_ time.Time) tea.Msg {
			return TickMsg{}
		})

	case AgentFinishedMsg:
		return m.handleAgentFinished(msg)

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

	if t.Prompt == "" {
		m.statusbar.SetError("no prompt set — press [p] to view/set prompt")
		return m, nil
	}

	if t.AgentPID != 0 {
		m.statusbar.SetError("agent already running")
		return m, nil
	}

	cmd, err := agent.BuildCmd(t, m.cfg)
	if err != nil {
		m.statusbar.SetError(err.Error())
		return m, nil
	}

	t.SetStatus(model.StatusInProgress)
	_ = m.store.Update(t)
	m.refreshTasks()

	taskID := t.ID
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return AgentFinishedMsg{TaskID: taskID, Err: err}
	})
}

func (m Model) handleAgentFinished(msg AgentFinishedMsg) (tea.Model, tea.Cmd) {
	t, err := m.store.Get(msg.TaskID)
	if err != nil {
		// Task was deleted while agent was running — silently ignore
		return m, nil
	}

	if msg.Err != nil {
		m.statusbar.SetError("agent error: " + msg.Err.Error())
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
		return m, nil
	}

	return m, cmd
}

func (m Model) handleConfirmDeleteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, m.keys.Confirm):
		if t := m.tasklist.Selected(); t != nil {
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
	m.tasklist.SetTasks(tasks)
	m.statusbar.SetTasks(tasks)
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var content string
	switch m.current {
	case viewNewTask:
		content = m.newtask.View()
	case viewHelp:
		content = m.helpview.View()
	case viewPrompt:
		content = m.promptView()
	case viewConfirmDelete:
		content = m.confirmDeleteView()
	default:
		content = m.taskListView()
	}

	// Status bar at the bottom
	bar := m.statusbar.View()

	// Calculate available height for content
	contentHeight := m.height - lipgloss.Height(bar) - 2
	if contentHeight < 0 {
		contentHeight = 0
	}

	// Pad content to push status bar to bottom
	contentLines := lipgloss.Height(content)
	padding := ""
	if contentLines < contentHeight {
		for i := 0; i < contentHeight-contentLines; i++ {
			padding += "\n"
		}
	}

	return content + padding + "\n" + bar
}

func (m Model) taskListView() string {
	active := 0
	for _, t := range m.store.Tasks() {
		if t.Status == model.StatusInProgress {
			active++
		}
	}

	title := m.theme.Title.Render("Argus")
	count := m.theme.Dimmed.Render("")
	if active > 0 {
		count = m.theme.InProgress.Render(" [" + itoa(active) + " active]")
	}

	header := title + count + "\n\n"
	return header + m.tasklist.View()
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

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

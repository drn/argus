package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// NewTaskForm handles the new task creation UI.
type NewTaskForm struct {
	inputs   []textinput.Model
	focused  int
	theme    Theme
	projects map[string]config.Project
	done     bool
	canceled bool
	width    int
	height   int
}

const (
	fieldProject = iota
	fieldPrompt
	fieldCount
)

func NewNewTaskForm(theme Theme, projects map[string]config.Project) NewTaskForm {
	inputs := make([]textinput.Model, fieldCount)

	projInput := textinput.New()
	projInput.Placeholder = "Project (from config)"
	projInput.CharLimit = 40
	if cwd, err := os.Getwd(); err == nil {
		projInput.SetValue(filepath.Base(cwd))
	}
	inputs[fieldProject] = projInput

	promptInput := textinput.New()
	promptInput.Placeholder = "Prompt for the agent"
	promptInput.CharLimit = 500
	promptInput.Focus()
	inputs[fieldPrompt] = promptInput

	return NewTaskForm{
		inputs:   inputs,
		focused:  fieldPrompt,
		theme:    theme,
		projects: projects,
	}
}

func (f *NewTaskForm) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			f.canceled = true
			return nil
		case "tab", "down":
			f.focused = (f.focused + 1) % fieldCount
			return f.focusCurrent()
		case "shift+tab", "up":
			f.focused = (f.focused - 1 + fieldCount) % fieldCount
			return f.focusCurrent()
		case "enter":
			if f.focused == fieldCount-1 {
				// Submit on enter at last field
				if strings.TrimSpace(f.inputs[fieldPrompt].Value()) != "" {
					f.done = true
				}
				return nil
			}
			f.focused++
			return f.focusCurrent()
		}
	}

	var cmd tea.Cmd
	f.inputs[f.focused], cmd = f.inputs[f.focused].Update(msg)
	return cmd
}

func (f *NewTaskForm) focusCurrent() tea.Cmd {
	cmds := make([]tea.Cmd, len(f.inputs))
	for i := range f.inputs {
		if i == f.focused {
			cmds[i] = f.inputs[i].Focus()
		} else {
			f.inputs[i].Blur()
		}
	}
	return tea.Batch(cmds...)
}

func (f *NewTaskForm) Task() *model.Task {
	project := strings.TrimSpace(f.inputs[fieldProject].Value())
	prompt := strings.TrimSpace(f.inputs[fieldPrompt].Value())

	// Extract keywords from prompt as task name (e.g., "fix-auth-token-refresh")
	name := model.GenerateNameFromPrompt(prompt)

	branch := "main"
	if p, ok := f.projects[project]; ok {
		if p.Branch != "" {
			branch = p.Branch
		}
	}

	return &model.Task{
		Name:    name,
		Status:  model.StatusPending,
		Project: project,
		Branch:  branch,
		Prompt:  prompt,
	}
}

func (f *NewTaskForm) Done() bool     { return f.done }
func (f *NewTaskForm) Canceled() bool { return f.canceled }

func (f *NewTaskForm) SetSize(w, h int) {
	f.width = w
	f.height = h
	// Set input widths to fit inside the modal
	inputWidth := f.modalWidth() - 6 // account for border + padding
	if inputWidth < 20 {
		inputWidth = 20
	}
	for i := range f.inputs {
		f.inputs[i].Width = inputWidth
	}
}

func (f NewTaskForm) modalWidth() int {
	w := f.width * 2 / 5
	if w < 50 {
		w = 50
	}
	if w > 80 {
		w = 80
	}
	if w > f.width-4 {
		w = f.width - 4
	}
	return w
}

func (f NewTaskForm) View() string {
	var b strings.Builder

	labels := []string{"Project:", "Prompt:"}
	for i, label := range labels {
		style := f.theme.Dimmed
		if i == f.focused {
			style = f.theme.Selected
		}
		b.WriteString(style.Render(label) + "\n")
		b.WriteString(f.inputs[i].View() + "\n\n")
	}

	b.WriteString(f.theme.Help.Render("tab/shift+tab: navigate  enter: submit  esc: cancel"))

	mw := f.modalWidth()

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("87")).
		Padding(1, 2).
		Width(mw).
		Render(f.theme.Title.Render("New Task") + "\n\n" + b.String())

	return lipgloss.Place(f.width, f.height, lipgloss.Center, lipgloss.Center, modal)
}

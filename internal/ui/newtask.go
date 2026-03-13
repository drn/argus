package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/darrencheng/argus/internal/config"
	"github.com/darrencheng/argus/internal/model"
)

// NewTaskForm handles the new task creation UI.
type NewTaskForm struct {
	inputs   []textinput.Model
	focused  int
	theme    Theme
	projects map[string]config.Project
	done     bool
	canceled bool
}

const (
	fieldName = iota
	fieldProject
	fieldPrompt
	fieldCount
)

func NewNewTaskForm(theme Theme, projects map[string]config.Project) NewTaskForm {
	inputs := make([]textinput.Model, fieldCount)

	nameInput := textinput.New()
	nameInput.Placeholder = "Task name"
	nameInput.Focus()
	nameInput.CharLimit = 80
	inputs[fieldName] = nameInput

	projInput := textinput.New()
	projInput.Placeholder = "Project (from config)"
	projInput.CharLimit = 40
	inputs[fieldProject] = projInput

	promptInput := textinput.New()
	promptInput.Placeholder = "Prompt for the agent (optional)"
	promptInput.CharLimit = 500
	inputs[fieldPrompt] = promptInput

	return NewTaskForm{
		inputs:   inputs,
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
				if strings.TrimSpace(f.inputs[fieldName].Value()) != "" {
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
	name := strings.TrimSpace(f.inputs[fieldName].Value())
	project := strings.TrimSpace(f.inputs[fieldProject].Value())
	prompt := strings.TrimSpace(f.inputs[fieldPrompt].Value())

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

func (f NewTaskForm) View() string {
	var b strings.Builder
	b.WriteString(f.theme.Title.Render("New Task"))
	b.WriteString("\n\n")

	labels := []string{"Name:", "Project:", "Prompt:"}
	for i, label := range labels {
		style := f.theme.Dimmed
		if i == f.focused {
			style = f.theme.Selected
		}
		b.WriteString("  " + style.Render(label) + "\n")
		b.WriteString("  " + f.inputs[i].View() + "\n\n")
	}

	b.WriteString(f.theme.Help.Render("  tab/shift+tab: navigate  enter: submit  esc: cancel"))
	return b.String()
}

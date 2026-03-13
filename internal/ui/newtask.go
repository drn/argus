package ui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
)

// NewTaskForm handles the new task creation UI.
type NewTaskForm struct {
	promptInput  textinput.Model
	projectNames []string
	projectIdx   int
	focused      int // 0 = project, 1 = prompt
	theme        Theme
	projects     map[string]config.Project
	done         bool
	canceled     bool
	width        int
	height       int
}

const (
	fieldProject = 0
	fieldPrompt  = 1
	fieldCount   = 2
)

func NewNewTaskForm(theme Theme, projects map[string]config.Project) NewTaskForm {
	promptInput := textinput.New()
	promptInput.Placeholder = "Prompt for the agent"
	promptInput.CharLimit = 500
	promptInput.Focus()

	// Build sorted project name list
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)

	return NewTaskForm{
		promptInput:  promptInput,
		projectNames: names,
		projectIdx:   0,
		focused:      fieldPrompt,
		theme:        theme,
		projects:     projects,
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
			if f.focused == fieldProject {
				f.focused = fieldPrompt
				return f.promptInput.Focus()
			}
			f.focused = fieldProject
			f.promptInput.Blur()
			return nil
		case "shift+tab", "up":
			if f.focused == fieldPrompt {
				f.focused = fieldProject
				f.promptInput.Blur()
				return nil
			}
			f.focused = fieldPrompt
			return f.promptInput.Focus()
		case "left":
			if f.focused == fieldProject && len(f.projectNames) > 0 {
				f.projectIdx = (f.projectIdx - 1 + len(f.projectNames)) % len(f.projectNames)
				return nil
			}
		case "right":
			if f.focused == fieldProject && len(f.projectNames) > 0 {
				f.projectIdx = (f.projectIdx + 1) % len(f.projectNames)
				return nil
			}
		case "enter":
			if f.focused == fieldProject {
				f.focused = fieldPrompt
				return f.promptInput.Focus()
			}
			// Submit on enter at prompt field
			if strings.TrimSpace(f.promptInput.Value()) != "" {
				f.done = true
			}
			return nil
		}
	}

	if f.focused == fieldPrompt {
		var cmd tea.Cmd
		f.promptInput, cmd = f.promptInput.Update(msg)
		return cmd
	}

	return nil
}

func (f *NewTaskForm) SelectedProject() string {
	if len(f.projectNames) == 0 {
		return ""
	}
	return f.projectNames[f.projectIdx]
}

func (f *NewTaskForm) Task() *model.Task {
	project := f.SelectedProject()
	prompt := strings.TrimSpace(f.promptInput.Value())

	name := model.GenerateNameFromPrompt(prompt)

	branch := "master"
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
	inputWidth := f.modalWidth() - 6
	if inputWidth < 20 {
		inputWidth = 20
	}
	f.promptInput.Width = inputWidth
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

	// Project selector
	projStyle := f.theme.Dimmed
	if f.focused == fieldProject {
		projStyle = f.theme.Selected
	}
	b.WriteString(projStyle.Render("Project:") + "\n")
	b.WriteString(f.renderProjectSelector() + "\n\n")

	// Prompt input
	promptStyle := f.theme.Dimmed
	if f.focused == fieldPrompt {
		promptStyle = f.theme.Selected
	}
	b.WriteString(promptStyle.Render("Prompt:") + "\n")
	b.WriteString(f.promptInput.View() + "\n\n")

	b.WriteString(f.theme.Help.Render("tab/shift+tab: navigate  \u2190/\u2192: select project  enter: submit  esc: cancel"))

	mw := f.modalWidth()

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("87")).
		Padding(1, 2).
		Width(mw).
		Render(f.theme.Title.Render("New Task") + "\n\n" + b.String())

	return lipgloss.Place(f.width, f.height, lipgloss.Center, lipgloss.Center, modal)
}

func (f NewTaskForm) renderProjectSelector() string {
	if len(f.projectNames) == 0 {
		return f.theme.Dimmed.Render("  (no projects configured)")
	}

	arrow := f.theme.Dimmed
	selected := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("87"))

	left := arrow.Render("\u25c0 ")
	right := arrow.Render(" \u25b6")
	name := selected.Render(f.projectNames[f.projectIdx])

	counter := f.theme.Dimmed.Render(
		fmt.Sprintf(" (%d/%d)", f.projectIdx+1, len(f.projectNames)))

	return "  " + left + name + right + counter
}

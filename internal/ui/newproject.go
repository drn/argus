package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/config"
)

// NewProjectForm handles the new project creation UI.
type NewProjectForm struct {
	inputs   []textinput.Model
	focused  int
	theme    Theme
	done     bool
	canceled bool
	width    int
	height   int
}

const (
	projFieldName = iota
	projFieldPath
	projFieldBranch
	projFieldBackend
	projFieldCount
)

func NewNewProjectForm(theme Theme) NewProjectForm {
	inputs := make([]textinput.Model, projFieldCount)

	nameInput := textinput.New()
	nameInput.Placeholder = "Project name (e.g. argus)"
	nameInput.CharLimit = 40
	inputs[projFieldName] = nameInput

	pathInput := textinput.New()
	pathInput.Placeholder = "Path to repository"
	pathInput.CharLimit = 200
	inputs[projFieldPath] = pathInput

	branchInput := textinput.New()
	branchInput.Placeholder = "Default branch (e.g. master)"
	branchInput.CharLimit = 60
	branchInput.SetValue("master")
	inputs[projFieldBranch] = branchInput

	backendInput := textinput.New()
	backendInput.Placeholder = "Backend (leave empty for default)"
	backendInput.CharLimit = 40
	inputs[projFieldBackend] = backendInput

	return NewProjectForm{
		inputs:  inputs,
		focused: projFieldName,
		theme:   theme,
	}
}

func (f *NewProjectForm) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			f.canceled = true
			return nil
		case "tab", "down":
			f.focused = (f.focused + 1) % projFieldCount
			return f.focusCurrent()
		case "shift+tab", "up":
			f.focused = (f.focused - 1 + projFieldCount) % projFieldCount
			return f.focusCurrent()
		case "enter":
			if f.focused == projFieldCount-1 {
				// Submit on enter at last field
				if strings.TrimSpace(f.inputs[projFieldName].Value()) != "" &&
					strings.TrimSpace(f.inputs[projFieldPath].Value()) != "" {
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

func (f *NewProjectForm) focusCurrent() tea.Cmd {
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

func (f *NewProjectForm) ProjectEntry() (string, config.Project) {
	name := strings.TrimSpace(f.inputs[projFieldName].Value())
	path := strings.TrimSpace(f.inputs[projFieldPath].Value())
	branch := strings.TrimSpace(f.inputs[projFieldBranch].Value())
	backend := strings.TrimSpace(f.inputs[projFieldBackend].Value())

	return name, config.Project{
		Path:    path,
		Branch:  branch,
		Backend: backend,
	}
}

func (f *NewProjectForm) Done() bool     { return f.done }
func (f *NewProjectForm) Canceled() bool { return f.canceled }

func (f *NewProjectForm) SetSize(w, h int) {
	f.width = w
	f.height = h
	inputWidth := f.modalWidth() - 6
	if inputWidth < 20 {
		inputWidth = 20
	}
	for i := range f.inputs {
		f.inputs[i].Width = inputWidth
	}
}

func (f NewProjectForm) modalWidth() int {
	return clampModalWidth(f.width)
}

func (f NewProjectForm) View() string {
	var b strings.Builder

	labels := []string{"Name:", "Path:", "Branch:", "Backend:"}
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
		Render(f.theme.Title.Render("New Project") + "\n\n" + b.String())

	return lipgloss.Place(f.width, f.height, lipgloss.Center, lipgloss.Center, modal)
}

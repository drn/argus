package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/drn/argus/internal/config"
)

// BackendForm handles the backend creation and editing UI.
type BackendForm struct {
	inputs       []textinput.Model
	focused      int
	theme        Theme
	done         bool
	canceled     bool
	width        int
	height       int
	editMode     bool   // true when editing an existing backend
	originalName string // backend name being edited (read-only in edit mode)
}

const (
	backendFieldName = iota
	backendFieldCommand
	backendFieldResumeCommand
	backendFieldPromptFlag
	backendFieldCount
)

func NewBackendForm(theme Theme) BackendForm {
	inputs := make([]textinput.Model, backendFieldCount)

	nameInput := textinput.New()
	nameInput.Placeholder = "Backend name (e.g. codex)"
	nameInput.CharLimit = 40
	inputs[backendFieldName] = nameInput

	commandInput := textinput.New()
	commandInput.Placeholder = "Command (e.g. codex --full-auto)"
	commandInput.CharLimit = 200
	inputs[backendFieldCommand] = commandInput

	resumeInput := textinput.New()
	resumeInput.Placeholder = "Resume command (optional, e.g. codex resume --full-auto --last)"
	resumeInput.CharLimit = 200
	inputs[backendFieldResumeCommand] = resumeInput

	promptFlagInput := textinput.New()
	promptFlagInput.Placeholder = "Prompt flag (usually empty)"
	promptFlagInput.CharLimit = 40
	inputs[backendFieldPromptFlag] = promptFlagInput

	return BackendForm{
		inputs:  inputs,
		focused: backendFieldName,
		theme:   theme,
	}
}

// LoadBackend switches the form into edit mode, pre-populating all fields.
func (f *BackendForm) LoadBackend(name string, b config.Backend) {
	f.editMode = true
	f.originalName = name
	f.inputs[backendFieldName].SetValue(name)
	f.inputs[backendFieldCommand].SetValue(b.Command)
	f.inputs[backendFieldResumeCommand].SetValue(b.ResumeCommand)
	f.inputs[backendFieldPromptFlag].SetValue(b.PromptFlag)
	// Start focus on command since name is read-only in edit mode.
	f.focused = backendFieldCommand
}

func (f *BackendForm) nextField() int {
	if f.editMode {
		switch f.focused {
		case backendFieldCommand:
			return backendFieldResumeCommand
		case backendFieldResumeCommand:
			return backendFieldPromptFlag
		default:
			return backendFieldCommand
		}
	}
	return (f.focused + 1) % backendFieldCount
}

func (f *BackendForm) prevField() int {
	if f.editMode {
		switch f.focused {
		case backendFieldCommand:
			return backendFieldPromptFlag
		case backendFieldResumeCommand:
			return backendFieldCommand
		default:
			return backendFieldResumeCommand
		}
	}
	return (f.focused - 1 + backendFieldCount) % backendFieldCount
}

func (f *BackendForm) lastField() int {
	return backendFieldPromptFlag
}

func (f *BackendForm) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			f.canceled = true
			return nil
		case "tab", "down":
			f.focused = f.nextField()
			return f.focusCurrent()
		case "shift+tab", "up":
			f.focused = f.prevField()
			return f.focusCurrent()
		case "enter":
			if f.focused == f.lastField() {
				nameOK := f.editMode || strings.TrimSpace(f.inputs[backendFieldName].Value()) != ""
				if nameOK && strings.TrimSpace(f.inputs[backendFieldCommand].Value()) != "" {
					f.done = true
				}
				return nil
			}
			f.focused = f.nextField()
			return f.focusCurrent()
		}
		if applyWordNavTextinput(msg, &f.inputs[f.focused]) {
			return nil
		}
	}

	var cmd tea.Cmd
	f.inputs[f.focused], cmd = f.inputs[f.focused].Update(msg)
	return cmd
}

func (f *BackendForm) focusCurrent() tea.Cmd {
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

// BackendEntry returns the name and config for the form values.
func (f *BackendForm) BackendEntry() (string, config.Backend) {
	name := strings.TrimSpace(f.inputs[backendFieldName].Value())
	if f.editMode {
		name = f.originalName
	}
	return name, config.Backend{
		Command:       strings.TrimSpace(f.inputs[backendFieldCommand].Value()),
		PromptFlag:    strings.TrimSpace(f.inputs[backendFieldPromptFlag].Value()),
		ResumeCommand: strings.TrimSpace(f.inputs[backendFieldResumeCommand].Value()),
	}
}

func (f *BackendForm) Done() bool     { return f.done }
func (f *BackendForm) Canceled() bool { return f.canceled }

func (f *BackendForm) SetSize(w, h int) {
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

func (f BackendForm) modalWidth() int {
	return clampModalWidth(f.width)
}

func (f BackendForm) View() string {
	if len(f.inputs) == 0 {
		return ""
	}
	var b strings.Builder

	labels := []struct {
		label string
		field int
	}{
		{"Name:", backendFieldName},
		{"Command:", backendFieldCommand},
		{"Resume Command:", backendFieldResumeCommand},
		{"Prompt Flag:", backendFieldPromptFlag},
	}
	for _, l := range labels {
		if f.editMode && l.field == backendFieldName {
			b.WriteString(f.theme.Dimmed.Render(l.label) + "\n")
			b.WriteString(f.theme.Normal.Render(f.originalName) + "\n\n")
			continue
		}
		style := f.theme.Dimmed
		if l.field == f.focused {
			style = f.theme.Selected
		}
		b.WriteString(style.Render(l.label) + "\n")
		b.WriteString(f.inputs[l.field].View() + "\n\n")
	}

	b.WriteString(f.theme.Help.Render("tab/shift+tab: navigate  enter: submit  esc: cancel"))

	mw := f.modalWidth()

	title := "New Backend"
	if f.editMode {
		title = "Edit Backend"
	}

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("87")).
		Padding(1, 2).
		Width(mw).
		Render(f.theme.Title.Render(title) + "\n\n" + b.String())

	return lipgloss.Place(f.width, f.height, lipgloss.Center, lipgloss.Center, modal)
}

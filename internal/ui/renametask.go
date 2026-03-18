package ui

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// validTaskName matches names safe for git branch names and filesystem paths.
var validTaskName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._\-]*$`)

// RenameTaskForm handles the task rename UI — a single text input
// pre-filled with the current task name.
type RenameTaskForm struct {
	input    textinput.Model
	taskID   string // ID of the task being renamed
	theme    Theme
	done     bool
	canceled bool
	errMsg   string
	width    int
	height   int
	ready    bool // true after constructor runs (guards zero-value)
}

func NewRenameTaskForm(theme Theme, taskID, currentName string) RenameTaskForm {
	ti := textinput.New()
	ti.Placeholder = "task-name"
	ti.CharLimit = 80
	ti.SetValue(currentName)
	ti.CursorEnd()
	ti.Focus()

	return RenameTaskForm{
		input:  ti,
		taskID: taskID,
		theme:  theme,
		ready:  true,
	}
}

func (f *RenameTaskForm) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		f.errMsg = ""
		switch msg.String() {
		case "esc":
			f.canceled = true
			return nil
		case "enter":
			name := strings.TrimSpace(f.input.Value())
			if name == "" {
				f.errMsg = "name cannot be empty"
				return nil
			}
			if !validTaskName.MatchString(name) {
				f.errMsg = "name must start with a letter/digit and contain only letters, digits, '.', '_', '-'"
				return nil
			}
			f.done = true
			return nil
		}
	}

	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

func (f *RenameTaskForm) Done() bool     { return f.done }
func (f *RenameTaskForm) Canceled() bool { return f.canceled }
func (f *RenameTaskForm) TaskID() string { return f.taskID }

func (f *RenameTaskForm) NewName() string {
	return strings.TrimSpace(f.input.Value())
}

func (f *RenameTaskForm) SetError(msg string) {
	f.errMsg = msg
	f.done = false
}

func (f *RenameTaskForm) SetSize(w, h int) {
	f.width = w
	f.height = h
	if !f.ready {
		return
	}
	inputWidth := max(f.modalWidth()-4, 20)
	f.input.Width = inputWidth
}

func (f RenameTaskForm) modalWidth() int {
	return clampModalWidth(f.width)
}

func (f RenameTaskForm) View() string {
	if !f.ready {
		return ""
	}
	var b strings.Builder

	b.WriteString(f.theme.Selected.Render("Name:") + "\n")
	b.WriteString(f.input.View() + "\n\n")

	if f.errMsg != "" {
		errStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		b.WriteString(errStyle.Render("Error: "+f.errMsg) + "\n\n")
	}

	b.WriteString(f.theme.Help.Render("enter: submit  esc: cancel"))

	mw := f.modalWidth()

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("87")).
		Padding(1, 2).
		Width(mw).
		Render(f.theme.Title.Render("Rename Task") + "\n\n" + b.String())

	return lipgloss.Place(f.width, f.height, lipgloss.Center, lipgloss.Center, modal)
}

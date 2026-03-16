package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// SandboxConfigForm handles editing sandbox configuration in a modal.
type SandboxConfigForm struct {
	inputs   []textinput.Model
	focused  int
	enabled  bool
	theme    Theme
	done     bool
	canceled bool
	width    int
	height   int
}

const (
	sbFieldDomains = iota
	sbFieldDenyRead
	sbFieldExtraWrite
	sbFieldCount
)

// NewSandboxConfigForm creates a sandbox config editor, pre-populated with current values.
func NewSandboxConfigForm(theme Theme, enabled bool, domains, denyRead, extraWrite []string) SandboxConfigForm {
	inputs := make([]textinput.Model, sbFieldCount)

	domainsInput := textinput.New()
	domainsInput.Placeholder = "Comma-separated domains (e.g. github.com,npmjs.org)"
	domainsInput.CharLimit = 500
	domainsInput.SetValue(strings.Join(domains, ","))
	inputs[sbFieldDomains] = domainsInput

	denyInput := textinput.New()
	denyInput.Placeholder = "Comma-separated paths (e.g. /secrets,~/.private)"
	denyInput.CharLimit = 500
	denyInput.SetValue(strings.Join(denyRead, ","))
	inputs[sbFieldDenyRead] = denyInput

	writeInput := textinput.New()
	writeInput.Placeholder = "Comma-separated paths (e.g. ~/.npm,/var/cache)"
	writeInput.CharLimit = 500
	writeInput.SetValue(strings.Join(extraWrite, ","))
	inputs[sbFieldExtraWrite] = writeInput

	return SandboxConfigForm{
		inputs:  inputs,
		focused: sbFieldDomains,
		enabled: enabled,
		theme:   theme,
	}
}

func (f *SandboxConfigForm) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEsc:
			f.canceled = true
			return nil
		case tea.KeyTab, tea.KeyDown:
			f.focused = (f.focused + 1) % sbFieldCount
			return f.focusCurrent()
		case tea.KeyShiftTab, tea.KeyUp:
			f.focused = (f.focused - 1 + sbFieldCount) % sbFieldCount
			return f.focusCurrent()
		case tea.KeyCtrlE:
			f.enabled = !f.enabled
			return nil
		case tea.KeyEnter:
			if f.focused == sbFieldCount-1 {
				f.done = true
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

func (f *SandboxConfigForm) focusCurrent() tea.Cmd {
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

// Result returns the edited sandbox config values.
func (f *SandboxConfigForm) Result() (enabled bool, domains, denyRead, extraWrite string) {
	return f.enabled,
		strings.TrimSpace(f.inputs[sbFieldDomains].Value()),
		strings.TrimSpace(f.inputs[sbFieldDenyRead].Value()),
		strings.TrimSpace(f.inputs[sbFieldExtraWrite].Value())
}

func (f *SandboxConfigForm) Done() bool     { return f.done }
func (f *SandboxConfigForm) Canceled() bool { return f.canceled }

// FocusFirst returns a command that focuses the first input field.
func (f *SandboxConfigForm) FocusFirst() tea.Cmd {
	if len(f.inputs) == 0 {
		return nil
	}
	return f.inputs[0].Focus()
}

func (f *SandboxConfigForm) SetSize(w, h int) {
	f.width = w
	f.height = h
	inputWidth := max(f.modalWidth()-6, 20)
	for i := range f.inputs {
		f.inputs[i].Width = inputWidth
	}
}

func (f SandboxConfigForm) modalWidth() int {
	return clampModalWidth(f.width)
}

func (f SandboxConfigForm) View() string {
	if len(f.inputs) == 0 {
		return ""
	}
	var b strings.Builder

	// Enabled toggle
	enabledLabel := f.theme.Error.Render("Disabled")
	if f.enabled {
		enabledLabel = f.theme.Complete.Render("Enabled")
	}
	b.WriteString(f.theme.Dimmed.Render("Sandbox: ") + enabledLabel)
	b.WriteString(f.theme.Dimmed.Render("  (ctrl+e to toggle)") + "\n\n")

	labels := []string{"Allowed Domains:", "Deny Read:", "Extra Write:"}
	for i, label := range labels {
		style := f.theme.Dimmed
		if i == f.focused {
			style = f.theme.Selected
		}
		b.WriteString(style.Render(label) + "\n")
		b.WriteString(f.inputs[i].View() + "\n\n")
	}

	b.WriteString(f.theme.Help.Render("tab/shift+tab: navigate  ctrl+e: toggle  enter: save  esc: cancel"))

	mw := f.modalWidth()

	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("87")).
		Padding(1, 2).
		Width(mw).
		Render(f.theme.Title.Render("Sandbox Configuration") + "\n\n" + b.String())

	return lipgloss.Place(f.width, f.height, lipgloss.Center, lipgloss.Center, modal)
}

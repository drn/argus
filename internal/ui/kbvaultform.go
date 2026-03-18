package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// KBVaultForm is a simple modal to set an Obsidian vault path.
// vaultName is a human label like "Metis (KB)" or "Argus (task sync)".
// configKey is the DB config key to write, e.g. "kb.metis_vault_path".
type KBVaultForm struct {
	input     textinput.Model
	theme     Theme
	vaultName string
	configKey string
	done      bool
	canceled  bool
	width     int
	height    int
	ready     bool
}

func NewKBVaultForm(theme Theme, vaultName, configKey, currentPath string) KBVaultForm {
	ti := textinput.New()
	ti.Placeholder = "/path/to/obsidian/vault"
	ti.CharLimit = 256
	ti.SetValue(currentPath)
	ti.CursorEnd()
	ti.Focus()

	return KBVaultForm{
		input:     ti,
		theme:     theme,
		vaultName: vaultName,
		configKey: configKey,
		ready:     true,
	}
}

func (f *KBVaultForm) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			f.canceled = true
			return nil
		case "enter":
			f.done = true
			return nil
		}
	}
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return cmd
}

func (f *KBVaultForm) Done() bool      { return f.done }
func (f *KBVaultForm) Canceled() bool  { return f.canceled }
func (f *KBVaultForm) Value() string   { return strings.TrimSpace(f.input.Value()) }
func (f KBVaultForm) ConfigKey() string { return f.configKey }

func (f *KBVaultForm) SetSize(w, h int) {
	f.width = w
	f.height = h
	if !f.ready {
		return
	}
	f.input.Width = max(clampModalWidth(w)-4, 20)
}

func (f KBVaultForm) View() string {
	if !f.ready {
		return ""
	}
	var b strings.Builder
	b.WriteString(f.theme.Selected.Render("Vault path:") + "\n")
	b.WriteString(f.input.View() + "\n\n")
	b.WriteString(f.theme.Help.Render("enter: save  esc: cancel"))

	title := "Set Vault Path: " + f.vaultName
	modal := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("87")).
		Padding(1, 2).
		Width(clampModalWidth(f.width)).
		Render(f.theme.Title.Render(title) + "\n\n" + b.String())

	return lipgloss.Place(f.width, f.height, lipgloss.Center, lipgloss.Center, modal)
}

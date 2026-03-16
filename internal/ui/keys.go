package ui

import "github.com/charmbracelet/bubbles/key"

// KeyMap defines all keybindings for the application.
type KeyMap struct {
	New       key.Binding
	Attach    key.Binding
	StatusFwd key.Binding
	StatusRev key.Binding
	Delete    key.Binding
	Destroy   key.Binding
	Quit      key.Binding
	Help      key.Binding
	Filter    key.Binding
	Prompt    key.Binding
	Worktree  key.Binding
	Prune     key.Binding
	RestartDaemon key.Binding
	Up            key.Binding
	Down          key.Binding
	TabLeft       key.Binding
	TabRight      key.Binding
	Confirm       key.Binding
	Cancel        key.Binding
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		New: key.NewBinding(
			key.WithKeys("n"),
			key.WithHelp("n", "new task"),
		),
		Attach: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("↵", "attach"),
		),
		StatusFwd: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "advance status"),
		),
		StatusRev: key.NewBinding(
			key.WithKeys("S"),
			key.WithHelp("S", "revert status"),
		),
		Delete: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "delete"),
		),
		Destroy: key.NewBinding(
			key.WithKeys("ctrl+d"),
			key.WithHelp("^d", "destroy (kill+cleanup+delete)"),
		),
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Filter: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "filter"),
		),
		Prompt: key.NewBinding(
			key.WithKeys("p"),
			key.WithHelp("p", "view prompt"),
		),
		Worktree: key.NewBinding(
			key.WithKeys("w"),
			key.WithHelp("w", "worktree info"),
		),
		Prune: key.NewBinding(
			key.WithKeys("ctrl+r"),
			key.WithHelp("^r", "prune completed"),
		),
		RestartDaemon: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "restart daemon"),
		),
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		TabLeft: key.NewBinding(
			key.WithKeys("left"),
			key.WithHelp("←", "prev tab"),
		),
		TabRight: key.NewBinding(
			key.WithKeys("right"),
			key.WithHelp("→", "next tab"),
		),
		Confirm: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("↵", "confirm"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "cancel"),
		),
	}
}

// ShortHelp returns keybindings shown in the mini help view.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.New, k.Attach, k.StatusFwd, k.Delete, k.Quit, k.Help}
}

// FullHelp returns keybindings for the expanded help view.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.New, k.Attach, k.Delete, k.Destroy},
		{k.StatusFwd, k.StatusRev, k.Prompt},
		{k.Up, k.Down, k.Filter},
		{k.Worktree, k.Prune, k.RestartDaemon, k.Help, k.Quit},
	}
}

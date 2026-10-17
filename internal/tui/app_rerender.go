package tui

import "github.com/drn/argus/internal/tui/terminal"

func (a *App) setRerendering(taskID string, active bool) {
	a.rerenderPanes(func(pane *terminal.TerminalPane) { pane.SetRerenderingForTask(taskID, active) })
}

func (a *App) setRerenderPending(taskID string) {
	a.rerenderPanes(func(pane *terminal.TerminalPane) { pane.SetRerenderPendingForTask(taskID) })
}

func (a *App) rerenderPanes(update func(*terminal.TerminalPane)) {
	panes := []*terminal.TerminalPane{a.agentPane}
	if a.heraPage != nil {
		panes = append(panes, a.heraPage.CoordPane(), a.heraPage.AgentPane())
	}
	for _, pane := range panes {
		if pane != nil {
			update(pane)
		}
	}
}

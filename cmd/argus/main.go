package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/store"
	"github.com/drn/argus/internal/ui"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading config: %v\n", err)
		os.Exit(1)
	}

	s := store.New()
	if err := s.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "error loading tasks: %v\n", err)
		os.Exit(1)
	}

	// Create runner — the onFinish callback sends a message to the tea.Program.
	// We need to create the program first, so we use a closure that captures p.
	var p *tea.Program
	runner := agent.NewRunner(func(taskID string, err error, stopped bool) {
		if p != nil {
			p.Send(ui.AgentFinishedMsg{TaskID: taskID, Err: err, Stopped: stopped})
		}
	})
	m := ui.NewModel(cfg, s, runner)
	p = tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

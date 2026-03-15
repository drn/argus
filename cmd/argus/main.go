package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/ui"
)

func main() {
	database, err := db.Open(db.DefaultPath())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening database: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// Create runner — the onFinish callback sends a message to the tea.Program.
	// We need to create the program first, so we use a closure that captures p.
	var p *tea.Program
	runner := agent.NewRunner(func(taskID string, err error, stopped bool, lastOutput []byte) {
		if p != nil {
			p.Send(ui.AgentFinishedMsg{TaskID: taskID, Err: err, Stopped: stopped, LastOutput: lastOutput})
		}
	})
	m := ui.NewModel(database, runner)
	p = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

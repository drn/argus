package ui

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// captureModel is a minimal tea.Model that records KeyMsgs it receives.
type captureModel struct {
	keys []tea.KeyMsg
	done bool
}

func (m captureModel) Init() tea.Cmd { return nil }

func (m captureModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		m.keys = append(m.keys, msg)
		// Quit after receiving the key so the program exits.
		return m, tea.Quit
	}
	return m, nil
}

func (m captureModel) View() string { return "" }

// TestSuperToAltFilter_Integration feeds raw Cmd+Right bytes (\x1b[1;9C)
// through a real tea.Program with the SuperToAltFilter and verifies the
// model receives an Alt+Right KeyMsg.
func TestSuperToAltFilter_Integration(t *testing.T) {
	// Feed the raw CSI sequence for Cmd+Right (Super modifier = 9)
	input := bytes.NewReader([]byte("\x1b[1;9C"))

	m := captureModel{}
	p := tea.NewProgram(m,
		tea.WithInput(input),
		tea.WithOutput(&bytes.Buffer{}), // suppress output
		tea.WithFilter(SuperToAltFilter),
	)

	done := make(chan struct{})
	var finalModel tea.Model
	var runErr error
	go func() {
		finalModel, runErr = p.Run()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("tea.Program timed out")
	}

	if runErr != nil {
		t.Fatalf("program error: %v", runErr)
	}

	cm := finalModel.(captureModel)
	if len(cm.keys) == 0 {
		t.Fatal("no KeyMsg received — filter did not convert the CSI sequence")
	}

	got := cm.keys[0]
	if got.Type != tea.KeyRight {
		t.Errorf("Type = %v, want KeyRight", got.Type)
	}
	if !got.Alt {
		t.Error("Alt = false, want true")
	}
}

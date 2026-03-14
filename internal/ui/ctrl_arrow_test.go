package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAgentView_CtrlLeftRight(t *testing.T) {
	av := newTestAgentView()

	// Initial focus should be panelAgent (center)
	if av.focus != panelAgent {
		t.Fatalf("initial focus = %d, want panelAgent(%d)", av.focus, panelAgent)
	}

	// Ctrl+left should move to panelGit
	msg := tea.KeyMsg{Type: tea.KeyCtrlLeft}
	detach := av.HandleKey(msg)
	if detach {
		t.Fatal("ctrl+left should not trigger detach")
	}
	if av.focus != panelGit {
		t.Fatalf("focus after ctrl+left = %d, want panelGit(%d)", av.focus, panelGit)
	}

	// Reset to center
	av.focus = panelAgent

	// Ctrl+right should move to panelFiles
	msg2 := tea.KeyMsg{Type: tea.KeyCtrlRight}
	detach = av.HandleKey(msg2)
	if detach {
		t.Fatal("ctrl+right should not trigger detach")
	}
	if av.focus != panelFiles {
		t.Fatalf("focus after ctrl+right = %d, want panelFiles(%d)", av.focus, panelFiles)
	}
}

func TestAgentView_CtrlLeftRightWithAlt(t *testing.T) {
	av := newTestAgentView()

	// Ctrl+left with Alt flag (urxvt sends \x1b[Od → KeyCtrlLeft + Alt)
	msg := tea.KeyMsg{Type: tea.KeyCtrlLeft, Alt: true}
	detach := av.HandleKey(msg)
	if detach {
		t.Fatal("alt+ctrl+left should not trigger detach")
	}
	if av.focus != panelGit {
		t.Fatalf("focus after alt+ctrl+left = %d, want panelGit(%d)", av.focus, panelGit)
	}

	// Reset to center
	av.focus = panelAgent

	// Ctrl+right with Alt flag
	msg2 := tea.KeyMsg{Type: tea.KeyCtrlRight, Alt: true}
	detach = av.HandleKey(msg2)
	if detach {
		t.Fatal("alt+ctrl+right should not trigger detach")
	}
	if av.focus != panelFiles {
		t.Fatalf("focus after alt+ctrl+right = %d, want panelFiles(%d)", av.focus, panelFiles)
	}
}

func TestAgentView_AltLeftRight(t *testing.T) {
	av := newTestAgentView()

	// Alt+left should move to panelGit
	msg := tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
	detach := av.HandleKey(msg)
	if detach {
		t.Fatal("alt+left should not trigger detach")
	}
	if av.focus != panelGit {
		t.Fatalf("focus after alt+left = %d, want panelGit(%d)", av.focus, panelGit)
	}

	// Reset to center
	av.focus = panelAgent

	// Alt+right should move to panelFiles
	msg2 := tea.KeyMsg{Type: tea.KeyRight, Alt: true}
	detach = av.HandleKey(msg2)
	if detach {
		t.Fatal("alt+right should not trigger detach")
	}
	if av.focus != panelFiles {
		t.Fatalf("focus after alt+right = %d, want panelFiles(%d)", av.focus, panelFiles)
	}
}

func TestAgentView_PlainLeftRight(t *testing.T) {
	av := newTestAgentView()

	// Plain left arrow should switch panels (macOS captures ctrl+left for
	// Mission Control, so plain arrows are the primary pane-switching keys).
	msg := tea.KeyMsg{Type: tea.KeyLeft}
	detach := av.HandleKey(msg)
	if detach {
		t.Fatal("plain left should not trigger detach")
	}
	if av.focus != panelGit {
		t.Fatalf("focus after plain left = %d, want panelGit(%d)", av.focus, panelGit)
	}

	// Reset to center
	av.focus = panelAgent

	// Plain right arrow should move to panelFiles
	msg2 := tea.KeyMsg{Type: tea.KeyRight}
	detach = av.HandleKey(msg2)
	if detach {
		t.Fatal("plain right should not trigger detach")
	}
	if av.focus != panelFiles {
		t.Fatalf("focus after plain right = %d, want panelFiles(%d)", av.focus, panelFiles)
	}
}

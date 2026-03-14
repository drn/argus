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

func TestAgentView_AltLeftRight_NoSwitch(t *testing.T) {
	av := newTestAgentView()

	// Alt+left should NOT switch panels (only ctrl+left does)
	msg := tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
	detach := av.HandleKey(msg)
	if detach {
		t.Fatal("alt+left should not trigger detach")
	}
	if av.focus != panelAgent {
		t.Fatalf("focus after alt+left = %d, want panelAgent(%d) (should not switch)", av.focus, panelAgent)
	}

	// Alt+right should NOT switch panels
	msg2 := tea.KeyMsg{Type: tea.KeyRight, Alt: true}
	detach = av.HandleKey(msg2)
	if detach {
		t.Fatal("alt+right should not trigger detach")
	}
	if av.focus != panelAgent {
		t.Fatalf("focus after alt+right = %d, want panelAgent(%d) (should not switch)", av.focus, panelAgent)
	}
}

func TestAgentView_PlainLeftRight_NoSwitch(t *testing.T) {
	av := newTestAgentView()

	// Plain left arrow should NOT switch panels (only ctrl+left does)
	msg := tea.KeyMsg{Type: tea.KeyLeft}
	detach := av.HandleKey(msg)
	if detach {
		t.Fatal("plain left should not trigger detach")
	}
	if av.focus != panelAgent {
		t.Fatalf("focus after plain left = %d, want panelAgent(%d) (should not switch)", av.focus, panelAgent)
	}

	// Plain right arrow should NOT switch panels
	msg2 := tea.KeyMsg{Type: tea.KeyRight}
	detach = av.HandleKey(msg2)
	if detach {
		t.Fatal("plain right should not trigger detach")
	}
	if av.focus != panelAgent {
		t.Fatalf("focus after plain right = %d, want panelAgent(%d) (should not switch)", av.focus, panelAgent)
	}
}

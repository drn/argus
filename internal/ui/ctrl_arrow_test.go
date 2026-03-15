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

	// Ctrl+right should move to panelFiles
	msg := tea.KeyMsg{Type: tea.KeyCtrlRight}
	detach, _ := av.HandleKey(msg)
	if detach {
		t.Fatal("ctrl+right should not trigger detach")
	}
	if av.focus != panelFiles {
		t.Fatalf("focus after ctrl+right = %d, want panelFiles(%d)", av.focus, panelFiles)
	}

	// Ctrl+left should move back to panelAgent
	msg2 := tea.KeyMsg{Type: tea.KeyCtrlLeft}
	detach, _ = av.HandleKey(msg2)
	if detach {
		t.Fatal("ctrl+left should not trigger detach")
	}
	if av.focus != panelAgent {
		t.Fatalf("focus after ctrl+left = %d, want panelAgent(%d)", av.focus, panelAgent)
	}
}

func TestAgentView_CtrlLeft_DoesNotFocusGitPanel(t *testing.T) {
	av := newTestAgentView()

	// From panelAgent, ctrl+left should NOT move to panelGit
	msg := tea.KeyMsg{Type: tea.KeyCtrlLeft}
	av.HandleKey(msg)
	if av.focus != panelAgent {
		t.Fatalf("focus after ctrl+left from panelAgent = %d, want panelAgent(%d) (git panel not focusable)", av.focus, panelAgent)
	}
}

func TestAgentView_AltLeftRight_NoSwitch(t *testing.T) {
	av := newTestAgentView()

	// Alt+left should NOT switch panels (old binding removed)
	msg := tea.KeyMsg{Type: tea.KeyLeft, Alt: true}
	av.HandleKey(msg)
	if av.focus != panelAgent {
		t.Fatalf("focus after alt+left = %d, want panelAgent(%d) (should not switch)", av.focus, panelAgent)
	}

	// Alt+right should NOT switch panels
	msg2 := tea.KeyMsg{Type: tea.KeyRight, Alt: true}
	av.HandleKey(msg2)
	if av.focus != panelAgent {
		t.Fatalf("focus after alt+right = %d, want panelAgent(%d) (should not switch)", av.focus, panelAgent)
	}
}

func TestAgentView_PlainLeftRight_NoSwitch(t *testing.T) {
	av := newTestAgentView()

	// Plain left arrow should NOT switch panels
	msg := tea.KeyMsg{Type: tea.KeyLeft}
	av.HandleKey(msg)
	if av.focus != panelAgent {
		t.Fatalf("focus after plain left = %d, want panelAgent(%d) (should not switch)", av.focus, panelAgent)
	}

	// Plain right arrow should NOT switch panels
	msg2 := tea.KeyMsg{Type: tea.KeyRight}
	av.HandleKey(msg2)
	if av.focus != panelAgent {
		t.Fatalf("focus after plain right = %d, want panelAgent(%d) (should not switch)", av.focus, panelAgent)
	}
}

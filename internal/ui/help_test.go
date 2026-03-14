package ui

import (
	"strings"
	"testing"
)

func TestHelpView_ContainsKeybindings(t *testing.T) {
	hv := NewHelpView(DefaultKeyMap(), DefaultTheme())
	view := hv.View()

	if !strings.Contains(view, "Keybindings") {
		t.Error("help view should contain 'Keybindings' title")
	}
	if !strings.Contains(view, "new task") {
		t.Error("help view should contain 'new task' key description")
	}
	if !strings.Contains(view, "quit") {
		t.Error("help view should contain 'quit' key description")
	}
	if !strings.Contains(view, "attach") {
		t.Error("help view should contain 'attach' key description")
	}
	if !strings.Contains(view, "Press any key to close") {
		t.Error("help view should contain close hint")
	}
}

func TestHelpView_NonEmpty(t *testing.T) {
	hv := NewHelpView(DefaultKeyMap(), DefaultTheme())
	view := hv.View()
	if view == "" {
		t.Error("help view should not be empty")
	}
}

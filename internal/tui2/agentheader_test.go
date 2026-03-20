package tui2

import "testing"

func TestAgentHeader_SetTaskName(t *testing.T) {
	h := NewAgentHeader()

	if h.taskName != "" {
		t.Errorf("initial taskName = %q, want empty", h.taskName)
	}

	h.SetTaskName("fix-login-bug")
	if h.taskName != "fix-login-bug" {
		t.Errorf("taskName = %q, want %q", h.taskName, "fix-login-bug")
	}

	h.SetTaskName("")
	if h.taskName != "" {
		t.Errorf("taskName = %q, want empty", h.taskName)
	}
}

package widget

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

func TestAgentHeader_ClipboardHint(t *testing.T) {
	h := NewAgentHeader()
	if h.ClipboardHint() {
		t.Error("default clipboard hint should be off")
	}
	h.SetClipboardHint(true)
	if !h.ClipboardHint() {
		t.Error("SetClipboardHint(true) should turn it on")
	}
	h.SetClipboardHint(false)
	if h.ClipboardHint() {
		t.Error("SetClipboardHint(false) should turn it off")
	}
}

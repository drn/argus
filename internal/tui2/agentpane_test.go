package tui2

import "testing"

func TestAgentPane_SetSession(t *testing.T) {
	ap := NewAgentPane()
	if ap.session != nil {
		t.Error("initial session should be nil")
	}

	ap.SetTaskID("task-1")
	if ap.taskID != "task-1" {
		t.Errorf("taskID = %q, want task-1", ap.taskID)
	}

	ap.SetFocused(true)
	if !ap.focused {
		t.Error("should be focused")
	}
}

func TestSplitLines(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		maxWidth int
		want     int // expected number of lines
	}{
		{"empty", nil, 80, 0},
		{"single line", []byte("hello"), 80, 1},
		{"two lines", []byte("hello\nworld"), 80, 2},
		{"wrap", []byte("abcdefgh"), 4, 2},
		{"cr-lf", []byte("hello\r\nworld"), 80, 2},
		{"ansi-stripped", []byte("\x1b[32mhello\x1b[0m\nworld"), 80, 2},
		{"control-chars", []byte("he\x07llo"), 80, 1},
	}

	// Verify ANSI escapes are actually removed from content
	ansiLines := splitLines([]byte("\x1b[32mgreen\x1b[0m text"), 80)
	if len(ansiLines) != 1 || ansiLines[0] != "green text" {
		t.Errorf("ANSI not stripped: got %q", ansiLines)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := splitLines(tt.data, tt.maxWidth)
			if len(lines) != tt.want {
				t.Errorf("splitLines(%q, %d) = %d lines, want %d", tt.data, tt.maxWidth, len(lines), tt.want)
			}
		})
	}
}

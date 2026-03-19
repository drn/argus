package tui2

import "testing"

func TestGitPanel_SetStatus(t *testing.T) {
	gp := NewGitPanel()
	if gp.loaded {
		t.Error("should not be loaded initially")
	}

	gp.SetStatus(" M file.go\n A new.go", " file.go | 2 +-", "M\tfile.go")
	if !gp.loaded {
		t.Error("should be loaded after SetStatus")
	}
	if len(gp.statusLines) != 2 {
		t.Errorf("statusLines = %d, want 2", len(gp.statusLines))
	}
	if len(gp.diffLines) != 1 {
		t.Errorf("diffLines = %d, want 1", len(gp.diffLines))
	}
}

func TestGitPanel_Clear(t *testing.T) {
	gp := NewGitPanel()
	gp.SetStatus("M file.go", "", "")
	gp.Clear()

	if gp.loaded {
		t.Error("should not be loaded after Clear")
	}
	if len(gp.statusLines) != 0 {
		t.Error("statusLines should be empty after Clear")
	}
}

func TestSplitNonEmpty(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"hello\nworld", 2},
		{"hello\n\nworld\n", 2},
		{"\n\n\n", 0},
		{"single", 1},
	}
	for _, tt := range tests {
		got := splitNonEmpty(tt.input)
		if len(got) != tt.want {
			t.Errorf("splitNonEmpty(%q) = %d lines, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("hello world", 5); got != "hell…" {
		t.Errorf("truncate long = %q", got)
	}
	if got := truncate("hi", 2); got != "hi" {
		t.Errorf("truncate exact = %q", got)
	}
}

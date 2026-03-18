package agentview

import (
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	s := New()
	if s.Focus != PanelTerminal {
		t.Errorf("New().Focus = %d, want PanelTerminal", s.Focus)
	}
	if !s.Diff.Split {
		t.Error("New().Diff.Split should default to true")
	}
	if s.TaskID != "" {
		t.Error("New().TaskID should be empty")
	}
}

func TestReset(t *testing.T) {
	s := New()
	s.TaskID = "old"
	s.PRURL = "https://example.com"
	s.ScrollOffset = 10
	s.LastOutput = []byte("data")
	s.WorktreeDir = "/tmp/wt"
	s.Diff.Active = true
	s.Diff.FileName = "foo.go"

	s.Reset("new-task", "my task")

	if s.TaskID != "new-task" {
		t.Errorf("TaskID = %q, want %q", s.TaskID, "new-task")
	}
	if s.TaskName != "my task" {
		t.Errorf("TaskName = %q, want %q", s.TaskName, "my task")
	}
	if s.PRURL != "" {
		t.Errorf("PRURL = %q, want empty", s.PRURL)
	}
	if s.Focus != PanelTerminal {
		t.Errorf("Focus = %d, want PanelTerminal", s.Focus)
	}
	if s.ScrollOffset != 0 {
		t.Errorf("ScrollOffset = %d, want 0", s.ScrollOffset)
	}
	if s.LastOutput != nil {
		t.Error("LastOutput should be nil")
	}
	if s.WorktreeDir != "" {
		t.Errorf("WorktreeDir = %q, want empty", s.WorktreeDir)
	}
	if s.Diff.Active {
		t.Error("Diff.Active should be false after reset")
	}
	if !s.Diff.Split {
		t.Error("Diff.Split should be true after reset")
	}
}

func TestNeedsGitRefresh(t *testing.T) {
	tests := []struct {
		name     string
		taskID   string
		lastTime time.Time
		want     bool
	}{
		{"empty task", "", time.Time{}, false},
		{"never refreshed", "task-1", time.Time{}, true},
		{"recent refresh", "task-1", time.Now(), false},
		{"stale refresh", "task-1", time.Now().Add(-4 * time.Second), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New()
			s.TaskID = tt.taskID
			s.LastGitRefresh = tt.lastTime
			if got := s.NeedsGitRefresh(); got != tt.want {
				t.Errorf("NeedsGitRefresh() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFocusLeftRight(t *testing.T) {
	s := New()
	if s.Focus != PanelTerminal {
		t.Fatal("expected PanelTerminal")
	}

	// Left from terminal is a no-op
	s.FocusLeft()
	if s.Focus != PanelTerminal {
		t.Error("FocusLeft from PanelTerminal should stay")
	}

	// Right goes to files
	s.FocusRight()
	if s.Focus != PanelFiles {
		t.Error("FocusRight from PanelTerminal should go to PanelFiles")
	}

	// Right from files is a no-op
	s.FocusRight()
	if s.Focus != PanelFiles {
		t.Error("FocusRight from PanelFiles should stay")
	}

	// Left goes back to terminal
	s.FocusLeft()
	if s.Focus != PanelTerminal {
		t.Error("FocusLeft from PanelFiles should go to PanelTerminal")
	}
}

func TestScrollUpDown(t *testing.T) {
	s := New()

	// Scroll up with no cache (maxLines=0) — should grow freely
	s.ScrollUp(5, 0, 10)
	if s.ScrollOffset != 5 {
		t.Errorf("ScrollOffset = %d, want 5", s.ScrollOffset)
	}

	// Scroll up with cache — should clamp
	s.ScrollOffset = 0
	s.ScrollUp(100, 20, 10)
	if s.ScrollOffset != 10 {
		t.Errorf("ScrollOffset = %d, want 10 (clamped to maxLines-dispH)", s.ScrollOffset)
	}

	// Scroll down
	s.ScrollDown(5)
	if s.ScrollOffset != 5 {
		t.Errorf("ScrollOffset = %d, want 5", s.ScrollOffset)
	}

	// Scroll down past zero
	s.ScrollDown(100)
	if s.ScrollOffset != 0 {
		t.Errorf("ScrollOffset = %d, want 0", s.ScrollOffset)
	}
}

func TestScrollUpSmallContent(t *testing.T) {
	s := New()
	// When content fits in display, max scroll is 0
	s.ScrollUp(5, 5, 10)
	if s.ScrollOffset != 0 {
		t.Errorf("ScrollOffset = %d, want 0 (content fits in display)", s.ScrollOffset)
	}
}

func TestDiffScroll(t *testing.T) {
	s := New()
	s.Diff.Active = true

	// Scroll down with bounds
	s.DiffScrollDown(5, 20, 10)
	if s.Diff.ScrollOff != 5 {
		t.Errorf("Diff.ScrollOff = %d, want 5", s.Diff.ScrollOff)
	}

	// Clamp at max
	s.DiffScrollDown(100, 20, 10)
	if s.Diff.ScrollOff != 10 {
		t.Errorf("Diff.ScrollOff = %d, want 10", s.Diff.ScrollOff)
	}

	// Scroll up
	s.DiffScrollUp(3)
	if s.Diff.ScrollOff != 7 {
		t.Errorf("Diff.ScrollOff = %d, want 7", s.Diff.ScrollOff)
	}

	// Scroll up past zero
	s.DiffScrollUp(100)
	if s.Diff.ScrollOff != 0 {
		t.Errorf("Diff.ScrollOff = %d, want 0", s.Diff.ScrollOff)
	}
}

func TestEnterExitDiff(t *testing.T) {
	s := New()

	s.EnterDiff("main.go")
	if !s.Diff.Active {
		t.Error("EnterDiff should set Active=true")
	}
	if s.Diff.FileName != "main.go" {
		t.Errorf("FileName = %q, want %q", s.Diff.FileName, "main.go")
	}
	if s.Diff.ScrollOff != 0 {
		t.Errorf("ScrollOff = %d, want 0", s.Diff.ScrollOff)
	}

	s.ExitDiff()
	if s.Diff.Active {
		t.Error("ExitDiff should set Active=false")
	}
	if s.Diff.FileName != "" {
		t.Errorf("FileName = %q, want empty", s.Diff.FileName)
	}
	if !s.Diff.Split {
		t.Error("ExitDiff should preserve Split default (true)")
	}
}

func TestExitDiffPreservesSplit(t *testing.T) {
	s := New()
	s.EnterDiff("main.go")

	// User toggles to unified mode
	s.Diff.Split = false

	s.ExitDiff()
	if s.Diff.Split {
		t.Error("ExitDiff should preserve Split=false (user preference)")
	}

	// Re-enter diff — Split should still be false
	s.EnterDiff("other.go")
	if s.Diff.Split {
		t.Error("EnterDiff should preserve Split=false across opens")
	}
}

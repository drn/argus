package ui

import (
	"os"
	"strings"
	"testing"
)

func TestGitStatusUpdate(t *testing.T) {
	gs := NewGitStatus(DefaultTheme())
	gs.SetSize(40, 10)
	gs.SetTask("task-1")

	msg := GitStatusRefreshMsg{
		TaskID:      "task-1",
		Status:      " M file.go",
		Diff:        " file.go | 2 +-",
		BranchDiff:  " file.go | 10 +++++++---",
		BranchFiles: "M\tfile.go",
	}
	gs.Update(msg)

	if !gs.loaded {
		t.Error("expected loaded to be true after Update")
	}
	if gs.statusText != msg.Status {
		t.Errorf("statusText = %q, want %q", gs.statusText, msg.Status)
	}
	if gs.diffText != msg.Diff {
		t.Errorf("diffText = %q, want %q", gs.diffText, msg.Diff)
	}
	if gs.branchDiffText != msg.BranchDiff {
		t.Errorf("branchDiffText = %q, want %q", gs.branchDiffText, msg.BranchDiff)
	}
}

func TestGitStatusUpdateIgnoresMismatchedTask(t *testing.T) {
	gs := NewGitStatus(DefaultTheme())
	gs.SetTask("task-1")

	gs.Update(GitStatusRefreshMsg{
		TaskID: "task-2",
		Status: " M file.go",
	})

	if gs.loaded {
		t.Error("should not update for mismatched task ID")
	}
}

func TestGitStatusSetTaskClears(t *testing.T) {
	gs := NewGitStatus(DefaultTheme())
	gs.SetTask("task-1")
	gs.Update(GitStatusRefreshMsg{
		TaskID:      "task-1",
		Status:      " M file.go",
		BranchDiff:  " file.go | 5 ++---",
		BranchFiles: "M\tfile.go",
	})

	gs.SetTask("task-2")

	if gs.loaded {
		t.Error("expected loaded to be false after task change")
	}
	if gs.branchDiffText != "" {
		t.Error("expected branchDiffText cleared on task change")
	}
}

func TestGitStatusViewShowsBranchDiff(t *testing.T) {
	gs := NewGitStatus(DefaultTheme())
	gs.SetSize(60, 20)
	gs.SetTask("task-1")

	// Simulate committed changes only (no uncommitted)
	gs.Update(GitStatusRefreshMsg{
		TaskID:     "task-1",
		BranchDiff: " file.go | 10 +++++++---",
	})

	view := gs.View()
	if view == "" {
		t.Error("expected non-empty view when BranchDiff is set")
	}
	// Should not show "Clean — no changes"
	if contains(view, "Clean") {
		t.Error("view should not say 'Clean' when BranchDiff has content")
	}
}

func TestGitStatusViewClean(t *testing.T) {
	gs := NewGitStatus(DefaultTheme())
	gs.SetSize(40, 10)
	gs.SetTask("task-1")
	gs.Update(GitStatusRefreshMsg{TaskID: "task-1"})

	view := gs.View()
	if !contains(view, "Clean") {
		t.Error("expected 'Clean' in view when all fields empty")
	}
}

func TestParseGitDiffNameStatus(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect []ChangedFile
	}{
		{
			name:   "empty",
			input:  "",
			expect: nil,
		},
		{
			name:  "single modified file",
			input: "M\tinternal/ui/root.go",
			expect: []ChangedFile{
				{Status: "M", Path: "internal/ui/root.go"},
			},
		},
		{
			name:  "multiple files",
			input: "M\tfile1.go\nA\tfile2.go\nD\tfile3.go",
			expect: []ChangedFile{
				{Status: "M", Path: "file1.go"},
				{Status: "A", Path: "file2.go"},
				{Status: "D", Path: "file3.go"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseGitDiffNameStatus(tt.input)
			if len(got) != len(tt.expect) {
				t.Fatalf("got %d files, want %d", len(got), len(tt.expect))
			}
			for i, f := range got {
				if f.Status != tt.expect[i].Status || f.Path != tt.expect[i].Path {
					t.Errorf("file[%d] = %+v, want %+v", i, f, tt.expect[i])
				}
			}
		})
	}
}

func TestFindWorktreeByName(t *testing.T) {
	// Create a temp directory structure mimicking .claude/worktrees/
	dir := t.TempDir()

	// .claude/worktrees/argus/task-one/.git
	taskOneDir := dir + "/argus/task-one"
	if err := os.MkdirAll(taskOneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskOneDir+"/.git", []byte("gitdir: ..."), 0o644); err != nil {
		t.Fatal(err)
	}

	// .claude/worktrees/argus/task-two/.git
	taskTwoDir := dir + "/argus/task-two"
	if err := os.MkdirAll(taskTwoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(taskTwoDir+"/.git", []byte("gitdir: ..."), 0o644); err != nil {
		t.Fatal(err)
	}

	// Should find the correct worktree by name
	got := findWorktreeByName(dir, "task-one")
	if got != taskOneDir {
		t.Errorf("findWorktreeByName(dir, 'task-one') = %q, want %q", got, taskOneDir)
	}

	got = findWorktreeByName(dir, "task-two")
	if got != taskTwoDir {
		t.Errorf("findWorktreeByName(dir, 'task-two') = %q, want %q", got, taskTwoDir)
	}

	// Non-existent task returns empty
	got = findWorktreeByName(dir, "no-such-task")
	if got != "" {
		t.Errorf("findWorktreeByName(dir, 'no-such-task') = %q, want empty", got)
	}
}

func TestGitStatus_NeedsRefresh_NoTask(t *testing.T) {
	gs := NewGitStatus(DefaultTheme())
	if gs.NeedsRefresh() {
		t.Error("NeedsRefresh should be false with no task")
	}
}

func TestGitStatus_NeedsRefresh_WithTask(t *testing.T) {
	gs := NewGitStatus(DefaultTheme())
	gs.SetTask("task-1")
	// lastRefresh is zero, so time.Since is large
	if !gs.NeedsRefresh() {
		t.Error("NeedsRefresh should be true for fresh task")
	}
}

func TestGitStatus_SetFocused(t *testing.T) {
	gs := NewGitStatus(DefaultTheme())
	gs.SetSize(40, 10)
	gs.SetTask("task-1")
	gs.Update(GitStatusRefreshMsg{TaskID: "task-1", Status: " M file.go"})

	gs.SetFocused(true)
	focused := gs.View()
	gs.SetFocused(false)
	unfocused := gs.View()

	// Both should render, and they should be different (border color differs)
	if focused == "" || unfocused == "" {
		t.Error("both views should be non-empty")
	}
}

func TestGitStatus_ColorizeStatus(t *testing.T) {
	gs := NewGitStatus(DefaultTheme())
	gs.SetSize(60, 20)
	gs.SetTask("task-1")
	gs.Update(GitStatusRefreshMsg{
		TaskID: "task-1",
		Status: " M modified.go\n A added.go\n?? untracked.go\n D deleted.go",
	})

	view := gs.View()
	if view == "" {
		t.Error("expected non-empty view with status lines")
	}
}

func TestGitStatus_ViewNoTask(t *testing.T) {
	gs := NewGitStatus(DefaultTheme())
	gs.SetSize(40, 10)
	view := gs.View()
	if !contains(view, "No worktree") {
		t.Error("expected 'No worktree' when no task set")
	}
}

func TestGitStatus_ViewLoading(t *testing.T) {
	gs := NewGitStatus(DefaultTheme())
	gs.SetSize(40, 10)
	gs.SetTask("task-1")
	// loaded is false, no update called
	view := gs.View()
	if !contains(view, "Loading") {
		t.Error("expected 'Loading...' before first update")
	}
}

func TestGitStatus_TruncateLines(t *testing.T) {
	gs := NewGitStatus(DefaultTheme())
	text := "short\nthis is a very long line that should be truncated at some point eventually\nline3\nline4\nline5"
	result := gs.truncateLines(text, 20, 3)
	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

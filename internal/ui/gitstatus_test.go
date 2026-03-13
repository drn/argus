package ui

import (
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

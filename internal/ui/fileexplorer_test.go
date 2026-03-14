package ui

import (
	"strings"
	"testing"
)

func TestParseGitStatus_Empty(t *testing.T) {
	files := ParseGitStatus("")
	if files != nil {
		t.Errorf("expected nil for empty input, got %v", files)
	}
}

func TestParseGitStatus_SingleModified(t *testing.T) {
	files := ParseGitStatus(" M internal/ui/root.go\n")
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Status != "M" {
		t.Errorf("expected status 'M', got %q", files[0].Status)
	}
	if files[0].Path != "internal/ui/root.go" {
		t.Errorf("expected path 'internal/ui/root.go', got %q", files[0].Path)
	}
}

func TestParseGitStatus_MultipleStatuses(t *testing.T) {
	input := " M file1.go\n A file2.go\n D file3.go\n?? file4.go\n"
	files := ParseGitStatus(input)
	if len(files) != 4 {
		t.Fatalf("expected 4 files, got %d", len(files))
	}
	expected := []struct {
		status, path string
	}{
		{"M", "file1.go"},
		{"A", "file2.go"},
		{"D", "file3.go"},
		{"??", "file4.go"},
	}
	for i, e := range expected {
		if files[i].Status != e.status {
			t.Errorf("file[%d].Status = %q, want %q", i, files[i].Status, e.status)
		}
		if files[i].Path != e.path {
			t.Errorf("file[%d].Path = %q, want %q", i, files[i].Path, e.path)
		}
	}
}

func TestParseGitStatus_ShortLines(t *testing.T) {
	// Lines shorter than 4 chars should be skipped
	files := ParseGitStatus("AB\n M ok.go\n")
	if len(files) != 1 {
		t.Fatalf("expected 1 file (short lines skipped), got %d", len(files))
	}
	if files[0].Path != "ok.go" {
		t.Errorf("expected path 'ok.go', got %q", files[0].Path)
	}
}

func TestParseGitDiffNameStatus_WithTrailingNewline(t *testing.T) {
	input := "M\tfile1.go\nA\tfile2.go\n"
	files := ParseGitDiffNameStatus(input)
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	if files[0].Status != "M" || files[0].Path != "file1.go" {
		t.Errorf("file[0] = %+v, want M/file1.go", files[0])
	}
	if files[1].Status != "A" || files[1].Path != "file2.go" {
		t.Errorf("file[1] = %+v, want A/file2.go", files[1])
	}
}

func TestParseGitDiffNameStatus_NoTab(t *testing.T) {
	// Lines without tabs should be skipped
	files := ParseGitDiffNameStatus("no tab here\nM\tgood.go\n")
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
}

func TestNewFileExplorer(t *testing.T) {
	fe := NewFileExplorer(DefaultTheme())
	if fe.scroll.Cursor() != 0 {
		t.Errorf("initial cursor = %d, want 0", fe.scroll.Cursor())
	}
	if len(fe.files) != 0 {
		t.Errorf("initial files should be empty")
	}
}

func TestFileExplorer_SetSize(t *testing.T) {
	fe := NewFileExplorer(DefaultTheme())
	fe.SetSize(30, 20)
	if fe.width != 30 || fe.height != 20 {
		t.Errorf("SetSize(30,20) gave width=%d height=%d", fe.width, fe.height)
	}
}

func TestFileExplorer_SetFiles(t *testing.T) {
	fe := NewFileExplorer(DefaultTheme())
	fe.scroll.cursor = 5
	files := []ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "A", Path: "b.go"},
	}
	fe.SetFiles(files)
	if len(fe.files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(fe.files))
	}
	if fe.scroll.Cursor() != 1 {
		t.Errorf("cursor should be clamped to 1, got %d", fe.scroll.Cursor())
	}
}

func TestFileExplorer_SetFiles_Empty(t *testing.T) {
	fe := NewFileExplorer(DefaultTheme())
	fe.scroll.cursor = 3
	fe.SetFiles(nil)
	if fe.scroll.Cursor() != 0 {
		t.Errorf("cursor should be 0 for empty files, got %d", fe.scroll.Cursor())
	}
}

func TestFileExplorer_CursorUpDown(t *testing.T) {
	fe := NewFileExplorer(DefaultTheme())
	fe.SetSize(30, 20)
	fe.SetFiles([]ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "M", Path: "b.go"},
		{Status: "M", Path: "c.go"},
	})

	fe.CursorDown()
	if fe.scroll.Cursor() != 1 {
		t.Errorf("cursor after down = %d, want 1", fe.scroll.Cursor())
	}

	fe.CursorDown()
	if fe.scroll.Cursor() != 2 {
		t.Errorf("cursor after down x2 = %d, want 2", fe.scroll.Cursor())
	}

	fe.CursorDown()
	if fe.scroll.Cursor() != 2 {
		t.Errorf("cursor after down at end = %d, want 2", fe.scroll.Cursor())
	}

	fe.CursorUp()
	if fe.scroll.Cursor() != 1 {
		t.Errorf("cursor after up = %d, want 1", fe.scroll.Cursor())
	}

	fe.CursorUp()
	if fe.scroll.Cursor() != 0 {
		t.Errorf("cursor after up x2 = %d, want 0", fe.scroll.Cursor())
	}

	fe.CursorUp()
	if fe.scroll.Cursor() != 0 {
		t.Errorf("cursor after up at start = %d, want 0", fe.scroll.Cursor())
	}
}

func TestFileExplorer_ViewFocused(t *testing.T) {
	fe := NewFileExplorer(DefaultTheme())
	fe.SetSize(40, 20)
	fe.SetFiles([]ChangedFile{
		{Status: "M", Path: "modified.go"},
		{Status: "A", Path: "added.go"},
	})

	view := fe.View(true)
	if !strings.Contains(view, "FILES") {
		t.Error("focused view should contain 'FILES'")
	}
	if !strings.Contains(view, "(2)") {
		t.Error("focused view should show file count")
	}
}

func TestFileExplorer_ViewUnfocused(t *testing.T) {
	fe := NewFileExplorer(DefaultTheme())
	fe.SetSize(40, 20)
	fe.SetFiles([]ChangedFile{
		{Status: "M", Path: "file.go"},
	})

	view := fe.View(false)
	if !strings.Contains(view, "FILES") {
		t.Error("unfocused view should still contain 'FILES'")
	}
}

func TestFileExplorer_ViewEmpty(t *testing.T) {
	fe := NewFileExplorer(DefaultTheme())
	fe.SetSize(40, 20)
	fe.SetFiles(nil)

	view := fe.View(false)
	if !strings.Contains(view, "No changes") {
		t.Error("empty file view should show 'No changes'")
	}
}

func TestFileExplorer_StatusIcon(t *testing.T) {
	fe := NewFileExplorer(DefaultTheme())
	tests := []struct {
		status, want string
	}{
		{"M", "M"},
		{"MM", "M"},
		{"A", "A"},
		{"D", "D"},
		{"??", "?"},
		{"R", "R"},
		{"X", "X"}, // unknown status returns as-is
	}
	for _, tt := range tests {
		got := fe.statusIcon(tt.status)
		if got != tt.want {
			t.Errorf("statusIcon(%q) = %q, want %q", tt.status, got, tt.want)
		}
	}
}

func TestFileExplorer_StatusStyle(t *testing.T) {
	theme := DefaultTheme()
	fe := NewFileExplorer(theme)

	// Just verify different statuses return non-zero styles without panicking
	statuses := []string{"M", "MM", "A", "??", "D", "X"}
	for _, s := range statuses {
		style := fe.statusStyle(s)
		// Render something to ensure the style works
		result := style.Render("test")
		if result == "" {
			t.Errorf("statusStyle(%q) rendered empty string", s)
		}
	}
}

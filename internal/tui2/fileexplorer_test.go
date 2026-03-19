package tui2

import (
	"testing"

	"github.com/drn/argus/internal/ui"
)

func TestFilePanel_SetFiles(t *testing.T) {
	fp := NewFilePanel()
	files := []ui.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "A", Path: "b.go"},
	}
	fp.SetFiles(files)

	if fp.FileCount() != 2 {
		t.Errorf("FileCount = %d, want 2", fp.FileCount())
	}
	if f := fp.SelectedFile(); f == nil || f.Path != "a.go" {
		t.Errorf("initial selected file = %v", f)
	}
}

func TestFilePanel_CursorNavigation(t *testing.T) {
	fp := NewFilePanel()
	// Simulate having inner rect
	fp.Box.SetRect(0, 0, 40, 20)
	files := []ui.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "A", Path: "b.go"},
		{Status: "D", Path: "c.go"},
	}
	fp.SetFiles(files)

	fp.CursorDown()
	if f := fp.SelectedFile(); f == nil || f.Path != "b.go" {
		t.Errorf("after CursorDown: selected = %v", f)
	}

	fp.CursorUp()
	if f := fp.SelectedFile(); f == nil || f.Path != "a.go" {
		t.Errorf("after CursorUp: selected = %v", f)
	}
}

func TestFilePanel_DirExpansion(t *testing.T) {
	fp := NewFilePanel()
	fp.Box.SetRect(0, 0, 40, 20)
	files := []ui.ChangedFile{
		{Status: "M", Path: "src/", IsDir: true},
		{Status: "A", Path: "b.go"},
	}
	fp.SetFiles(files)

	// Moving to dir auto-expands it and returns dir path for fetch
	dir := fp.CursorDown()
	// First cursor should be on src/ (already expanded or needs fetch)
	_ = dir

	fp.SetDirChildren("src/", []ui.ChangedFile{
		{Status: "M", Path: "src/main.go"},
	})

	// Now there should be rows for: src/, src/main.go, b.go
	if len(fp.rows) < 2 {
		t.Errorf("expected at least 2 rows after expansion, got %d", len(fp.rows))
	}
}

func TestFilePanel_Empty(t *testing.T) {
	fp := NewFilePanel()
	fp.SetFiles(nil)
	if fp.FileCount() != 0 {
		t.Error("empty panel should have 0 files")
	}
	if fp.SelectedFile() != nil {
		t.Error("empty panel should return nil selected file")
	}
}

func TestFilePanel_StatusIcons(t *testing.T) {
	fp := NewFilePanel()
	tests := []struct {
		status string
		icon   rune
	}{
		{"M", 'M'},
		{"A", 'A'},
		{"D", 'D'},
		{"??", '?'},
		{"R", 'R'},
		{"X", '·'},
	}
	for _, tt := range tests {
		icon, _ := fp.statusIcon(tt.status)
		if icon != tt.icon {
			t.Errorf("statusIcon(%q) = %c, want %c", tt.status, icon, tt.icon)
		}
	}
}

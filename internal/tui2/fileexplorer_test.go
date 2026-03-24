package tui2

import (
	"testing"

	"github.com/drn/argus/internal/gitutil"
)

func TestFilePanel_SetFiles(t *testing.T) {
	fp := NewFilePanel()
	files := []gitutil.ChangedFile{
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
	files := []gitutil.ChangedFile{
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
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "M", Path: "src/", IsDir: true},
		{Status: "A", Path: "b.go"},
	}
	fp.SetFiles(files)

	// Pre-populate children so skip-to-file can land on a child
	fp.SetDirChildren("src/", []gitutil.ChangedFile{
		{Status: "M", Path: "src/main.go"},
	})

	// From a.go, CursorDown hits src/ dir → autoExpand expands it → skipToFile lands on first child
	fp.CursorDown()

	// Rows: a.go, src/, src/main.go, b.go — cursor should skip src/ dir and land on src/main.go
	if len(fp.rows) != 4 {
		t.Errorf("expected 4 rows after expansion, got %d", len(fp.rows))
	}
	if f := fp.SelectedFile(); f == nil || f.Path != "src/main.go" {
		t.Errorf("cursor should skip dir and land on src/main.go, got %v", f)
	}
}

func TestFilePanel_SkipDirDown(t *testing.T) {
	fp := NewFilePanel()
	fp.Box.SetRect(0, 0, 40, 20)
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "M", Path: "pkg/", IsDir: true},
		{Status: "A", Path: "b.go"},
	}
	fp.SetFiles(files)

	// Start on a.go, move down — should skip pkg/ dir and land on b.go
	fp.CursorDown()
	if f := fp.SelectedFile(); f == nil || f.Path != "b.go" {
		t.Errorf("should skip dir, got %v", f)
	}
}

func TestFilePanel_SkipDirUp(t *testing.T) {
	fp := NewFilePanel()
	fp.Box.SetRect(0, 0, 40, 20)
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "M", Path: "pkg/", IsDir: true},
		{Status: "A", Path: "b.go"},
	}
	fp.SetFiles(files)

	// Move cursor to b.go first
	fp.CursorDown()
	if f := fp.SelectedFile(); f == nil || f.Path != "b.go" {
		t.Fatalf("setup: expected b.go, got %v", f)
	}

	// Move up — should skip pkg/ dir and land on a.go
	fp.CursorUp()
	if f := fp.SelectedFile(); f == nil || f.Path != "a.go" {
		t.Errorf("should skip dir going up, got %v", f)
	}
}

func TestFilePanel_AllDirsNoSkip(t *testing.T) {
	fp := NewFilePanel()
	fp.Box.SetRect(0, 0, 40, 20)
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "src/", IsDir: true},
		{Status: "M", Path: "pkg/", IsDir: true},
	}
	fp.SetFiles(files)

	// With only dirs, cursor moves normally (skipToFile preserves position) and stays in bounds
	fp.CursorDown()
	if fp.cursor < 0 || fp.cursor >= len(fp.rows) {
		t.Errorf("cursor out of bounds: %d (rows: %d)", fp.cursor, len(fp.rows))
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

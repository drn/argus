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

func TestFilePanel_SetFilesAutoExpandDir(t *testing.T) {
	fp := NewFilePanel()
	fp.Box.SetRect(0, 0, 40, 20)

	t.Run("dir at cursor with cached children skips to first file", func(t *testing.T) {
		// Pre-cache children so autoExpand during SetFiles can skip to the file.
		fp.dirChildren["src/"] = []gitutil.ChangedFile{
			{Status: "M", Path: "src/main.go"},
			{Status: "A", Path: "src/util.go"},
		}
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "src/", IsDir: true},
			{Status: "A", Path: "b.go"},
		}
		fp.SetFiles(files)
		if f := fp.SelectedFile(); f == nil || f.Path != "src/main.go" {
			t.Errorf("expected cursor on src/main.go, got %v", f)
		}
	})

	t.Run("no dirs returns empty fetch", func(t *testing.T) {
		fp3 := NewFilePanel()
		fp3.Box.SetRect(0, 0, 40, 20)
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "a.go"},
			{Status: "A", Path: "b.go"},
		}
		fetch := fp3.SetFiles(files)
		if fetch != "" {
			t.Errorf("expected empty fetch for no-dir list, got %q", fetch)
		}
	})

	t.Run("background refresh preserves expanded dir when cursor on child", func(t *testing.T) {
		fp4 := NewFilePanel()
		fp4.Box.SetRect(0, 0, 40, 20)
		// First call: dir at cursor 0 → auto-expand
		fp4.dirChildren["src/"] = []gitutil.ChangedFile{
			{Status: "M", Path: "src/main.go"},
		}
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "src/", IsDir: true},
			{Status: "A", Path: "b.go"},
		}
		fp4.SetFiles(files)
		// Cursor is now on src/main.go, src/ is expanded
		if !fp4.expanded["src/"] {
			t.Fatal("src/ should be expanded after initial SetFiles")
		}
		if f := fp4.SelectedFile(); f == nil || f.Path != "src/main.go" {
			t.Fatalf("setup: expected cursor on src/main.go, got %v", f)
		}
		// Simulate background git refresh with same files — cursor on child file,
		// not a dir row, so autoExpand should NOT fire and expansion is preserved.
		fp4.SetFiles(files)
		if !fp4.expanded["src/"] {
			t.Error("background refresh should not collapse expanded dir when cursor is on child file")
		}
		if f := fp4.SelectedFile(); f == nil || f.Path != "src/main.go" {
			t.Errorf("cursor should stay on src/main.go after refresh, got %v", f)
		}
	})

	t.Run("dir at cursor without children returns fetch", func(t *testing.T) {
		fp2 := NewFilePanel()
		fp2.Box.SetRect(0, 0, 40, 20)
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "pkg/", IsDir: true},
			{Status: "A", Path: "a.go"},
		}
		fetch := fp2.SetFiles(files)
		if fetch != "pkg/" {
			t.Errorf("expected fetch = %q, got %q", "pkg/", fetch)
		}
		// Cursor should be on a.go (skipped past dir since no children yet)
		if f := fp2.SelectedFile(); f == nil || f.Path != "a.go" {
			t.Errorf("expected cursor on a.go, got %v", f)
		}
	})
}

func TestFilePanel_SetDirChildrenSkipsToFile(t *testing.T) {
	t.Run("cursor on dir row skips to first child", func(t *testing.T) {
		fp := NewFilePanel()
		fp.Box.SetRect(0, 0, 40, 20)
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "src/", IsDir: true},
			{Status: "A", Path: "b.go"},
		}
		fp.SetFiles(files)
		// Simulate the dir being expanded (as autoExpand would have done)
		fp.expanded["src/"] = true
		// Now move cursor back to the dir row
		fp.cursor = 0

		// When children arrive, cursor should skip to first child file
		fp.SetDirChildren("src/", []gitutil.ChangedFile{
			{Status: "M", Path: "src/main.go"},
		})
		if f := fp.SelectedFile(); f == nil || f.Path != "src/main.go" {
			t.Errorf("expected cursor on src/main.go after SetDirChildren, got %v", f)
		}
	})

	t.Run("cursor already on file is not displaced", func(t *testing.T) {
		fp := NewFilePanel()
		fp.Box.SetRect(0, 0, 40, 20)
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "src/", IsDir: true},
			{Status: "A", Path: "b.go"},
			{Status: "M", Path: "c.go"},
		}
		fp.SetFiles(files)
		// Cursor should be on b.go (skipped past dir)
		if f := fp.SelectedFile(); f == nil || f.Path != "b.go" {
			t.Fatalf("setup: expected b.go, got %v", f)
		}
		// Simulate dir expanded and children arriving while cursor is on b.go
		fp.expanded["src/"] = true
		fp.SetDirChildren("src/", []gitutil.ChangedFile{
			{Status: "M", Path: "src/main.go"},
		})
		// Cursor should still be on b.go (not displaced by async children)
		if f := fp.SelectedFile(); f == nil || f.Path != "b.go" {
			t.Errorf("cursor should stay on b.go, got %v", f)
		}
	})
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

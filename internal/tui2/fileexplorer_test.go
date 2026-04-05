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

	t.Run("unfetched dir pauses on dir", func(t *testing.T) {
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "a.go"},
			{Status: "M", Path: "pkg/", IsDir: true},
			{Status: "A", Path: "b.go"},
		}
		fp.SetFiles(files)

		// Start on a.go, move down — stays on pkg/ while children are fetched
		fetch := fp.CursorDown()
		if fetch == "" {
			t.Error("expected fetch request for unfetched dir")
		}
		if f := fp.SelectedFile(); f == nil || f.Path != "pkg/" {
			t.Errorf("should stay on unfetched dir, got %v", f)
		}
	})

	t.Run("cached empty dir skips to next file", func(t *testing.T) {
		fp2 := NewFilePanel()
		fp2.Box.SetRect(0, 0, 40, 20)
		fp2.dirChildren["pkg/"] = []gitutil.ChangedFile{} // cached but empty
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "a.go"},
			{Status: "M", Path: "pkg/", IsDir: true},
			{Status: "A", Path: "b.go"},
		}
		fp2.SetFiles(files)

		// Cached empty dir — skip to b.go
		fp2.cursor = 0 // a.go
		fp2.CursorDown()
		if f := fp2.SelectedFile(); f == nil || f.Path != "b.go" {
			t.Errorf("should skip cached empty dir, got %v", f)
		}
	})
}

func TestFilePanel_SkipDirUp(t *testing.T) {
	fp := NewFilePanel()
	fp.Box.SetRect(0, 0, 40, 20)

	t.Run("cached empty dir skips to prev file", func(t *testing.T) {
		fp.dirChildren["pkg/"] = []gitutil.ChangedFile{} // cached but empty
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "a.go"},
			{Status: "M", Path: "pkg/", IsDir: true},
			{Status: "A", Path: "b.go"},
		}
		fp.SetFiles(files)

		// Move cursor to b.go
		for i, r := range fp.rows {
			if r.Path == "b.go" {
				fp.cursor = i
				break
			}
		}

		// Move up — cached empty dir, skip to a.go
		fp.CursorUp()
		if f := fp.SelectedFile(); f == nil || f.Path != "a.go" {
			t.Errorf("should skip cached empty dir going up, got %v", f)
		}
	})

	t.Run("unfetched dir pauses on dir", func(t *testing.T) {
		fp2 := NewFilePanel()
		fp2.Box.SetRect(0, 0, 40, 20)
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "a.go"},
			{Status: "M", Path: "pkg/", IsDir: true},
			{Status: "A", Path: "b.go"},
		}
		fp2.SetFiles(files)

		// Move cursor to b.go
		for i, r := range fp2.rows {
			if r.Path == "b.go" {
				fp2.cursor = i
				break
			}
		}

		// Move up — unfetched dir, stays on dir
		fetch := fp2.CursorUp()
		if fetch == "" {
			t.Error("expected fetch request for unfetched dir")
		}
		if f := fp2.SelectedFile(); f == nil || f.Path != "pkg/" {
			t.Errorf("should stay on unfetched dir, got %v", f)
		}
	})
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

func TestFilePanel_CursorUpIntoExpandedDir(t *testing.T) {
	t.Run("lands on last child when navigating up into folder", func(t *testing.T) {
		fp := NewFilePanel()
		fp.Box.SetRect(0, 0, 40, 20)
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "a.go"},
			{Status: "M", Path: "src/", IsDir: true},
			{Status: "A", Path: "b.go"},
		}
		fp.SetFiles(files)
		fp.SetDirChildren("src/", []gitutil.ChangedFile{
			{Status: "M", Path: "src/one.go"},
			{Status: "A", Path: "src/two.go"},
			{Status: "D", Path: "src/three.go"},
		})

		// Navigate down to b.go (past the folder)
		// Rows: a.go, src/, src/one.go, src/two.go, src/three.go, b.go
		// Move cursor to b.go by going down repeatedly
		fp.cursor = len(fp.rows) - 1 // b.go
		if f := fp.SelectedFile(); f == nil || f.Path != "b.go" {
			t.Fatalf("setup: expected b.go, got %v", f)
		}

		// Navigate up — should enter folder and land on last child (src/two.go,
		// alphabetically last after sorting by buildChildTree).
		fp.CursorUp()
		if f := fp.SelectedFile(); f == nil || f.Path != "src/two.go" {
			t.Errorf("expected src/two.go (last child, sorted), got %v", f)
		}
	})

	t.Run("lands on last child with single child", func(t *testing.T) {
		fp := NewFilePanel()
		fp.Box.SetRect(0, 0, 40, 20)
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "src/", IsDir: true},
			{Status: "A", Path: "b.go"},
		}
		fp.dirChildren["src/"] = []gitutil.ChangedFile{
			{Status: "M", Path: "src/only.go"},
		}
		fp.SetFiles(files)

		// Move to b.go
		fp.CursorDown()
		fp.CursorDown()
		// Find b.go
		for fp.cursor < len(fp.rows) {
			if f := fp.SelectedFile(); f != nil && f.Path == "b.go" {
				break
			}
			fp.cursor++
		}
		if f := fp.SelectedFile(); f == nil || f.Path != "b.go" {
			t.Fatalf("setup: expected b.go, got %v", f)
		}

		fp.CursorUp()
		if f := fp.SelectedFile(); f == nil || f.Path != "src/only.go" {
			t.Errorf("expected src/only.go, got %v", f)
		}
	})

	t.Run("navigating up from first child exits folder", func(t *testing.T) {
		fp := NewFilePanel()
		fp.Box.SetRect(0, 0, 40, 20)
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "a.go"},
			{Status: "M", Path: "src/", IsDir: true},
			{Status: "A", Path: "b.go"},
		}
		fp.SetFiles(files)
		fp.SetDirChildren("src/", []gitutil.ChangedFile{
			{Status: "M", Path: "src/one.go"},
			{Status: "A", Path: "src/two.go"},
		})

		// Navigate into folder — land on first child
		fp.CursorDown()
		if f := fp.SelectedFile(); f == nil || f.Path != "src/one.go" {
			t.Fatalf("setup: expected src/one.go, got %v", f)
		}

		// Navigate up from first child — should exit folder and land on a.go
		fp.CursorUp()
		if f := fp.SelectedFile(); f == nil || f.Path != "a.go" {
			t.Errorf("expected a.go (exit folder), got %v", f)
		}
	})

	t.Run("no children fetched yet stays on dir", func(t *testing.T) {
		fp := NewFilePanel()
		fp.Box.SetRect(0, 0, 40, 20)
		files := []gitutil.ChangedFile{
			{Status: "M", Path: "a.go"},
			{Status: "M", Path: "pkg/", IsDir: true},
			{Status: "A", Path: "b.go"},
		}
		fp.SetFiles(files)
		// Move to b.go
		for i, r := range fp.rows {
			if r.Path == "b.go" {
				fp.cursor = i
				break
			}
		}
		if f := fp.SelectedFile(); f == nil || f.Path != "b.go" {
			t.Fatalf("setup: expected b.go, got %v", f)
		}
		// Up — no children cached, stays on dir (fetch in progress)
		fetch := fp.CursorUp()
		if fetch == "" {
			t.Error("expected fetch request for unfetched dir")
		}
		if f := fp.SelectedFile(); f == nil || f.Path != "pkg/" {
			t.Errorf("expected pkg/ (awaiting fetch), got %v", f)
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

func TestBuildChildTree_BasicGrouping(t *testing.T) {
	children := []gitutil.ChangedFile{
		{Status: "M", Path: "src/a/1.go"},
		{Status: "A", Path: "src/a/2.go"},
		{Status: "M", Path: "src/b/3.go"},
		{Status: "M", Path: "src/main.go"},
	}
	rows := buildChildTree(nil, children, "src/", 1)

	// Expected: a/ (dir), 1.go, 2.go, b/ (dir), 3.go, main.go
	want := []struct {
		path    string
		indent  int
		isDir   bool
		display string
	}{
		{"src/a/", 1, true, "a/"},
		{"src/a/1.go", 2, false, ""},
		{"src/a/2.go", 2, false, ""},
		{"src/b/", 1, true, "b/"},
		{"src/b/3.go", 2, false, ""},
		{"src/main.go", 1, false, ""},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		r := rows[i]
		if r.Path != w.path || r.indent != w.indent || r.IsDir != w.isDir || r.displayName != w.display {
			t.Errorf("row[%d] = {%q, indent=%d, dir=%v, display=%q}, want {%q, indent=%d, dir=%v, display=%q}",
				i, r.Path, r.indent, r.IsDir, r.displayName,
				w.path, w.indent, w.isDir, w.display)
		}
	}
}

func TestBuildChildTree_DeepNesting(t *testing.T) {
	children := []gitutil.ChangedFile{
		{Status: "A", Path: "src/a/b/c/file.go"},
	}
	rows := buildChildTree(nil, children, "src/", 1)

	want := []struct {
		path   string
		indent int
		isDir  bool
	}{
		{"src/a/", 1, true},
		{"src/a/b/", 2, true},
		{"src/a/b/c/", 3, true},
		{"src/a/b/c/file.go", 4, false},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if rows[i].Path != w.path || rows[i].indent != w.indent || rows[i].IsDir != w.isDir {
			t.Errorf("row[%d] = {%q, indent=%d, dir=%v}, want {%q, indent=%d, dir=%v}",
				i, rows[i].Path, rows[i].indent, rows[i].IsDir, w.path, w.indent, w.isDir)
		}
	}
}

func TestBuildChildTree_FlatOnly(t *testing.T) {
	children := []gitutil.ChangedFile{
		{Status: "M", Path: "src/main.go"},
		{Status: "A", Path: "src/util.go"},
	}
	rows := buildChildTree(nil, children, "src/", 1)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for _, r := range rows {
		if r.IsDir {
			t.Errorf("unexpected dir row: %q", r.Path)
		}
		if r.indent != 1 {
			t.Errorf("expected indent 1, got %d for %q", r.indent, r.Path)
		}
	}
}

func TestFilePanel_NavigationWithNestedTree(t *testing.T) {
	fp := NewFilePanel()
	fp.Box.SetRect(0, 0, 40, 20)
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "M", Path: "src/", IsDir: true},
		{Status: "A", Path: "z.go"},
	}
	fp.SetFiles(files)
	fp.SetDirChildren("src/", []gitutil.ChangedFile{
		{Status: "M", Path: "src/components/Button.go"},
		{Status: "A", Path: "src/components/Input.go"},
		{Status: "M", Path: "src/utils/helper.go"},
		{Status: "M", Path: "src/main.go"},
	})

	// Rows should be:
	// a.go, src/, components/ (dir), Button.go, Input.go, utils/ (dir), helper.go, main.go, z.go
	// Navigate down from a.go — should skip src/ dir, components/ dir, land on Button.go
	fp.CursorDown()
	if f := fp.SelectedFile(); f == nil || f.Path != "src/components/Button.go" {
		t.Errorf("CursorDown from a.go: expected src/components/Button.go, got %v", f)
	}

	// Keep going down — should skip utils/ dir
	fp.CursorDown() // Input.go
	fp.CursorDown() // skip utils/ → helper.go
	if f := fp.SelectedFile(); f == nil || f.Path != "src/utils/helper.go" {
		t.Errorf("expected src/utils/helper.go, got %v", f)
	}

	// Continue to main.go then z.go
	fp.CursorDown() // main.go
	fp.CursorDown() // z.go
	if f := fp.SelectedFile(); f == nil || f.Path != "z.go" {
		t.Errorf("expected z.go, got %v", f)
	}
}

func TestFilePanel_CursorUpIntoNestedFolder(t *testing.T) {
	fp := NewFilePanel()
	fp.Box.SetRect(0, 0, 40, 20)
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "src/", IsDir: true},
		{Status: "A", Path: "z.go"},
	}
	fp.dirChildren["src/"] = []gitutil.ChangedFile{
		{Status: "M", Path: "src/components/Button.go"},
		{Status: "A", Path: "src/utils/helper.go"},
		{Status: "M", Path: "src/main.go"},
	}
	fp.SetFiles(files)

	// Navigate to z.go
	for fp.cursor < len(fp.rows)-1 {
		fp.CursorDown()
	}
	// Find z.go
	if f := fp.SelectedFile(); f == nil || f.Path != "z.go" {
		// z.go might not be the last file row, find it
		for i, r := range fp.rows {
			if r.Path == "z.go" {
				fp.cursor = i
				break
			}
		}
	}
	if f := fp.SelectedFile(); f == nil || f.Path != "z.go" {
		t.Fatalf("setup: expected z.go, got %v", f)
	}

	// Navigate up — should enter folder and land on deepest last file (main.go,
	// which is the last direct child sorted after subdirs)
	fp.CursorUp()
	if f := fp.SelectedFile(); f == nil || f.Path != "src/main.go" {
		t.Errorf("expected src/main.go (last child in tree), got %v", f)
	}
}

func TestFilePanel_CursorDownStaysOnUnfetchedDir(t *testing.T) {
	fp := NewFilePanel()
	fp.Box.SetRect(0, 0, 40, 20)
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "M", Path: "api/", IsDir: true},
		{Status: "M", Path: "lib/", IsDir: true},
		{Status: "M", Path: "src/", IsDir: true},
		{Status: "A", Path: "z.go"},
	}
	fp.SetFiles(files)

	// Start on a.go, press down — should land on api/ (unfetched), not jump to z.go
	fetch := fp.CursorDown()
	if fetch == "" {
		t.Error("expected fetch request for unfetched dir")
	}
	f := fp.SelectedFile()
	if f == nil || f.Path == "z.go" {
		t.Errorf("should NOT jump over unfetched dirs to z.go, got %v", f)
	}
	if f == nil || f.Path != "api/" {
		t.Errorf("expected cursor on api/ (awaiting fetch), got %v", f)
	}
}

func TestFilePanel_ConsecutiveUpThroughUnfetchedDirs(t *testing.T) {
	fp := NewFilePanel()
	fp.Box.SetRect(0, 0, 40, 20)
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "M", Path: "api/", IsDir: true},
		{Status: "M", Path: "lib/", IsDir: true},
		{Status: "A", Path: "z.go"},
	}
	fp.SetFiles(files)

	// Start on z.go
	for i, r := range fp.rows {
		if r.Path == "z.go" {
			fp.cursor = i
			break
		}
	}

	// First up: lands on lib/ (unfetched)
	fp.CursorUp()
	if f := fp.SelectedFile(); f == nil || f.Path != "lib/" {
		t.Fatalf("first up: expected lib/, got %v", f)
	}

	// Second up: lands on api/ (unfetched) — not a.go
	fp.CursorUp()
	if f := fp.SelectedFile(); f == nil || f.Path != "api/" {
		t.Fatalf("second up: expected api/, got %v", f)
	}

	// Third up: lands on a.go (file)
	fp.CursorUp()
	if f := fp.SelectedFile(); f == nil || f.Path != "a.go" {
		t.Errorf("third up: expected a.go, got %v", f)
	}
}

func TestFilePanel_SetDirChildrenAfterPauseOnDir(t *testing.T) {
	fp := NewFilePanel()
	fp.Box.SetRect(0, 0, 40, 20)
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "a.go"},
		{Status: "M", Path: "src/", IsDir: true},
		{Status: "A", Path: "z.go"},
	}
	fp.SetFiles(files)

	// Navigate down from a.go — lands on src/ (unfetched, pauses)
	fetch := fp.CursorDown()
	if fetch != "src/" {
		t.Fatalf("setup: expected fetch for src/, got %q", fetch)
	}
	if f := fp.SelectedFile(); f == nil || f.Path != "src/" {
		t.Fatalf("setup: expected cursor on src/, got %v", f)
	}

	// Children arrive — cursor should move to first child file
	fp.SetDirChildren("src/", []gitutil.ChangedFile{
		{Status: "M", Path: "src/main.go"},
		{Status: "A", Path: "src/util.go"},
	})
	if f := fp.SelectedFile(); f == nil || f.Path != "src/main.go" {
		t.Errorf("after SetDirChildren, expected src/main.go, got %v", f)
	}
}

func TestFilePanel_CursorUpStaysOnUnfetchedDir(t *testing.T) {
	fp := NewFilePanel()
	fp.Box.SetRect(0, 0, 40, 20)
	files := []gitutil.ChangedFile{
		{Status: "M", Path: "docker-compose.e2e.yml"},
		{Status: "M", Path: "api/", IsDir: true},
		{Status: "M", Path: "lib/", IsDir: true},
		{Status: "M", Path: "src/", IsDir: true},
		{Status: "A", Path: "z.go"},
	}
	fp.SetFiles(files)

	// Navigate to z.go
	for i, r := range fp.rows {
		if r.Path == "z.go" {
			fp.cursor = i
			break
		}
	}
	if f := fp.SelectedFile(); f == nil || f.Path != "z.go" {
		t.Fatalf("setup: expected z.go, got %v", f)
	}

	// Press up — should land on the dir above (src/), not jump to docker-compose.
	// The dir needs fetching (no cached children), so cursor stays on the dir row.
	fetch := fp.CursorUp()
	if fetch == "" {
		t.Error("expected fetch request for the unfetched dir")
	}
	f := fp.SelectedFile()
	if f == nil || f.Path == "docker-compose.e2e.yml" {
		t.Errorf("should NOT jump over unfetched dirs to docker-compose.e2e.yml, got %v", f)
	}
	if f != nil && !f.IsDir {
		t.Errorf("expected cursor on a directory row awaiting fetch, got file %v", f.Path)
	}
}

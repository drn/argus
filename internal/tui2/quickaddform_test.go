package tui2

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func TestScanDirectory_FindsGitRepos(t *testing.T) {
	dir := t.TempDir()

	// Create 3 children: 2 with .git, 1 without.
	for _, name := range []string{"repo-a", "repo-b"} {
		os.MkdirAll(filepath.Join(dir, name, ".git"), 0o755)
	}
	os.MkdirAll(filepath.Join(dir, "not-a-repo"), 0o755)

	repos, err := scanDirectory(dir, nil, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, len(repos), 2)
	testutil.Equal(t, repos[0].name, "repo-a")
	testutil.Equal(t, repos[1].name, "repo-b")

	for _, r := range repos {
		testutil.Equal(t, r.selected, true)
	}
}

func TestScanDirectory_FiltersExisting(t *testing.T) {
	dir := t.TempDir()

	repoPath := filepath.Join(dir, "myrepo")
	os.MkdirAll(filepath.Join(repoPath, ".git"), 0o755)
	os.MkdirAll(filepath.Join(dir, "newrepo", ".git"), 0o755)

	// Resolve symlinks to match what scanDirectory does internally
	// (macOS /var → /private/var).
	realRepoPath, _ := filepath.EvalSymlinks(repoPath)
	existingPaths := map[string]bool{realRepoPath: true}

	repos, err := scanDirectory(dir, existingPaths, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, len(repos), 1)
	testutil.Equal(t, repos[0].name, "newrepo")
}

func TestScanDirectory_NameConflict(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "app", ".git"), 0o755)

	// "app" already exists as a project name with a different path.
	existingNames := map[string]bool{"app": true}

	repos, err := scanDirectory(dir, nil, existingNames)
	testutil.NoError(t, err)
	testutil.Equal(t, len(repos), 1)
	testutil.Equal(t, repos[0].name, "app-2")
	testutil.Equal(t, repos[0].dirName, "app")
}

func TestScanDirectory_DepthOne(t *testing.T) {
	dir := t.TempDir()

	// .git nested at depth 2 should NOT be found.
	os.MkdirAll(filepath.Join(dir, "parent", "child", ".git"), 0o755)

	repos, err := scanDirectory(dir, nil, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, len(repos), 0)
}

func TestScanDirectory_SkipsHiddenDirs(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".hidden", ".git"), 0o755)
	os.MkdirAll(filepath.Join(dir, "visible", ".git"), 0o755)

	repos, err := scanDirectory(dir, nil, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, len(repos), 1)
	testutil.Equal(t, repos[0].name, "visible")
}

func TestScanDirectory_GitFile(t *testing.T) {
	// Worktrees use a .git FILE, not directory. Verify we detect those too.
	dir := t.TempDir()
	repoDir := filepath.Join(dir, "worktree-repo")
	os.MkdirAll(repoDir, 0o755)
	os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: /some/other/path"), 0o644)

	repos, err := scanDirectory(dir, nil, nil)
	testutil.NoError(t, err)
	testutil.Equal(t, len(repos), 1)
	testutil.Equal(t, repos[0].name, "worktree-repo")
}

func TestScanDirectory_NonexistentDir(t *testing.T) {
	_, err := scanDirectory("/nonexistent/path/xyz", nil, nil)
	if err == nil {
		t.Error("expected error for nonexistent directory")
	}
}

func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}

	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"bare tilde", "~", home},
		{"tilde slash", "~/foo", filepath.Join(home, "foo")},
		{"tilde deep", "~/a/b/c", filepath.Join(home, "a/b/c")},
		{"no tilde", "/usr/local", "/usr/local"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, expandTilde(tt.input), tt.expect)
		})
	}
}

func TestCollapseTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}

	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"home dir", home, "~"},
		{"home subdir", filepath.Join(home, "foo"), "~/foo"},
		{"other path", "/usr/local", "/usr/local"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testutil.Equal(t, collapseTilde(tt.input), tt.expect)
		})
	}
}

func TestQuickAddForm_Toggle(t *testing.T) {
	f := NewQuickAddForm(nil)
	f.repos = []repoCandidate{
		{name: "a", path: "/a", selected: true},
		{name: "b", path: "/b", selected: true},
	}
	f.phase = 1

	// Toggle first repo off.
	f.HandleKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	testutil.Equal(t, f.repos[0].selected, false)
	testutil.Equal(t, f.repos[1].selected, true)

	// Toggle back on.
	f.HandleKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	testutil.Equal(t, f.repos[0].selected, true)
}

func TestQuickAddForm_SelectAll(t *testing.T) {
	f := NewQuickAddForm(nil)
	f.repos = []repoCandidate{
		{name: "a", path: "/a", selected: false},
		{name: "b", path: "/b", selected: false},
	}
	f.phase = 1

	// Select all with 'a'.
	f.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'a', 0))
	testutil.Equal(t, f.repos[0].selected, true)
	testutil.Equal(t, f.repos[1].selected, true)

	// Deselect all with 'x'.
	f.HandleKey(tcell.NewEventKey(tcell.KeyRune, 'x', 0))
	testutil.Equal(t, f.repos[0].selected, false)
	testutil.Equal(t, f.repos[1].selected, false)
}

func TestQuickAddForm_CancelPhase0(t *testing.T) {
	f := NewQuickAddForm(nil)
	f.HandleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, f.Canceled(), true)
}

func TestQuickAddForm_EscapePhase1GoesBack(t *testing.T) {
	f := NewQuickAddForm(nil)
	f.repos = []repoCandidate{{name: "a", path: "/a", selected: true}}
	f.phase = 1

	f.HandleKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	testutil.Equal(t, f.Canceled(), false)
	testutil.Equal(t, f.phase, 0)
}

func TestQuickAddForm_DirAutocomplete(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Development"), 0o755)
	os.MkdirAll(filepath.Join(dir, "Desktop"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755)

	f := NewQuickAddForm(nil)
	// Type the parent dir path + "Dev"
	input := dir + "/Dev"
	f.dirPath = []rune(input)
	f.dirCursor = len(f.dirPath)

	f.updateDirAutocomplete()
	testutil.Equal(t, f.acOpen, true)
	testutil.Equal(t, len(f.acMatches), 1)
	testutil.Equal(t, f.acMatches[0], filepath.Join(dir, "Development"))
}

func TestQuickAddForm_DirAutocompleteListsAll(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "aaa"), 0o755)
	os.MkdirAll(filepath.Join(dir, "bbb"), 0o755)

	f := NewQuickAddForm(nil)
	input := dir + "/"
	f.dirPath = []rune(input)
	f.dirCursor = len(f.dirPath)

	f.updateDirAutocomplete()
	testutil.Equal(t, f.acOpen, true)
	testutil.Equal(t, len(f.acMatches), 2)
}

func TestQuickAddForm_DirAutocompleteSkipsHidden(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755)
	os.MkdirAll(filepath.Join(dir, "visible"), 0o755)

	f := NewQuickAddForm(nil)
	input := dir + "/"
	f.dirPath = []rune(input)
	f.dirCursor = len(f.dirPath)

	f.updateDirAutocomplete()
	testutil.Equal(t, f.acOpen, true)
	testutil.Equal(t, len(f.acMatches), 1)
	testutil.Equal(t, f.acMatches[0], filepath.Join(dir, "visible"))
}

func TestQuickAddForm_AcceptAutocomplete(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "Development"), 0o755)

	f := NewQuickAddForm(nil)
	f.dirPath = []rune(dir + "/Dev")
	f.dirCursor = len(f.dirPath)
	f.updateDirAutocomplete()

	testutil.Equal(t, f.acOpen, true)

	// Accept via Tab.
	f.acceptAutocomplete()
	result := string(f.dirPath)
	expected := collapseTilde(filepath.Join(dir, "Development")) + "/"
	testutil.Equal(t, result, expected)
}

func TestQuickAddForm_SelectedRepos(t *testing.T) {
	f := NewQuickAddForm(nil)
	f.repos = []repoCandidate{
		{name: "a", path: "/a", selected: true},
		{name: "b", path: "/b", selected: false},
		{name: "c", path: "/c", selected: true},
	}
	f.phase = 1

	selected := f.SelectedRepos()
	testutil.Equal(t, len(selected), 2)
	testutil.Equal(t, selected[0].name, "a")
	testutil.Equal(t, selected[1].name, "c")
}

// wireSyncScan sets up a synchronous OnScan callback for testing.
func wireSyncScan(f *QuickAddForm) {
	f.OnScan = func(dir string) {
		repos, err := scanDirectory(dir, f.existingPaths, f.existingNames)
		var errMsg string
		if err != nil {
			errMsg = "Error: " + err.Error()
		}
		f.SetScanResult(repos, errMsg)
	}
}

func TestQuickAddForm_Integration(t *testing.T) {
	// Full flow: enter dir, scan, select, get results.
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "project-a", ".git"), 0o755)
	os.MkdirAll(filepath.Join(dir, "project-b", ".git"), 0o755)
	os.MkdirAll(filepath.Join(dir, "not-git"), 0o755)

	database, err := db.OpenInMemory()
	testutil.NoError(t, err)

	f := NewQuickAddForm(database.Projects())
	wireSyncScan(f)

	// Type the directory path.
	for _, r := range dir {
		f.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}

	// Press Enter to scan.
	f.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	testutil.Equal(t, f.phase, 1)
	testutil.Equal(t, len(f.repos), 2)

	// Deselect first, confirm.
	f.HandleKey(tcell.NewEventKey(tcell.KeyRune, ' ', 0))
	f.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	testutil.Equal(t, f.Done(), true)
	selected := f.SelectedRepos()
	testutil.Equal(t, len(selected), 1)
	testutil.Equal(t, selected[0].name, "project-b")
}

func TestQuickAddForm_NoReposError(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "not-git"), 0o755)

	f := NewQuickAddForm(nil)
	wireSyncScan(f)
	for _, r := range dir {
		f.HandleKey(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	f.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	testutil.Equal(t, f.phase, 0)
	testutil.Contains(t, f.errMsg, "No new git repos found")
}

func TestQuickAddForm_EmptyDirError(t *testing.T) {
	f := NewQuickAddForm(nil)
	f.HandleKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))
	testutil.Contains(t, f.errMsg, "Enter a directory path")
}

func TestSettings_QuickAddCallback(t *testing.T) {
	sv := testSettingsView(t)

	// Add a project so we have a project row to select.
	sv.database.SetProject("test", config.Project{Path: "/tmp/test"})
	sv.Refresh()

	// Move cursor to a project row.
	for i, row := range sv.rows {
		if row.kind == srProject {
			sv.cursor = i
			break
		}
	}

	called := false
	sv.OnQuickAdd = func() { called = true }

	ev := tcell.NewEventKey(tcell.KeyRune, 'i', 0)
	handled := sv.HandleKey(ev)

	testutil.Equal(t, handled, true)
	testutil.Equal(t, called, true)
}

func TestSettings_QuickAddNotOnOtherSections(t *testing.T) {
	sv := testSettingsView(t)
	sv.OnQuickAdd = func() { t.Error("OnQuickAdd should not fire on non-project row") }

	// Verify precondition: cursor starts on a non-project row.
	if sv.cursor < len(sv.rows) && sv.rows[sv.cursor].kind == srProject {
		t.Fatal("precondition failed: cursor should not start on a project row")
	}

	ev := tcell.NewEventKey(tcell.KeyRune, 'i', 0)
	sv.HandleKey(ev)
}

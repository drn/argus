package tui2

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestScanVaultToDos_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	items, err := ScanVaultToDos(dir)
	testutil.NoError(t, err)
	testutil.Equal(t, len(items), 0)
}

func TestScanVaultToDos_NonExistentDir(t *testing.T) {
	items, err := ScanVaultToDos("/nonexistent/path/xyz")
	testutil.NoError(t, err)
	testutil.Equal(t, len(items), 0)
}

func TestScanVaultToDos_EmptyPath(t *testing.T) {
	items, err := ScanVaultToDos("")
	testutil.NoError(t, err)
	testutil.Nil(t, items)
}

func TestScanVaultToDos_ParsesMarkdownFiles(t *testing.T) {
	dir := t.TempDir()

	// Create two markdown files with different mod times
	f1 := filepath.Join(dir, "first-task.md")
	f2 := filepath.Join(dir, "second-task.md")
	os.WriteFile(f1, []byte("# First Task\nDo the first thing"), 0644)
	os.WriteFile(f2, []byte("# Second Task\nDo the second thing"), 0644)

	// Make f2 newer
	now := time.Now()
	os.Chtimes(f1, now.Add(-time.Hour), now.Add(-time.Hour))
	os.Chtimes(f2, now, now)

	items, err := ScanVaultToDos(dir)
	testutil.NoError(t, err)
	testutil.Equal(t, len(items), 2)

	// Newest first
	testutil.Equal(t, items[0].Name, "second-task")
	testutil.Equal(t, items[0].Path, f2)
	testutil.Contains(t, items[0].Content, "Second Task")

	testutil.Equal(t, items[1].Name, "first-task")
	testutil.Equal(t, items[1].Path, f1)
}

func TestScanVaultToDos_IgnoresNonMarkdown(t *testing.T) {
	dir := t.TempDir()

	os.WriteFile(filepath.Join(dir, "note.md"), []byte("a note"), 0644)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("text file"), 0644)
	os.WriteFile(filepath.Join(dir, "image.png"), []byte("binary"), 0644)
	os.Mkdir(filepath.Join(dir, "subdir.md"), 0755) // directory with .md suffix

	items, err := ScanVaultToDos(dir)
	testutil.NoError(t, err)
	testutil.Equal(t, len(items), 1)
	testutil.Equal(t, items[0].Name, "note")
}

func TestScanVaultToDos_StripsExtension(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "my-todo.md"), []byte("content"), 0644)

	items, err := ScanVaultToDos(dir)
	testutil.NoError(t, err)
	testutil.Equal(t, len(items), 1)
	testutil.Equal(t, items[0].Name, "my-todo")
}

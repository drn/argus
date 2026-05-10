package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// TestOpen_CorruptedFile triggers the createTables / migration error paths by
// pre-populating the database file with non-SQLite garbage.
func TestOpen_CorruptedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.sql")
	// Write 1 KB of garbage so SQLite refuses to open.
	garbage := make([]byte, 1024)
	for i := range garbage {
		garbage[i] = byte(i % 256)
	}
	if err := os.WriteFile(path, garbage, 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Open(path)
	testutil.Error(t, err)
}

// TestOpen_BadDirectory targets the MkdirAll failure when a parent path is a
// regular file.
func TestOpen_BadDirectory(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("file, not dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Open(filepath.Join(blocker, "sub", "data.sql"))
	testutil.Error(t, err)
}

// TestOpen_ReadOnlyDirectory triggers MkdirAll error via permissions.
func TestOpen_ReadOnlyDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission tests are meaningless when running as root")
	}
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) }) // allow temp cleanup

	// MkdirAll inside a non-writable dir fails.
	_, err := Open(filepath.Join(ro, "child", "data.sql"))
	testutil.Error(t, err)
}

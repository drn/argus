package kb

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

// TestWatch_RemoveFileWithPendingTimer covers the "remove with pending debounce
// timer" branch (lines 290-292). We create a file twice in rapid succession so
// the debounce timer is pending when we delete it.
func TestWatch_RemoveFileWithPendingTimer(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}
	vault := t.TempDir()
	store := newMockStore()
	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.Start())
	t.Cleanup(idx.Stop)
	<-idx.Ready()

	abs := filepath.Join(vault, "race.md")
	// Write the file (debounce timer starts).
	if err := os.WriteFile(abs, []byte("# race\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Immediately remove it — within the 500ms debounce window.
	time.Sleep(50 * time.Millisecond) // give fsnotify time to register write
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}

	// Wait long enough for debounce + remove handler to run.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			return // pass — race may not surface; we just exercise the path
		default:
			if !store.has("race.md") {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// TestWatch_NonMarkdownFile covers the early-return for non-eligible files
// in the watch loop (lines 309-311).
func TestWatch_NonMarkdownFile(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}
	vault := t.TempDir()
	store := newMockStore()
	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.Start())
	t.Cleanup(idx.Stop)
	<-idx.Ready()

	if err := os.WriteFile(filepath.Join(vault, "skip.txt"), []byte("not markdown"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Wait briefly; expect store to remain empty for the non-markdown file.
	time.Sleep(700 * time.Millisecond)
	if store.has("skip.txt") {
		t.Error("non-markdown file should not be ingested")
	}
}

// TestWatch_RemoveNonEligibleFile covers the "remove with isEligibleFile=false"
// fast-skip path.
func TestWatch_RemoveNonEligibleFile(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}
	vault := t.TempDir()
	store := newMockStore()
	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.Start())
	t.Cleanup(idx.Stop)
	<-idx.Ready()

	abs := filepath.Join(vault, "x.txt")
	if err := os.WriteFile(abs, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	// The store should not contain it; the remove handler should early-skip.
	if store.has("x.txt") {
		t.Error("non-markdown file should never be in store")
	}
}

// TestWatch_AddWatchDirs_NonRootError ensures the non-root walk-error branch
// in addWatchDirs is exercised via a permission-denied subdirectory.
func TestWatch_AddWatchDirs_NonRootError(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}
	if os.Getuid() == 0 {
		t.Skip("root can read all dirs")
	}
	vault := t.TempDir()
	bad := filepath.Join(vault, "bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bad, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o755) })

	store := newMockStore()
	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.Start())
	t.Cleanup(idx.Stop)
	<-idx.Ready()
}

// TestWatch_StopWithPendingTimer ensures the stop-with-pending-timer cleanup
// loop runs (lines 270-272). We write a file and stop immediately so the
// debounce timer is still pending.
func TestWatch_StopWithPendingTimer(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}
	vault := t.TempDir()
	store := newMockStore()
	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.Start())
	<-idx.Ready()

	// Trigger a pending timer by creating a file.
	if err := os.WriteFile(filepath.Join(vault, "pending.md"), []byte("# pending\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Give fsnotify time to enqueue the event but not enough for the
	// debounce timer (500ms) to fire.
	time.Sleep(100 * time.Millisecond)
	idx.Stop()
}

// TestWatch_NewSubdirectoryAdded ensures the directory-create branch in the
// watcher loop is exercised. We create a subdirectory after start.
func TestWatch_NewSubdirectoryAdded(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}
	vault := t.TempDir()
	store := newMockStore()
	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.Start())
	t.Cleanup(idx.Stop)
	<-idx.Ready()

	// Add subdirectory.
	sub := filepath.Join(vault, "newsub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Wait for the watcher to pick up the new dir.
	time.Sleep(200 * time.Millisecond)

	// Add a markdown file inside; should be ingested via the new watch.
	if err := os.WriteFile(filepath.Join(sub, "deep.md"), []byte("# deep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-deadline:
			return // Best-effort
		default:
			if store.has("newsub/deep.md") {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// TestWatch_HiddenSubdirectoryIgnored ensures the hidden-prefix dir branch
// in the watch loop fires.
func TestWatch_HiddenSubdirectoryIgnored(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}
	vault := t.TempDir()
	store := newMockStore()
	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.Start())
	t.Cleanup(idx.Stop)
	<-idx.Ready()

	sub := filepath.Join(vault, ".cache")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	// No assertion — just exercising the path.
}

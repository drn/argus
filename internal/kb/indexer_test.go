package kb

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

// mockStore implements KBStore for testing.
type mockStore struct {
	mu      sync.Mutex
	docs    map[string]*Document
	deleted []string
}

func newMockStore() *mockStore {
	return &mockStore{docs: make(map[string]*Document)}
}

func (m *mockStore) KBUpsert(doc *Document) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[doc.Path] = doc
	return nil
}

func (m *mockStore) KBDelete(path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.docs, path)
	m.deleted = append(m.deleted, path)
	return nil
}

func (m *mockStore) has(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.docs[path]
	return ok
}

func (m *mockStore) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.docs)
}

func TestFullScan(t *testing.T) {
	vault := t.TempDir()
	store := newMockStore()

	// Create some files.
	os.WriteFile(filepath.Join(vault, "one.md"), []byte("# One\n\nContent."), 0o644)       //nolint:errcheck
	os.WriteFile(filepath.Join(vault, "two.md"), []byte("# Two\n\nMore content."), 0o644)  //nolint:errcheck
	os.WriteFile(filepath.Join(vault, "skip.txt"), []byte("not markdown"), 0o644)           //nolint:errcheck
	os.MkdirAll(filepath.Join(vault, ".obsidian"), 0o755)                                   //nolint:errcheck
	os.WriteFile(filepath.Join(vault, ".obsidian", "hidden.md"), []byte("# Hidden"), 0o644) //nolint:errcheck

	// Nested directory.
	os.MkdirAll(filepath.Join(vault, "sub"), 0o755)                                            //nolint:errcheck
	os.WriteFile(filepath.Join(vault, "sub", "nested.md"), []byte("# Nested\n\nDeep."), 0o644) //nolint:errcheck

	idx := NewIndexer(store, vault)
	err := idx.FullScan()
	testutil.NoError(t, err)

	// Should have 3 docs: one.md, two.md, sub/nested.md.
	testutil.Equal(t, store.count(), 3)

	if !store.has("one.md") {
		t.Error("missing one.md")
	}
	if !store.has("two.md") {
		t.Error("missing two.md")
	}
	if !store.has("sub/nested.md") {
		t.Error("missing sub/nested.md")
	}
	// .obsidian should be skipped.
	if store.has(".obsidian/hidden.md") {
		t.Error(".obsidian/hidden.md should be skipped")
	}
}

func TestIngestFile(t *testing.T) {
	vault := t.TempDir()
	store := newMockStore()
	idx := NewIndexer(store, vault)

	absPath := filepath.Join(vault, "test.md")
	os.WriteFile(absPath, []byte("---\ntitle: Hello\ntags: [go]\n---\n\nBody text here."), 0o644) //nolint:errcheck

	err := idx.IngestFile(absPath)
	testutil.NoError(t, err)

	if !store.has("test.md") {
		t.Fatal("document not ingested")
	}
	store.mu.Lock()
	doc := store.docs["test.md"]
	store.mu.Unlock()
	testutil.Equal(t, doc.Title, "Hello")
	testutil.Equal(t, len(doc.Tags), 1)
	testutil.Contains(t, doc.Body, "Body text here.")
}

func TestDeleteFile(t *testing.T) {
	vault := t.TempDir()
	store := newMockStore()
	store.docs["notes/old.md"] = &Document{Path: "notes/old.md"}
	idx := NewIndexer(store, vault)

	err := idx.DeleteFile(filepath.Join(vault, "notes", "old.md"))
	testutil.NoError(t, err)

	if store.has("notes/old.md") {
		t.Error("document should have been deleted")
	}
}

func TestIsEligibleFile(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"markdown", "/vault/test.md", true},
		{"uppercase", "/vault/TEST.MD", true},
		{"hidden", "/vault/.hidden.md", false},
		{"icloud", "/vault/file.icloud", false},
		{"tmp", "/vault/file.md.tmp", false},
		{"txt", "/vault/file.txt", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testutil.Equal(t, isEligibleFile(tc.path), tc.want)
		})
	}
}

func TestWatch_CreateFile(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}

	vault := t.TempDir()
	store := newMockStore()
	idx := NewIndexer(store, vault)

	err := idx.Start()
	testutil.NoError(t, err)
	defer idx.Stop()
	<-idx.Ready()

	// Create a new file — watcher should pick it up after debounce.
	absPath := filepath.Join(vault, "new.md")
	os.WriteFile(absPath, []byte("# New\n\nCreated after start."), 0o644) //nolint:errcheck

	// Wait for debounce + processing.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for file to be ingested")
		default:
			if store.has("new.md") {
				return // success
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func TestWatch_DeleteFile(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}

	vault := t.TempDir()
	store := newMockStore()

	// Pre-create a file so it's in the store after FullScan.
	absPath := filepath.Join(vault, "doomed.md")
	os.WriteFile(absPath, []byte("# Doomed"), 0o644) //nolint:errcheck

	idx := NewIndexer(store, vault)
	err := idx.Start()
	testutil.NoError(t, err)
	defer idx.Stop()
	<-idx.Ready()

	// Verify it was ingested.
	if !store.has("doomed.md") {
		t.Fatal("file should have been ingested on start")
	}

	// Remove the file.
	os.Remove(absPath) //nolint:errcheck

	// Wait for removal to be processed.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for file deletion")
		default:
			if !store.has("doomed.md") {
				return // success — deleted from store
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func TestWatch_SubdirectoryFile(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}

	vault := t.TempDir()
	store := newMockStore()
	idx := NewIndexer(store, vault)

	err := idx.Start()
	testutil.NoError(t, err)
	defer idx.Stop()
	<-idx.Ready()

	// Create a subdirectory and file.
	subDir := filepath.Join(vault, "notes")
	os.MkdirAll(subDir, 0o755)                                                       //nolint:errcheck
	time.Sleep(200 * time.Millisecond) // give watcher time to pick up new dir
	os.WriteFile(filepath.Join(subDir, "deep.md"), []byte("# Deep\n\nNested."), 0o644) //nolint:errcheck

	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for nested file to be ingested")
		default:
			if store.has("notes/deep.md") {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

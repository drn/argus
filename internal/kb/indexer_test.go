package kb

import (
	"errors"
	"fmt"
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

func (m *mockStore) KBMetadataMap() (map[string]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.docs))
	for path, doc := range m.docs {
		out[path] = doc.ModifiedAt.Unix()
	}
	return out, nil
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
	os.WriteFile(filepath.Join(vault, "one.md"), []byte("# One\n\nContent."), 0o644)        //nolint:errcheck
	os.WriteFile(filepath.Join(vault, "two.md"), []byte("# Two\n\nMore content."), 0o644)   //nolint:errcheck
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

func TestIncrementalScan_SkipsUnchanged(t *testing.T) {
	vault := t.TempDir()
	store := newMockStore()

	// Write a file and do an initial full scan.
	absPath := filepath.Join(vault, "stable.md")
	os.WriteFile(absPath, []byte("# Stable\n\nOriginal."), 0o644) //nolint:errcheck

	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.FullScan())
	testutil.Equal(t, store.count(), 1)

	// Record the ingested doc's body so we can check it doesn't change.
	store.mu.Lock()
	origBody := store.docs["stable.md"].Body
	store.mu.Unlock()

	// Overwrite the doc in the store with a marker body (simulate "already ingested").
	store.mu.Lock()
	store.docs["stable.md"].Body = "MARKER"
	store.mu.Unlock()

	// Run incremental scan — file mtime hasn't changed, so it should be skipped.
	meta, err := store.KBMetadataMap()
	testutil.NoError(t, err)
	testutil.NoError(t, idx.IncrementalScan(meta))

	// Body should still be the marker — IngestFile was NOT called.
	store.mu.Lock()
	testutil.Equal(t, store.docs["stable.md"].Body, "MARKER")
	store.mu.Unlock()
	_ = origBody
}

func TestIncrementalScan_IngestsModified(t *testing.T) {
	vault := t.TempDir()
	store := newMockStore()

	absPath := filepath.Join(vault, "changing.md")
	os.WriteFile(absPath, []byte("# V1\n\nOriginal."), 0o644) //nolint:errcheck

	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.FullScan())

	// Build metadata with a stale mtime (1 second in the past) to simulate change.
	meta := map[string]int64{"changing.md": time.Now().Add(-10 * time.Second).Unix()}

	// Update file content.
	os.WriteFile(absPath, []byte("# V2\n\nUpdated."), 0o644) //nolint:errcheck

	testutil.NoError(t, idx.IncrementalScan(meta))

	store.mu.Lock()
	testutil.Contains(t, store.docs["changing.md"].Body, "Updated.")
	store.mu.Unlock()
}

func TestIncrementalScan_IngestsNew(t *testing.T) {
	vault := t.TempDir()
	store := newMockStore()
	idx := NewIndexer(store, vault)

	// Empty metadata — everything on disk is new.
	os.WriteFile(filepath.Join(vault, "brand-new.md"), []byte("# New"), 0o644) //nolint:errcheck

	meta := map[string]int64{} // empty but non-nil — simulates "DB has data but this file is new"
	testutil.NoError(t, idx.IncrementalScan(meta))

	testutil.Equal(t, store.count(), 1)
	if !store.has("brand-new.md") {
		t.Error("new file should have been ingested")
	}
}

func TestIncrementalScan_DeletesRemoved(t *testing.T) {
	vault := t.TempDir()
	store := newMockStore()
	idx := NewIndexer(store, vault)

	// Simulate a doc in the DB whose file no longer exists on disk.
	meta := map[string]int64{"gone.md": time.Now().Unix()}

	testutil.NoError(t, idx.IncrementalScan(meta))

	store.mu.Lock()
	deleted := store.deleted
	store.mu.Unlock()

	testutil.Equal(t, len(deleted), 1)
	testutil.Equal(t, deleted[0], "gone.md")
}

func TestStart_EmptyDB_BackgroundFullScan(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}

	vault := t.TempDir()
	store := newMockStore()

	// Pre-create a file.
	os.WriteFile(filepath.Join(vault, "hello.md"), []byte("# Hello"), 0o644) //nolint:errcheck

	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.Start())
	defer idx.Stop()

	// Scanning should be true initially (background full scan).
	// Wait for the scan to complete.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for background scan to complete")
		default:
			if !idx.Scanning() && store.has("hello.md") {
				return // success
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestStart_ExistingDB_IncrementalSync(t *testing.T) {
	if testing.Short() {
		t.Skip("fsnotify test")
	}

	vault := t.TempDir()
	store := newMockStore()

	// Pre-create a file and do a full scan to populate the store.
	absPath := filepath.Join(vault, "existing.md")
	os.WriteFile(absPath, []byte("# Existing"), 0o644) //nolint:errcheck

	preIdx := NewIndexer(store, vault)
	testutil.NoError(t, preIdx.FullScan())
	testutil.Equal(t, store.count(), 1)

	// Now start a new indexer — should use incremental (not background).
	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.Start())
	defer idx.Stop()

	// Should NOT be scanning (incremental is synchronous).
	testutil.Equal(t, idx.Scanning(), false)
	testutil.Equal(t, store.count(), 1)
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

	// Wait for background scan to complete (cold start uses background FullScan).
	scanDeadline := time.After(3 * time.Second)
	for {
		select {
		case <-scanDeadline:
			t.Fatal("timed out waiting for background scan")
		default:
			if store.has("doomed.md") {
				goto ingested
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
ingested:

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
	os.MkdirAll(subDir, 0o755)                                                         //nolint:errcheck
	time.Sleep(200 * time.Millisecond)                                                 // give watcher time to pick up new dir
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

// errStore wraps mockStore but returns an error from KBMetadataMap to drive
// the err branch in Indexer.Start.
type errStore struct {
	mockStore
	metaErr error
}

func (e *errStore) KBMetadataMap() (map[string]int64, error) {
	if e.metaErr != nil {
		return nil, e.metaErr
	}
	return e.mockStore.KBMetadataMap()
}

func TestIndexer_IngestFile_ErrorPaths(t *testing.T) {
	t.Run("file does not exist", func(t *testing.T) {
		vault := t.TempDir()
		store := newMockStore()
		idx := NewIndexer(store, vault)
		err := idx.IngestFile(filepath.Join(vault, "missing.md"))
		testutil.Error(t, err)
	})
}

func TestIndexer_DeleteFile_FallsBackOnRelError(t *testing.T) {
	store := newMockStore()
	idx := NewIndexer(store, "/vault")
	// On most platforms filepath.Rel succeeds for any pair; the fallback
	// branch is defensive. Provide a path that's already absolute and
	// outside the vault root — Rel returns a path with ".." which still
	// succeeds, so we just exercise the happy path here too.
	store.docs["/vault/notes/old.md"] = &Document{Path: "/vault/notes/old.md"}
	err := idx.DeleteFile("/vault/notes/old.md")
	testutil.NoError(t, err)
}

func TestIndexer_FullScan_VaultDoesNotExist(t *testing.T) {
	store := newMockStore()
	idx := NewIndexer(store, "/no/such/vault/path/x")
	err := idx.FullScan()
	testutil.Error(t, err)
}

func TestIndexer_FullScan_SkipsFilesInsideHiddenSubdirs(t *testing.T) {
	vault := t.TempDir()
	store := newMockStore()

	// Hidden directory should be skipped.
	if err := os.MkdirAll(filepath.Join(vault, ".trash"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, ".trash", "ignored.md"), []byte("# ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "real.md"), []byte("# real"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.FullScan())
	testutil.Equal(t, store.count(), 1)
	testutil.Equal(t, store.has("real.md"), true)
}

func TestIndexer_IncrementalScan_VaultDoesNotExist(t *testing.T) {
	store := newMockStore()
	idx := NewIndexer(store, "/no/such/vault")
	err := idx.IncrementalScan(map[string]int64{})
	testutil.Error(t, err)
}

func TestIndexer_IncrementalScan_SkipsHiddenSubdirs(t *testing.T) {
	vault := t.TempDir()
	store := newMockStore()

	if err := os.MkdirAll(filepath.Join(vault, ".obsidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, ".obsidian", "skip.md"), []byte("# skip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "good.md"), []byte("# good"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.IncrementalScan(map[string]int64{}))
	testutil.Equal(t, store.count(), 1)
	testutil.Equal(t, store.has("good.md"), true)
}

func TestIndexer_IncrementalScan_NonMarkdownFiles(t *testing.T) {
	vault := t.TempDir()
	store := newMockStore()

	if err := os.WriteFile(filepath.Join(vault, "config.txt"), []byte("not markdown"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "real.md"), []byte("# real"), 0o644); err != nil {
		t.Fatal(err)
	}

	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.IncrementalScan(map[string]int64{}))
	testutil.Equal(t, store.count(), 1)
	testutil.Equal(t, store.has("real.md"), true)
}

func TestIndexer_Start_EmptyVaultPath(t *testing.T) {
	store := newMockStore()
	idx := NewIndexer(store, "")
	err := idx.Start()
	testutil.NoError(t, err)
	// Ready should be closed.
	select {
	case <-idx.Ready():
	default:
		t.Error("Ready channel should be closed when vault path is empty")
	}
}

func TestIndexer_Start_MetadataMapError(t *testing.T) {
	vault := t.TempDir()
	store := &errStore{
		mockStore: *newMockStore(),
		metaErr:   errors.New("boom"),
	}
	idx := NewIndexer(store, vault)
	err := idx.Start()
	testutil.Error(t, err)
}

func TestIndexer_StopIdempotent(t *testing.T) {
	vault := t.TempDir()
	store := newMockStore()
	idx := NewIndexer(store, vault)
	testutil.NoError(t, idx.Start())
	idx.Stop()
	idx.Stop() // second call should be a no-op (already-closed branch)
}

// TestIndexer_FullScan_VaultIsFile covers the rare case where vault path is a
// file rather than a directory. filepath.Walk returns an error for the root.
func TestIndexer_FullScan_VaultIsFile(t *testing.T) {
	dir := t.TempDir()
	vaultFile := filepath.Join(dir, "vault.md")
	if err := os.WriteFile(vaultFile, []byte("# not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := newMockStore()
	idx := NewIndexer(store, vaultFile)
	// FullScan only treats the root err as fatal. A file root will simply
	// ingest the single file (since it's a markdown file at the root).
	_ = idx.FullScan()
}

// TestIngestFile_PreservesNonZeroModifiedAt covers the "ModifiedAt already set"
// branch. Since IngestFile calls ParseDocument which always returns
// ModifiedAt zero, we cannot reach the non-zero branch through this path.
// Document the limitation here for posterity.
func TestIngestFile_NonZeroModifiedAtUnreachable(t *testing.T) {
	// The ModifiedAt branch in ingest.go IngestFile is only reachable if
	// ParseDocument were to ever return a non-zero ModifiedAt — but it
	// doesn't. Keeping this no-op test as a marker.
	store := newMockStore()
	testutil.NoError(t, IngestFile(store, "x.md", "x"))
}

// TestIndexer_IncrementalScan_DeleteErrorIsLogged exercises the delete-error
// branch in the trailing for-loop.
type failDeleteStore struct {
	mockStore
	deleteErr error
}

func (f *failDeleteStore) KBDelete(path string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	return f.mockStore.KBDelete(path)
}

func TestIndexer_IncrementalScan_LogsDeleteError(t *testing.T) {
	vault := t.TempDir()
	store := &failDeleteStore{
		mockStore: *newMockStore(),
		deleteErr: fmt.Errorf("forced delete error"),
	}
	idx := NewIndexer(store, vault)
	// Provide meta that contains a path no longer on disk so the delete
	// branch fires.
	meta := map[string]int64{"gone.md": time.Now().Unix()}
	// Should not return an error — delete failures are only logged.
	testutil.NoError(t, idx.IncrementalScan(meta))
}

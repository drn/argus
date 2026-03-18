package kb

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Indexer watches a vault path and keeps kb_documents in sync.
// It performs an initial full scan on Start() and then watches for changes
// using fsnotify (added in Phase 5). For now it only does full scans.
type Indexer struct {
	db        KBStore
	vaultPath string
	stopCh    chan struct{}
	wg        sync.WaitGroup
}

// NewIndexer creates a new Indexer for the given vault path.
func NewIndexer(db KBStore, vaultPath string) *Indexer {
	return &Indexer{
		db:        db,
		vaultPath: vaultPath,
		stopCh:    make(chan struct{}),
	}
}

// Start runs the initial full scan and starts the background watcher goroutine.
func (idx *Indexer) Start() error {
	if idx.vaultPath == "" {
		return nil
	}
	if err := idx.FullScan(); err != nil {
		return err
	}
	idx.wg.Add(1)
	go func() {
		defer idx.wg.Done()
		idx.watch()
	}()
	return nil
}

// Stop signals the background goroutine to exit and waits for it.
func (idx *Indexer) Stop() {
	select {
	case <-idx.stopCh:
		// already stopped
	default:
		close(idx.stopCh)
	}
	idx.wg.Wait()
}

// IngestFile reads a single file from disk and upserts it into the KB.
// path should be absolute; the vault-relative path is stored in the KB.
func (idx *Indexer) IngestFile(absPath string) error {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return err
	}
	relPath, err := filepath.Rel(idx.vaultPath, absPath)
	if err != nil {
		relPath = absPath
	}

	doc := ParseDocument(relPath, string(data))
	doc.ModifiedAt = info.ModTime()
	doc.IngestedAt = time.Now()
	return idx.db.KBUpsert(&doc)
}

// DeleteFile removes a document from the KB by its absolute path.
func (idx *Indexer) DeleteFile(absPath string) error {
	relPath, err := filepath.Rel(idx.vaultPath, absPath)
	if err != nil {
		relPath = absPath
	}
	return idx.db.KBDelete(relPath)
}

// FullScan walks all .md files in the vault and upserts them into the KB.
// Skips the .obsidian/ directory.
func (idx *Indexer) FullScan() error {
	return filepath.Walk(idx.vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if path == idx.vaultPath {
				return err // vault root is inaccessible — propagate
			}
			return nil // skip unreadable sub-paths
		}
		// Skip .obsidian directory and its contents.
		if info.IsDir() {
			if info.Name() == ".obsidian" {
				return filepath.SkipDir
			}
			return nil
		}
		// Only process markdown files.
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			return nil
		}
		return idx.IngestFile(path)
	})
}

// watch is the background goroutine. It currently just sleeps until Stop is
// called. Phase 5 adds fsnotify integration here.
func (idx *Indexer) watch() {
	// Placeholder: fsnotify watcher added in Phase 5.
	<-idx.stopCh
}

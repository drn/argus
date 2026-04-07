package kb

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// debounceDelay is how long to wait after a file event before processing.
// iCloud-synced files may arrive partially written.
const debounceDelay = 500 * time.Millisecond

// Indexer watches a vault path and keeps kb_documents in sync.
// It performs an initial full scan on Start() and then watches for changes
// using fsnotify.
type Indexer struct {
	db        KBStore
	vaultPath string
	stopCh    chan struct{}
	readyCh   chan struct{} // closed when fsnotify watcher is set up
	wg        sync.WaitGroup
}

// NewIndexer creates a new Indexer for the given vault path.
func NewIndexer(db KBStore, vaultPath string) *Indexer {
	return &Indexer{
		db:        db,
		vaultPath: vaultPath,
		stopCh:    make(chan struct{}),
		readyCh:   make(chan struct{}),
	}
}

// Start runs the initial full scan and starts the background fsnotify watcher.
func (idx *Indexer) Start() error {
	if idx.vaultPath == "" {
		close(idx.readyCh)
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

// Ready returns a channel that is closed when the fsnotify watcher is set up
// and ready to receive events.
func (idx *Indexer) Ready() <-chan struct{} {
	return idx.readyCh
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
		// Skip hidden directories (.obsidian, .git, .trash, etc.).
		if info.IsDir() {
			if info.Name() != "." && strings.HasPrefix(info.Name(), ".") {
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

// isEligibleFile returns true if the path is a .md file that should be indexed.
func isEligibleFile(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return false
	}
	if strings.HasSuffix(base, ".icloud") {
		return false
	}
	if strings.HasSuffix(base, ".tmp") {
		return false
	}
	return strings.HasSuffix(strings.ToLower(base), ".md")
}

// watch uses fsnotify to monitor the vault for file changes.
// It watches all subdirectories recursively and handles create/write/remove/rename.
func (idx *Indexer) watch() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("[kb] fsnotify init failed: %v", err)
		close(idx.readyCh)
		<-idx.stopCh
		return
	}
	defer watcher.Close()

	// Add vault root and all subdirectories.
	if err := idx.addWatchDirs(watcher); err != nil {
		log.Printf("[kb] failed to watch vault dirs: %v", err)
		close(idx.readyCh)
		<-idx.stopCh
		return
	}

	log.Printf("[kb] watching %s for changes", idx.vaultPath)
	close(idx.readyCh)

	// Debounce: pending tracks files waiting for their timer to fire.
	pending := make(map[string]*time.Timer)
	ready := make(chan string, 16)

	for {
		select {
		case <-idx.stopCh:
			for _, t := range pending {
				t.Stop()
			}
			return

		case path := <-ready:
			delete(pending, path)
			if err := idx.IngestFile(path); err != nil {
				log.Printf("[kb] ingest %s: %v", path, err)
			}

		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			switch {
			case event.Op&fsnotify.Remove != 0 || event.Op&fsnotify.Rename != 0:
				// File removed or renamed away — delete from KB.
				if isEligibleFile(event.Name) {
					if t, exists := pending[event.Name]; exists {
						t.Stop()
						delete(pending, event.Name)
					}
					if err := idx.DeleteFile(event.Name); err != nil {
						log.Printf("[kb] delete %s: %v", event.Name, err)
					}
				}

			case event.Op&(fsnotify.Create|fsnotify.Write) != 0:
				// New directory — add to watcher for recursive monitoring.
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					base := filepath.Base(event.Name)
					if !strings.HasPrefix(base, ".") {
						watcher.Add(event.Name) //nolint:errcheck
					}
					continue
				}

				if !isEligibleFile(event.Name) {
					continue
				}

				// Debounce: reset timer on each write to the same file.
				if t, exists := pending[event.Name]; exists {
					t.Stop()
				}
				path := event.Name
				pending[path] = time.AfterFunc(debounceDelay, func() {
					select {
					case ready <- path:
					case <-idx.stopCh:
					}
				})
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Printf("[kb] watcher error: %v", err)
		}
	}
}

// addWatchDirs walks the vault and adds all directories to the fsnotify watcher.
func (idx *Indexer) addWatchDirs(watcher *fsnotify.Watcher) error {
	return filepath.Walk(idx.vaultPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if path == idx.vaultPath {
				return err
			}
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		// Skip .obsidian and other hidden directories.
		if info.Name() != "." && strings.HasPrefix(info.Name(), ".") {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

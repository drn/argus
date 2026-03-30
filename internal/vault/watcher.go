// Package vault watches the Argus vault directory for new .md files
// and auto-creates tasks from them via a pluggable task creator.
package vault

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
)

// debounceDelay is how long to wait after a file Create/Write event before processing.
// iCloud-synced files may arrive partially written; this gives the sync time to finish.
const debounceDelay = 500 * time.Millisecond

// TaskCreator creates a task from name, prompt, project, and todoPath.
// Returns the created task or an error.
type TaskCreator func(name, prompt, project, todoPath string) (*model.Task, error)

// Watcher watches the Argus vault directory for new .md files and
// auto-creates tasks from them.
type Watcher struct {
	db         *db.DB
	vaultPath  string
	createTask TaskCreator
	stopCh     chan struct{}
}

// NewWatcher creates a new vault watcher for the given path.
func NewWatcher(database *db.DB, vaultPath string, creator TaskCreator) *Watcher {
	return &Watcher{
		db:         database,
		vaultPath:  vaultPath,
		createTask: creator,
		stopCh:     make(chan struct{}),
	}
}

// Start performs an initial scan for unlinked .md files, then watches for new
// files via fsnotify. Blocks until Stop is called or an error occurs.
func (w *Watcher) Start() error {
	if w.vaultPath == "" {
		return nil
	}

	// Ensure vault directory exists.
	if err := os.MkdirAll(w.vaultPath, 0o755); err != nil {
		return err
	}

	// Initial scan: create tasks for any .md files that don't already have linked tasks.
	w.scanExisting()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := watcher.Add(w.vaultPath); err != nil {
		return err
	}

	log.Printf("[vault] watching %s for new .md files", w.vaultPath)

	// pending tracks files waiting for debounce. The channel receives
	// paths when debounce timers fire, keeping all map access on this goroutine.
	pending := make(map[string]*time.Timer)
	ready := make(chan string, 16)

	for {
		select {
		case <-w.stopCh:
			for _, t := range pending {
				t.Stop()
			}
			return nil

		case path := <-ready:
			// Debounce timer fired — process the file.
			delete(pending, path)
			w.processFile(path)

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Op&(fsnotify.Create|fsnotify.Write) == 0 {
				continue
			}
			if !IsEligibleFile(event.Name) {
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
				case <-w.stopCh:
				}
			})

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Printf("[vault] watcher error: %v", err)
		}
	}
}

// Stop signals the watcher to exit. Start() returns after stopCh is closed.
func (w *Watcher) Stop() {
	select {
	case <-w.stopCh:
		// already stopped
	default:
		close(w.stopCh)
	}
}

// scanExisting checks all .md files in the vault and creates tasks for any
// that don't already have a linked task.
func (w *Watcher) scanExisting() {
	entries, err := os.ReadDir(w.vaultPath)
	if err != nil {
		log.Printf("[vault] scan error: %v", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		absPath := filepath.Join(w.vaultPath, entry.Name())
		if !IsEligibleFile(absPath) {
			continue
		}
		w.processFile(absPath)
	}
}

// processFile reads a .md file and creates a task from it if no task
// is already linked to this path.
func (w *Watcher) processFile(absPath string) {
	// Check the stop channel to avoid creating tasks during shutdown.
	select {
	case <-w.stopCh:
		return
	default:
	}

	// Deduplication: skip if a task already exists for this path.
	existing := w.db.TasksByTodoPath()
	if _, ok := existing[absPath]; ok {
		return
	}

	// Read the file content.
	data, err := os.ReadFile(absPath)
	if err != nil {
		log.Printf("[vault] read error %s: %v", absPath, err)
		return
	}
	if len(data) == 0 {
		return
	}

	cfg := w.db.Config()
	project := cfg.Defaults.TodoProject
	if project == "" {
		log.Printf("[vault] skipping %s: no default todo project configured", filepath.Base(absPath))
		return
	}

	// Name from filename (strip .md extension).
	name := strings.TrimSuffix(filepath.Base(absPath), ".md")
	prompt := model.BuildToDoPrompt("", string(data))

	task, err := w.createTask(name, prompt, project, absPath)
	if err != nil {
		log.Printf("[vault] failed to create task from %s: %v", filepath.Base(absPath), err)
		return
	}

	log.Printf("[vault] auto-created task %s (%s) from %s", task.ID, task.Name, filepath.Base(absPath))
}

// StartPolling periodically scans the vault directory for new .md files and
// auto-creates tasks from them. Blocks until Stop is called. The interval
// controls how often the scan runs.
func (w *Watcher) StartPolling(interval time.Duration) error {
	if w.vaultPath == "" {
		return nil
	}

	// Ensure vault directory exists.
	if err := os.MkdirAll(w.vaultPath, 0o755); err != nil {
		return err
	}

	log.Printf("[vault] polling %s every %s for new .md files", w.vaultPath, interval)

	// Initial scan.
	w.scanExisting()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return nil
		case <-ticker.C:
			w.scanExisting()
		}
	}
}

// IsEligibleFile returns true if the path is a .md file that should be processed.
// Excludes .icloud placeholder files, hidden files, and non-.md files.
func IsEligibleFile(path string) bool {
	base := filepath.Base(path)

	// Skip hidden files.
	if strings.HasPrefix(base, ".") {
		return false
	}

	// Skip iCloud placeholder files.
	if strings.HasSuffix(base, ".icloud") {
		return false
	}

	// Only process .md files.
	return strings.HasSuffix(base, ".md")
}

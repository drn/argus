package tui2

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/drn/argus/internal/uxlog"
)

// maxToDoFileSize is the maximum file size (1MB) for to-do notes.
// Larger files are skipped to avoid pathological memory use.
const maxToDoFileSize = 1 << 20

// ToDoItem represents a single to-do note from the Obsidian vault.
type ToDoItem struct {
	Name     string    // display name (filename without .md extension)
	Path     string    // full filesystem path
	Content  string    // raw markdown content
	ModTime  time.Time // last modification time
}

// ScanVaultToDos reads all .md files from the given vault directory
// and returns them as ToDoItem entries sorted by modification time (newest first).
func ScanVaultToDos(vaultDir string) ([]ToDoItem, error) {
	if vaultDir == "" {
		return nil, nil
	}

	entries, err := os.ReadDir(vaultDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var items []ToDoItem
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		fullPath := filepath.Join(vaultDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.Size() > maxToDoFileSize {
			uxlog.Log("[todos] skipping %s: file too large (%d bytes)", entry.Name(), info.Size())
			continue
		}

		data, err := os.ReadFile(fullPath)
		if err != nil {
			continue
		}

		name := strings.TrimSuffix(entry.Name(), ".md")
		items = append(items, ToDoItem{
			Name:    name,
			Path:    fullPath,
			Content: string(data),
			ModTime: info.ModTime(),
		})
	}

	// Sort by modification time, newest first
	sort.Slice(items, func(i, j int) bool {
		return items[i].ModTime.After(items[j].ModTime)
	})

	return items, nil
}

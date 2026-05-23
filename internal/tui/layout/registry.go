package layout

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Registry holds named layouts. Safe for concurrent reads/writes.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]Layout
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{entries: make(map[string]Layout)}
}

// Register stores a validated layout. Re-registering the same name replaces
// the prior entry.
func (r *Registry) Register(l Layout) error {
	if err := Validate(l); err != nil {
		return err
	}
	r.mu.Lock()
	r.entries[l.Name] = l
	r.mu.Unlock()
	return nil
}

// Get returns the layout registered under name, if any.
func (r *Registry) Get(name string) (Layout, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	l, ok := r.entries[name]
	return l, ok
}

// List returns all registered layouts sorted by name.
func (r *Registry) List() []Layout {
	r.mu.RLock()
	out := make([]Layout, 0, len(r.entries))
	for _, l := range r.entries {
		out = append(out, l)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// LoadResult summarizes a [Registry.LoadDir] call.
type LoadResult struct {
	Loaded int
	Errors []error
}

// LoadDir reads every *.json file directly inside dir, parses each as a
// layout, and registers the valid ones. Subdirectories are ignored. Missing
// or empty paths are no-ops so callers can wire it unconditionally to
// ~/.argus/layouts/.
func (r *Registry) LoadDir(dir string) LoadResult {
	var res LoadResult
	if dir == "" {
		return res
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return res
		}
		res.Errors = append(res.Errors, fmt.Errorf("read layouts dir %s: %w", dir, err))
		return res
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path) //nolint:gosec // user-managed layout files
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("read %s: %w", path, err))
			continue
		}
		l, err := Parse(data)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("parse %s: %w", path, err))
			continue
		}
		if err := r.Register(l); err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("register %s: %w", path, err))
			continue
		}
		res.Loaded++
	}
	return res
}

// WithDefaults registers the built-in default layout(s) on r and returns
// r. Convenience wrapper for boot wiring.
func WithDefaults(r *Registry) *Registry {
	_ = r.Register(defaultLayout())
	return r
}

// defaultLayout returns the descriptor for the existing three-panel task
// page. The shape mirrors what app.go builds today; making it data lets
// PR 7's settings UI list it alongside user layouts.
func defaultLayout() Layout {
	return Layout{
		Name:  DefaultLayoutName,
		Title: "Tasks (default)",
		Root: Node{
			Type:      NodeSplit,
			Direction: DirHorizontal,
			Sizes:     []int{1, 3, 1},
			Children: []Node{
				{Type: NodeTaskList},
				{
					Type:      NodeSplit,
					Direction: DirVertical,
					Sizes:     []int{3, 7},
					Children: []Node{
						{Type: NodeGit},
						{Type: NodeTaskPreview},
					},
				},
				{Type: NodeTaskDetail},
			},
		},
	}
}

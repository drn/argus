// Package todo defines the pluggable to-do-list backend abstraction used by
// the MCP todo_* tools (see internal/mcp). Exactly one backend is active at a
// time, named by config.TodoConfig.Backend; internal/todo/things3 is the
// first (and, for now, only) implementation.
package todo

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/drn/argus/internal/config"
)

// Item is a single to-do item as returned by a backend. ID is opaque and
// backend-specific — callers must treat it as a token, not parse it.
type Item struct {
	ID    string
	Title string
	Notes string
	Done  bool
}

// CreateInput carries the fields accepted when creating an item.
type CreateInput struct {
	Title string
	Notes string
}

// UpdateInput carries the fields accepted when updating an item. A nil
// pointer means "leave unchanged"; a non-nil pointer to an empty string
// means "clear this field" — the same partial-update idiom schedule_update
// already uses.
type UpdateInput struct {
	Title *string
	Notes *string
}

// Backend is implemented by every to-do-list integration Argus supports.
// Argus treats the backend as the sole source of truth: it never caches or
// mirrors item content in its own database, so every method queries (or
// mutates) the backend live.
type Backend interface {
	Create(ctx context.Context, in CreateInput) (Item, error)
	// List returns open (not completed/canceled) items.
	List(ctx context.Context) ([]Item, error)
	Update(ctx context.Context, id string, in UpdateInput) (Item, error)
	Complete(ctx context.Context, id string) (Item, error)
	Delete(ctx context.Context, id string) error
}

// Factory builds a Backend from the resolved TodoConfig. A factory reads only
// its own sub-table (e.g. cfg.Things3) — the shared TodoConfig shape lets the
// registry stay backend-agnostic without generics.
type Factory func(cfg config.TodoConfig) (Backend, error)

var (
	mu        sync.RWMutex
	factories = map[string]Factory{}
)

// Register adds a backend factory under name. Called from each backend
// package's init() (see internal/todo/things3). Panics on a duplicate name —
// that is a programming error caught at daemon startup, not a runtime
// condition to handle gracefully.
func Register(name string, f Factory) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := factories[name]; exists {
		panic(fmt.Sprintf("todo: backend %q already registered", name))
	}
	factories[name] = f
}

// Get resolves the named backend against cfg. Returns an error for an empty
// or unregistered name so callers can surface a clear configuration error
// instead of a nil-backend panic.
func Get(name string, cfg config.TodoConfig) (Backend, error) {
	if name == "" {
		return nil, fmt.Errorf("todo: no backend configured")
	}
	mu.RLock()
	f, ok := factories[name]
	mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("todo: unknown backend %q (registered: %v)", name, Registered())
	}
	return f(cfg)
}

// Registered returns the sorted list of registered backend names.
func Registered() []string {
	mu.RLock()
	defer mu.RUnlock()
	names := make([]string, 0, len(factories))
	for name := range factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

package settings

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds the live set of plugin settings sections. Concurrent reads
// (from the TUI's Refresh path) and writes (from the HTTP handler) are safe.
//
// Persistence is owned by `*db.DB.PluginSettingsSections` and friends — the
// registry is in-memory state seeded from the DB at boot and updated on
// every HTTP register/unregister so the TUI sees both replays and live
// additions through the same code path.
type Registry struct {
	mu       sync.RWMutex
	sections map[registryKey]Section
}

// registryKey is the composite (scope, title) primary key matching the
// `plugin_settings` table's UNIQUE constraint. Re-registration with the same
// key replaces the prior entry — the substrate plan allows one section per
// plugin (and re-registering is the lifecycle's idempotent update path).
type registryKey struct {
	scope string
	title string
}

// NewRegistry returns an empty registry. Callers that need built-in
// sections layered on top should add them via [Registry.Register] after
// construction.
func NewRegistry() *Registry {
	return &Registry{sections: make(map[registryKey]Section)}
}

// Register validates and stores s. Replaces any prior section with the same
// (scope, title). Returns the validation error directly so callers can map
// to HTTP 400 / RPC error codes. Validation runs at the parser boundary
// (ParseSection) for HTTP-sourced sections; Register repeats the same checks
// defensively so direct callers can't slip past — the underlying logic lives
// in validateSection so the two paths can't drift.
func (r *Registry) Register(s Section) error {
	if err := validateSection(s); err != nil {
		return err
	}
	r.mu.Lock()
	r.sections[registryKey{scope: s.Scope, title: s.Title}] = s
	r.mu.Unlock()
	return nil
}

// Unregister removes the section identified by (scope, title). Returns true
// when a row was removed; false is informational (lets the HTTP handler
// return 404 vs 200 distinctly).
func (r *Registry) Unregister(scope, title string) bool {
	if scope == "" || title == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := registryKey{scope: scope, title: title}
	if _, ok := r.sections[key]; !ok {
		return false
	}
	delete(r.sections, key)
	return true
}

// UnregisterScope removes every section owned by scope. Used by token
// revocation paths so a revoked plugin's rail entries disappear without
// requiring the plugin to clean up first. Returns the count of removed
// sections.
func (r *Registry) UnregisterScope(scope string) int {
	if scope == "" {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for k := range r.sections {
		if k.scope == scope {
			delete(r.sections, k)
			n++
		}
	}
	return n
}

// Get returns the section identified by (scope, title), if any. The returned
// Section is a copy — callers must not mutate the registry through it.
func (r *Registry) Get(scope, title string) (Section, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sections[registryKey{scope: scope, title: title}]
	return s, ok
}

// List returns every registered section sorted alphabetically by title. Ties
// are broken by scope so the output is deterministic. The TUI consumes this
// directly when building the "Plugins" portion of the rail (per the plan:
// "Plugin sections alphabetical by title").
func (r *Registry) List() []Section {
	r.mu.RLock()
	out := make([]Section, 0, len(r.sections))
	for _, s := range r.sections {
		out = append(out, s)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].Scope < out[j].Scope
	})
	return out
}

// Replace atomically swaps the entire section set. Used by the boot path
// (DB → registry rehydrate) and by tests that need to seed a known state
// in one step. Returns the count of sections retained after validation —
// invalid sections are silently dropped so a single corrupt DB row cannot
// take the registry offline; the caller is expected to log the count
// against the row count it loaded if reconciliation matters.
func (r *Registry) Replace(sections []Section) int {
	valid := make(map[registryKey]Section, len(sections))
	for _, s := range sections {
		// Reuse Register's validation by writing to a scratch registry; this
		// avoids duplicating the rule set and keeps the failure surface a
		// single source of truth.
		if err := validateSection(s); err != nil {
			continue
		}
		valid[registryKey{scope: s.Scope, title: s.Title}] = s
	}
	r.mu.Lock()
	r.sections = valid
	r.mu.Unlock()
	return len(valid)
}

// validateSection mirrors the per-field rules ParseSection applies but
// works on an already-typed Section (so it's reusable from Replace where
// the input is post-parse). Keeping it private avoids exposing two parallel
// APIs that could drift.
func validateSection(s Section) error {
	if s.Scope == "" {
		return ErrInvalidScope
	}
	if s.Title == "" {
		return ErrInvalidTitle
	}
	if s.Type != TypeForm && s.Type != TypeStream {
		return ErrInvalidType
	}
	if s.CallbackURL == "" {
		return ErrMissingCallbackURL
	}
	if s.Type == TypeStream {
		if s.Spec != nil && len(s.Spec.Fields) > 0 {
			return ErrStreamHasFields
		}
		return nil
	}
	if s.Spec == nil || len(s.Spec.Fields) == 0 {
		return ErrEmptyForm
	}
	seen := make(map[string]bool, len(s.Spec.Fields))
	for i := range s.Spec.Fields {
		f := &s.Spec.Fields[i]
		if f.Key == "" {
			return ErrFieldMissingKey
		}
		if seen[f.Key] {
			return fmt.Errorf("%w: %s", ErrFieldDuplicateKey, f.Key)
		}
		seen[f.Key] = true
		if f.Label == "" {
			return ErrFieldMissingLabel
		}
		if !f.Type.IsValid() {
			return fmt.Errorf("%w: %s", ErrFieldInvalidType, f.Type)
		}
		if err := validateFieldExtras(f); err != nil {
			return err
		}
	}
	return nil
}

// Len reports the current section count. Cheap — used by the TUI to gate
// the "Plugins" header (hide when zero).
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sections)
}

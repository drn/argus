package config

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

// FileName is the basename of the optional user TOML config under the argus
// data dir (~/.argus/config.toml).
//
// It is an OVERRIDE layer applied on top of the built-in defaults and the
// SQLite-backed settings: any field present in the file wins, and absent fields
// fall through to the DB/default value. Precedence is therefore
//
//	DefaultConfig()  <  DB (settings menu)  <  config.toml
//
// This lets power users customize beyond what the settings menu exposes
// (keybindings, theme, spinner, icons, …) — the same role alacritty.toml plays
// for Alacritty. The file is optional; a missing file changes nothing.
const FileName = "config.toml"

// FileLoader overlays a TOML config file onto a base Config. It caches the raw
// file bytes keyed by modtime+size, so the frequently-called db.Config() path
// re-reads from disk only after the file actually changes — giving
// alacritty-style live reload without a fsnotify goroutine and without
// re-reading on every call.
//
// A nil *FileLoader and a loader with an empty path are both valid no-ops, so
// in-memory/test databases that must never touch the real ~/.argus file can
// leave the loader unset.
type FileLoader struct {
	path string

	mu      sync.Mutex
	cached  []byte    // last successfully read bytes (nil when file absent)
	modTime time.Time // modtime of the cached bytes
	size    int64     // size of the cached bytes
	primed  bool      // true once a stat/read has populated the cache fields
	err     error     // last stat/read/parse error (nil when file is simply absent)

	// backendRoutingTierDefined mirrors the most recent Apply call's finding on
	// whether config.toml itself defines a non-empty [[backend_routing.tier]]
	// list — the one piece of Apply's toml.MetaData that a caller needs kept
	// (everything else about which keys decoded is otherwise discarded, see the
	// unknown-key comment in Apply). The Settings backend-tier category
	// (add-tiered-backend-routing) needs the SOURCE of the merged tier list,
	// not just its resolved value, to know whether to render read-only.
	backendRoutingTierDefined bool
}

// NewFileLoader returns a loader for the given path. An empty path makes Apply a
// no-op.
func NewFileLoader(path string) *FileLoader {
	return &FileLoader{path: path}
}

// Path returns the file path the loader reads from (empty for a no-op loader).
func (l *FileLoader) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Apply overlays the TOML file onto base, overriding only the fields present in
// the file, and returns the merged Config. A missing file (or an empty/nil
// loader) returns base unchanged. Stat/read/parse errors also return base
// unchanged and are retrievable via Err().
//
// base's map fields (Backends, Projects) are cloned before merging, so the
// caller's maps are never mutated even though TOML decoding writes into them.
func (l *FileLoader) Apply(base Config) Config {
	if l == nil || l.path == "" {
		return base
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Errors are logged once on the nil→error transition (a persistently broken
	// file must not spam the log on every — frequent — Config() call). Tracking
	// the prior error state, rather than the file-changed flag, also catches a
	// genuine stat/read failure on the very first call.
	prevErr := l.err

	data, changed, ok := l.readLocked()
	if !ok {
		l.backendRoutingTierDefined = false
		if l.err != nil && prevErr == nil {
			slog.Warn("argus config: cannot read config.toml, keeping current config", "path", l.path, "err", l.err)
		}
		return base
	}

	merged := base
	merged.Backends = cloneBackends(base.Backends)
	merged.Projects = cloneProjects(base.Projects)

	// The returned MetaData (which keys decoded) is intentionally discarded:
	// unknown/misspelled keys are silently ignored so the file stays
	// forward-compatible. Don't "fix" this into a strict decode — a typo
	// blocking the whole overlay would be worse than a silent no-op.
	meta, derr := toml.Decode(string(data), &merged)
	if derr != nil {
		l.err = fmt.Errorf("parsing %s: %w", l.path, derr)
		l.backendRoutingTierDefined = false
		if prevErr == nil {
			slog.Warn("argus config: ignoring config.toml (parse error)", "path", l.path, "err", derr)
		}
		return base
	}
	applyFileDefaults(&merged, meta)
	l.backendRoutingTierDefined = meta.IsDefined("backend_routing", "tier") && len(merged.BackendRouting.Tiers) > 0
	if changed {
		slog.Info("argus config: applied config.toml overrides", "path", l.path)
	}
	l.err = nil
	return merged
}

// DefinesBackendRoutingTiers reports whether the most recent Apply call found
// config.toml defining a non-empty [[backend_routing.tier]] list — i.e.
// whether config.toml, not the DB, is the active tier list's authoritative
// source (specs/config-management/spec.md's storage-precedence requirement).
// nil-safe (a nil loader defines nothing). Reflects only the last Apply call;
// a caller needing a fresh answer must call Apply (e.g. via db.DB.Config())
// first in the same cycle.
func (l *FileLoader) DefinesBackendRoutingTiers() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.backendRoutingTierDefined
}

// Err returns the last error encountered by Apply (a stat/read/parse failure),
// or nil. A missing file is not an error.
func (l *FileLoader) Err() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

// readLocked returns the (possibly cached) file bytes. The bool "changed"
// reports whether a fresh read happened (vs. a cache hit); "ok" is false when
// the file is absent or unreadable. On any error it sets l.err (cleared to nil
// for a simply-absent file, which is not an error). Caller must hold l.mu.
func (l *FileLoader) readLocked() (data []byte, changed, ok bool) {
	info, err := os.Stat(l.path)
	if err != nil {
		l.cached, l.size, l.modTime, l.primed = nil, 0, time.Time{}, true
		if errors.Is(err, fs.ErrNotExist) {
			l.err = nil // an absent file is the common, non-error case
		} else {
			l.err = fmt.Errorf("stat %s: %w", l.path, err)
		}
		return nil, false, false
	}

	if l.primed && l.cached != nil && info.Size() == l.size && info.ModTime().Equal(l.modTime) {
		return l.cached, false, true // cache hit
	}

	contents, err := os.ReadFile(l.path)
	if err != nil {
		l.cached, l.size, l.modTime, l.primed = nil, 0, time.Time{}, true
		l.err = fmt.Errorf("reading %s: %w", l.path, err)
		return nil, false, false
	}
	l.cached, l.size, l.modTime, l.primed = contents, info.Size(), info.ModTime(), true
	return contents, true, true
}

func cloneBackends(m map[string]Backend) map[string]Backend {
	if m == nil {
		return nil
	}
	out := make(map[string]Backend, len(m))
	maps.Copy(out, m)
	return out
}

func cloneProjects(m map[string]Project) map[string]Project {
	if m == nil {
		return nil
	}
	out := make(map[string]Project, len(m))
	maps.Copy(out, m)
	return out
}

func applyFileDefaults(cfg *Config, meta toml.MetaData) {
	if meta.IsDefined("hera", "worker_budget") && cfg.Hera.WorkerBudget.FallbackBackend == "" {
		cfg.Hera.WorkerBudget.FallbackBackend = DefaultWorkerBudgetFallbackBackend
	}
}

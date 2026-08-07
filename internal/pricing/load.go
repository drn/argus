package pricing

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

// Rate is the five per-rate-class USD-per-million-tokens price for one
// model, decoded from one [models.<alias>] table in rates.toml.
type Rate struct {
	Input        float64 `toml:"input"`
	CacheWrite1h float64 `toml:"cache_write_1h"`
	CacheWrite5m float64 `toml:"cache_write_5m"`
	CacheRead    float64 `toml:"cache_read"`
	Output       float64 `toml:"output"`
}

// Table is the decoded rates.toml shape: one Rate per model alias.
type Table struct {
	Models map[string]Rate `toml:"models"`
}

// Loader resolves the rate table from two locations: RepoPath (e.g.
// <worktree>/.argus/rates.toml) takes precedence when present, LibraryPath
// (e.g. ~/.argus/rates.toml) is the fallback. Either may be empty (skipped).
// Mirrors profiles.Loader's RepoDir/LibraryDir precedence
// (internal/profiles/load.go) applied to a single file rather than a
// named-profile directory.
type Loader struct {
	RepoPath    string
	LibraryPath string
}

// Load resolves and decodes the rate table fresh from disk on EVERY call —
// no caching, by design (design.md Decision 3): a hand-edit to whichever
// file is in effect takes effect on the very next call, with no reload
// mechanism to build or invalidate. Neither path existing is not an error —
// it yields an empty table, under which every PriceDelta call is unpriced.
func (l *Loader) Load() (*Table, error) {
	path, ok := l.locate()
	if !ok {
		return &Table{}, nil
	}
	var t Table
	if _, err := toml.DecodeFile(path, &t); err != nil {
		return nil, fmt.Errorf("loading rates %q: %w", path, err)
	}
	return &t, nil
}

// locate returns the file path to load, checking RepoPath before
// LibraryPath — mirrors profiles.Loader.locate's in-repo-first precedence.
func (l *Loader) locate() (string, bool) {
	if l.RepoPath != "" && fileExists(l.RepoPath) {
		return l.RepoPath, true
	}
	if l.LibraryPath != "" && fileExists(l.LibraryPath) {
		return l.LibraryPath, true
	}
	return "", false
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

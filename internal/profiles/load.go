package profiles

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Loader discovers and resolves profiles from two directories: an in-repo
// directory (RepoDir, e.g. <worktree>/.argus/profiles) that takes precedence,
// and a per-user library directory (LibraryDir, e.g. ~/.argus/profiles) used as
// a fallback. Either may be empty (that location is then skipped).
type Loader struct {
	RepoDir    string
	LibraryDir string
}

// rawProfile is a decoded-but-unresolved profile plus its TOML metadata, which
// is needed to do per-field overlay during extends resolution (a TOML bool/int
// that is set to its zero value is indistinguishable from an unset field
// without the metadata's IsDefined check).
type rawProfile struct {
	prof Profile
	md   toml.MetaData
}

// locate returns the file path and source for a profile name, checking the
// in-repo directory before the per-user library.
func (l *Loader) locate(name string) (path string, src Source, ok bool) {
	if l.RepoDir != "" {
		p := filepath.Join(l.RepoDir, name+".toml")
		if fileExists(p) {
			return p, SourceInRepo, true
		}
	}
	if l.LibraryDir != "" {
		p := filepath.Join(l.LibraryDir, name+".toml")
		if fileExists(p) {
			return p, SourceLibrary, true
		}
	}
	return "", "", false
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// Discover returns the sorted, de-duplicated set of profile names found on disk
// across both directories — every `<name>.toml` in RepoDir and LibraryDir, with
// the ".toml" suffix stripped. A name present in both is listed once (in-repo
// precedence is a Load-time concern; discovery only enumerates what exists).
// Used to populate the Settings project view's validated select-list and the
// new-agent prompt's Profile cycler. A missing/unreadable directory is skipped
// (not an error) — either location may be absent.
func (l *Loader) Discover() []string {
	seen := map[string]bool{}
	for _, dir := range []string{l.RepoDir, l.LibraryDir} {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			n := e.Name()
			if !strings.HasSuffix(n, ".toml") {
				continue
			}
			seen[strings.TrimSuffix(n, ".toml")] = true
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// loadRaw decodes a single profile file (no extends resolution).
func (l *Loader) loadRaw(name string) (rawProfile, error) {
	path, src, ok := l.locate(name)
	if !ok {
		return rawProfile{}, fmt.Errorf("profile %q not found", name)
	}
	var p Profile
	md, err := toml.DecodeFile(path, &p)
	if err != nil {
		return rawProfile{}, fmt.Errorf("loading profile %q: %w", name, err)
	}
	p.Name = name
	p.Source = src
	p.PanelPresent = md.IsDefined("panel")
	return rawProfile{prof: p, md: md}, nil
}

// Load loads a profile by name and resolves its `extends` chain, overlaying each
// child's declared fields onto its fully-resolved parent. The returned profile
// carries the leaf's Name and Source. An `extends` cycle (or a missing file in
// the chain) is returned as an error.
func (l *Loader) Load(name string) (*Profile, error) {
	return l.resolve(name, nil)
}

// ResolveProject resolves the profile bound to a project. An empty binding
// targets the `default` profile (the resolution target for unmapped projects).
func (l *Loader) ResolveProject(profileName string) (*Profile, error) {
	if strings.TrimSpace(profileName) == "" {
		profileName = "default"
	}
	return l.Load(profileName)
}

// resolve recursively resolves name, guarding against extends cycles. seen holds
// the chain of names currently being resolved (root first).
func (l *Loader) resolve(name string, seen []string) (*Profile, error) {
	for _, s := range seen {
		if s == name {
			chain := append(append([]string{}, seen...), name)
			return nil, fmt.Errorf("profile %q: extends cycle detected (%s)", name, strings.Join(chain, " -> "))
		}
	}

	raw, err := l.loadRaw(name)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(raw.prof.Extends) == "" {
		// Base case: no parent. Normalize the archetype map to non-nil so
		// callers can index it freely.
		p := raw.prof
		if p.Archetype == nil {
			p.Archetype = map[string]Archetype{}
		}
		return &p, nil
	}

	parent, err := l.resolve(raw.prof.Extends, append(seen, name))
	if err != nil {
		return nil, err
	}
	return overlay(parent, raw), nil
}

// overlay returns a new profile that is the parent with the child's declared
// fields applied on top. "Declared" is determined by the child's TOML metadata
// (md.IsDefined), so a child overrides only the fields it actually set —
// including bool/int fields whose zero value would otherwise be ambiguous.
func overlay(parent *Profile, child rawProfile) *Profile {
	out := *parent // shallow copy of scalar/slice/map header fields

	// Deep-copy the archetype map so the parent's map is never mutated.
	out.Archetype = make(map[string]Archetype, len(parent.Archetype))
	for k, v := range parent.Archetype {
		out.Archetype[k] = v
	}

	cp := child.prof
	md := child.md

	// Per-archetype, per-field overlay.
	for name, ca := range cp.Archetype {
		base := out.Archetype[name] // zero Archetype if the parent lacked it
		definesScalar := md.IsDefined("archetype", name, "model") || md.IsDefined("archetype", name, "effort")
		definesMenu := md.IsDefined("archetype", name, "menu")
		if md.IsDefined("archetype", name, "model") {
			base.Model = ca.Model
		}
		if md.IsDefined("archetype", name, "effort") {
			base.Effort = ca.Effort
		}
		if md.IsDefined("archetype", name, "window") {
			base.Window = ca.Window
		}
		if definesMenu {
			base.Menu = ca.Menu
		}
		// A child switching an archetype's representation (menu -> scalar or
		// scalar -> menu) must clear the parent's other representation —
		// otherwise the merged archetype carries both and trips the
		// mutual-exclusivity validation error even though neither the parent
		// nor the child alone violates it.
		if definesScalar && !definesMenu {
			base.Menu = nil
		}
		if definesMenu && !definesScalar {
			base.Model = ""
			base.Effort = ""
		}
		out.Archetype[name] = base
	}

	// Rigor: per-field overlay.
	if md.IsDefined("rigor", "review_passes") {
		out.Rigor.ReviewPasses = cp.Rigor.ReviewPasses
	}
	if md.IsDefined("rigor", "gating") {
		out.Rigor.Gating = cp.Rigor.Gating
	}
	if md.IsDefined("rigor", "security_spot_check") {
		out.Rigor.SecuritySpotCheck = cp.Rigor.SecuritySpotCheck
	}

	// Panel: the child's opaque block wins when present.
	if md.IsDefined("panel") {
		out.Panel = cp.Panel
		out.PanelPresent = true
	}

	// Carry the leaf's identity and its declared `extends` (informational).
	out.Name = cp.Name
	out.Source = cp.Source
	out.Extends = cp.Extends
	return &out
}

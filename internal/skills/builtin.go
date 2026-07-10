package skills

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/drn/argus/internal/db"
)

// builtinFS embeds argus's own skill sources — the single source of truth for
// skills that only make sense inside argus (they drive mcp__argus__* tools,
// read ARGUS_TASK_ID/~/.argus, or encode the hera coordination model). No
// runtime path, network fetch, or external repository is consulted.
//
//go:embed builtin
var builtinFS embed.FS

// builtinRoot is the embedded root directory name, stripped when walking so
// callers see skill directories at the top level (e.g. "archive", not
// "builtin/archive").
const builtinRoot = "builtin"

// managedSkillsWorkspace is the name of the directory materialized skills live
// under, inside ~/.argus. It is passed to Claude Code's --add-dir flag; Claude
// Code then loads <managedSkillsWorkspace>/.claude/skills/ automatically (a
// documented exception to --add-dir otherwise granting file access only).
const managedSkillsWorkspace = "skills"

// BuiltinItems returns one SkillItem per embedded builtin skill, sorted by
// name. Reads only the embedded FS — no filesystem or network access.
func BuiltinItems() []SkillItem {
	entries, err := fs.ReadDir(builtinFS, builtinRoot)
	if err != nil {
		return nil
	}
	var items []SkillItem
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		manifest := filepath.Join(builtinRoot, e.Name(), skillManifestFile)
		desc := readEmbeddedFrontmatterField(manifest, "description")
		items = append(items, SkillItem{Name: e.Name(), Description: desc})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

// readEmbeddedFrontmatterField mirrors readFrontmatterField but reads from
// the embedded FS instead of the OS filesystem.
func readEmbeddedFrontmatterField(path, field string) string {
	data, err := builtinFS.ReadFile(path)
	if err != nil {
		return ""
	}
	return parseFrontmatterField(data, field)
}

// EnsureBuiltinSkills materializes the embedded skill set to
// ~/.argus/skills/.claude/skills/<name>/ and returns the workspace root
// (~/.argus/skills) suitable for use as a Claude Code --add-dir argument.
//
// Materialization is idempotent and content-gated: a file is (re)written only
// when its on-disk content differs from the embedded copy, and each write is
// atomic (temp file + rename). The embedded copy always wins over a
// locally-modified on-disk copy. Skill directories present on disk but absent
// from the embedded set are removed, so the managed tree mirrors the embedded
// set exactly; removal is confined to this managed .claude/skills subtree.
func EnsureBuiltinSkills() (string, error) {
	root := filepath.Join(db.DataDir(), managedSkillsWorkspace)
	if testGuard(root) {
		return "", fmt.Errorf("ensure builtin skills: refusing to write to real data dir %q during go test", root)
	}
	skillsDir := filepath.Join(root, ".claude", "skills")

	entries, err := fs.ReadDir(builtinFS, builtinRoot)
	if err != nil {
		return "", fmt.Errorf("ensure builtin skills: read embedded root: %w", err)
	}

	embedded := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		embedded[name] = true
		if err := materializeSkillDir(filepath.Join(builtinRoot, name), filepath.Join(skillsDir, name)); err != nil {
			return "", fmt.Errorf("ensure builtin skills: materialize %q: %w", name, err)
		}
	}

	if err := pruneStaleSkillDirs(skillsDir, embedded); err != nil {
		return "", fmt.Errorf("ensure builtin skills: prune stale: %w", err)
	}

	return root, nil
}

// materializeSkillDir copies one embedded skill directory to dest, writing
// each file only when its content differs from what's already on disk.
func materializeSkillDir(embeddedDir, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	return fs.WalkDir(builtinFS, embeddedDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(embeddedDir, path)
		if err != nil {
			return err
		}
		content, err := builtinFS.ReadFile(path)
		if err != nil {
			return err
		}
		return writeIfChanged(filepath.Join(dest, rel), content)
	})
}

// writeIfChanged writes content to path atomically (temp file + rename), but
// only when the existing content differs (or the file is absent). Skipping
// unchanged files keeps repeat calls a no-op — no touched mtimes, no
// unnecessary writes.
func writeIfChanged(path string, content []byte) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, content) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".argus-skill-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()        //nolint:errcheck
		os.Remove(tmpName) //nolint:errcheck
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return err
	}
	return nil
}

// isTestBinary returns true when the current process is a Go test binary.
// Kept in sync with the identical copies in internal/agent/cleanup.go,
// internal/daemon/client/client.go, and internal/api/selfupdate.go — same
// detection, duplicated per package so each can refuse without importing
// across package boundaries.
func isTestBinary() bool {
	return strings.HasSuffix(os.Args[0], ".test") ||
		strings.Contains(os.Args[0], "/_test/")
}

// isRealDataDir returns true if path is under the real ~/.argus/ directory
// (not a test temp dir).
func isRealDataDir(path string) bool {
	cleaned := filepath.Clean(path)
	realData := filepath.Clean(db.DataDir())
	return strings.HasPrefix(cleaned, realData+string(filepath.Separator)) || cleaned == realData
}

// testGuard returns true if we're running inside "go test" and path targets
// the real ~/.argus/ directory, mirroring internal/agent/cleanup.go's guard
// against tests touching real state. Paths under os.TempDir() are always
// allowed — tests that t.Setenv("HOME", t.TempDir()) legitimately need
// EnsureBuiltinSkills to materialize into their synthetic data dir.
func testGuard(path string) bool {
	if !isTestBinary() {
		return false
	}
	cleaned := filepath.Clean(path)
	tmpRoot := filepath.Clean(os.TempDir()) + string(filepath.Separator)
	if strings.HasPrefix(cleaned, tmpRoot) {
		return false
	}
	return isRealDataDir(path)
}

// pruneStaleSkillDirs removes skill directories under skillsDir that are no
// longer part of the embedded set, so renaming or dropping a builtin skill
// doesn't leave a ghost directory behind. A missing skillsDir is not an error
// (nothing to prune yet).
func pruneStaleSkillDirs(skillsDir string, embedded map[string]bool) error {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || embedded[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(skillsDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

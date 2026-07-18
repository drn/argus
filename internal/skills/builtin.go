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

// EnsureBuiltinSkills materializes embedded builtin skills to the managed
// workspace (~/.argus/skills/.claude/skills) idempotently. Updates existing
// files on content drift; removes stale skills no longer in the embedded set.
// Errors are logged but non-fatal (failure to materialize one skill doesn't
// block others).
func EnsureBuiltinSkills(dataDir string) error {
	if isTestBinary() || dataDir == "" {
		return nil
	}
	workspaceRoot := filepath.Join(dataDir, managedSkillsWorkspace, ".claude", "skills")
	if err := os.MkdirAll(workspaceRoot, 0755); err != nil {
		return fmt.Errorf("create workspace dir: %w", err)
	}

	// Materialize each builtin skill.
	builtins := BuiltinItems()
	present := make(map[string]bool, len(builtins))
	for _, item := range builtins {
		skillDir := filepath.Join(builtinRoot, item.Name)
		entries, err := fs.ReadDir(builtinFS, skillDir)
		if err != nil {
			continue
		}
		targetDir := filepath.Join(workspaceRoot, item.Name)
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			continue
		}
		present[item.Name] = true
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			src := filepath.Join(skillDir, e.Name())
			data, err := fs.ReadFile(builtinFS, src)
			if err != nil {
				continue
			}
			dst := filepath.Join(targetDir, e.Name())
			_ = atomicWriteIfDifferent(dst, data)
		}
	}

	// Remove stale skills.
	existingDirs, _ := os.ReadDir(workspaceRoot)
	for _, d := range existingDirs {
		if d.IsDir() && !present[d.Name()] {
			os.RemoveAll(filepath.Join(workspaceRoot, d.Name()))
		}
	}
	return nil
}

// atomicWriteIfDifferent writes data to path only if the current file content
// differs. Uses a temp file + rename pattern to avoid partial writes.
func atomicWriteIfDifferent(path string, data []byte) error {
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readEmbeddedFrontmatterField extracts a YAML field value from the frontmatter
// of an embedded file. Returns "" if the file doesn't exist or the field is missing.
func readEmbeddedFrontmatterField(path, field string) string {
	data, err := fs.ReadFile(builtinFS, path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "---") {
		return ""
	}
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if line == "---" {
			break
		}
		prefix := field + ":"
		if strings.HasPrefix(line, prefix) {
			val := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			val = strings.Trim(val, "\"'")
			return val
		}
	}
	return ""
}

// isTestBinary returns true if the current process is a test executable.
func isTestBinary() bool {
	return strings.HasSuffix(os.Args[0], ".test")
}

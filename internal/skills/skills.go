package skills

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillItem represents a discovered Claude Code skill or slash command.
// Plugin-provided items use a "<plugin>:<name>" form (e.g. "cortex:review")
// to match how Claude Code exposes them as slash commands.
type SkillItem struct {
	Name        string
	Description string
}

// LoadSkills scans for skill and slash-command definitions in:
//   - each extraDir (project-scoped skills) — highest priority on name collision
//   - ~/.claude/skills/<name>/SKILL.md (user skills)
//   - installed plugins under ~/.claude/plugins/cache/... via installed_plugins.json,
//     exposing both plugin commands (commands/*.md) and skills (skills/**/SKILL.md)
//     as "<plugin>:<name>" entries.
func LoadSkills(extraDirs []string) []SkillItem {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var items []SkillItem

	// Bare skill directories (project + user). Earlier dirs win on collision.
	for _, dir := range extraDirs {
		items = append(items, loadSkillDirs(dir, "", seen)...)
	}
	items = append(items, loadSkillDirs(filepath.Join(home, ".claude", "skills"), "", seen)...)

	// Plugin-provided commands and skills, namespaced as "<plugin>:<name>".
	items = append(items, loadPluginItems(home, seen)...)

	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items
}

// loadSkillDirs scans dir for <name>/SKILL.md entries and returns SkillItems
// named "<prefix><name>". Names already in seen are skipped.
func loadSkillDirs(dir, prefix string, seen map[string]bool) []SkillItem {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var items []SkillItem
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := prefix + e.Name()
		if seen[name] {
			continue
		}
		seen[name] = true
		desc := readFrontmatterField(filepath.Join(dir, e.Name(), "SKILL.md"), "description")
		items = append(items, SkillItem{Name: name, Description: desc})
	}
	return items
}

// loadPluginItems reads ~/.claude/plugins/installed_plugins.json and, for each
// installed plugin, exposes its commands (commands/*.md) and skills (any
// SKILL.md under skills/, recursively) as "<plugin>:<name>" SkillItems.
func loadPluginItems(home string, seen map[string]bool) []SkillItem {
	manifestPath := filepath.Join(home, ".claude", "plugins", "installed_plugins.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil
	}
	var manifest struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil
	}

	// Stable plugin order so output is deterministic even before the final sort.
	keys := make([]string, 0, len(manifest.Plugins))
	for k := range manifest.Plugins {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var items []SkillItem
	for _, key := range keys {
		installs := manifest.Plugins[key]
		if len(installs) == 0 || installs[0].InstallPath == "" {
			continue
		}
		// Key is "<plugin>@<marketplace>"; the plugin name is the slash-command prefix.
		plugin, _, _ := strings.Cut(key, "@")
		if plugin == "" {
			continue
		}
		root := installs[0].InstallPath
		items = append(items, loadPluginCommands(root, plugin, seen)...)
		items = append(items, loadPluginSkills(root, plugin, seen)...)
	}
	return items
}

// loadPluginCommands returns each commands/*.md file as "<plugin>:<basename>".
func loadPluginCommands(root, plugin string, seen map[string]bool) []SkillItem {
	dir := filepath.Join(root, "commands")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var items []SkillItem
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := plugin + ":" + strings.TrimSuffix(e.Name(), ".md")
		if seen[name] {
			continue
		}
		seen[name] = true
		desc := readFrontmatterField(filepath.Join(dir, e.Name()), "description")
		items = append(items, SkillItem{Name: name, Description: desc})
	}
	return items
}

// loadPluginSkills walks the plugin's skills/ directory (following symlinks,
// since Claude Code ships plugin skills as a symlink into the marketplace
// checkout) and returns each SKILL.md as "<plugin>:<frontmatter-name>".
func loadPluginSkills(root, plugin string, seen map[string]bool) []SkillItem {
	skillRoot := filepath.Join(root, "skills")
	if resolved, err := filepath.EvalSymlinks(skillRoot); err == nil {
		skillRoot = resolved
	}
	var items []SkillItem
	_ = filepath.WalkDir(skillRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip unreadable subtrees rather than aborting the whole walk.
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		skillName := readFrontmatterField(path, "name")
		if skillName == "" {
			skillName = filepath.Base(filepath.Dir(path))
		}
		full := plugin + ":" + skillName
		if seen[full] {
			return nil
		}
		seen[full] = true
		items = append(items, SkillItem{
			Name:        full,
			Description: readFrontmatterField(path, "description"),
		})
		return nil
	})
	return items
}

// readFrontmatterField reads a single top-level field from a YAML-ish
// frontmatter block fenced by "---" lines at the start of the file. Quotes
// around the value are stripped. Returns "" if the file cannot be opened or
// the field is absent.
func readFrontmatterField(path, field string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	prefix := field + ":"
	inFrontmatter := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			if !inFrontmatter {
				inFrontmatter = true
				continue
			}
			break
		}
		if inFrontmatter && strings.HasPrefix(line, prefix) {
			val := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			val = strings.Trim(val, `"'`)
			return val
		}
	}
	return ""
}

// FilterSkills returns skills whose names have the given prefix.
// If prefix is empty, all skills are returned.
func FilterSkills(items []SkillItem, prefix string) []SkillItem {
	if prefix == "" {
		return items
	}
	var out []SkillItem
	for _, s := range items {
		if strings.HasPrefix(s.Name, prefix) {
			out = append(out, s)
		}
	}
	return out
}

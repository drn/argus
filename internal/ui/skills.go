package ui

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// acMaxVisible is the maximum number of autocomplete rows shown at once.
const acMaxVisible = 6

// SkillItem represents a discovered Claude Code skill.
type SkillItem struct {
	Name        string
	Description string
}

// LoadSkills scans ~/.claude/skills/ and any extraDirs for skill directories.
// Skills in extraDirs take precedence (earlier dirs win on name collision).
func LoadSkills(extraDirs []string) []SkillItem {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	dirs := make([]string, 0, len(extraDirs)+1)
	dirs = append(dirs, extraDirs...)
	dirs = append(dirs, filepath.Join(home, ".claude", "skills"))

	seen := make(map[string]bool)
	var skills []SkillItem

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || seen[e.Name()] {
				continue
			}
			seen[e.Name()] = true
			desc := readSkillDesc(filepath.Join(dir, e.Name(), "SKILL.md"))
			skills = append(skills, SkillItem{Name: e.Name(), Description: desc})
		}
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills
}

// readSkillDesc reads the description field from a SKILL.md frontmatter block.
func readSkillDesc(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
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
		if inFrontmatter && strings.HasPrefix(line, "description:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	return ""
}

// filterSkills returns skills whose names have the given prefix.
// If prefix is empty, all skills are returned.
func filterSkills(skills []SkillItem, prefix string) []SkillItem {
	if prefix == "" {
		return skills
	}
	var out []SkillItem
	for _, s := range skills {
		if strings.HasPrefix(s.Name, prefix) {
			out = append(out, s)
		}
	}
	return out
}

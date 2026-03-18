// Package imports provides Obsidian vault task parsing for Argus.
package imports

import (
	"regexp"
	"strings"

	"github.com/drn/argus/internal/model"
)

// ParsedTask holds a task parsed from Obsidian vault markdown.
type ParsedTask struct {
	Name       string
	Project    string
	SourceFile string
}

// taskLineRe matches Obsidian unchecked task lines with the #argus tag.
// The #argus tag is REQUIRED — tasks without it are ignored.
// Formats supported:
//   - [ ] Task name  #argus
//   - [ ] Task name  #argus/project-name
var taskLineRe = regexp.MustCompile(`(?m)^- \[ \] (.+?)\s+#argus(?:/([^\s]+))?(?:\s.*)?$`)

// ParseTasks extracts Argus task syntax from markdown content.
// Looks for: - [ ] Task name  #argus or - [ ] Task name  #argus/project-name
// Also honours [project:: project-name] Dataview syntax.
func ParseTasks(content, sourceFile string) []ParsedTask {
	var tasks []ParsedTask
	seen := make(map[string]bool)

	matches := taskLineRe.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		rawName := strings.TrimSpace(m[1])
		project := strings.TrimSpace(m[2])

		// Strip trailing #argus tag if it wasn't captured as a suffix.
		rawName = strings.TrimSuffix(rawName, "#argus")
		rawName = strings.TrimSpace(rawName)

		// Check for Dataview [project:: name] syntax in the task text.
		if project == "" {
			project = extractDataviewField(rawName, "project")
		}

		// Remove Dataview syntax from the task name.
		rawName = removeDataviewFields(rawName)

		if rawName == "" {
			continue
		}

		key := sourceFile + "::" + rawName
		if seen[key] {
			continue
		}
		seen[key] = true

		tasks = append(tasks, ParsedTask{
			Name:       rawName,
			Project:    project,
			SourceFile: sourceFile,
		})
	}
	return tasks
}

// ToTask converts a ParsedTask to a model.Task ready for db.Add.
func (p ParsedTask) ToTask() model.Task {
	return model.Task{
		Name:    p.Name,
		Project: p.Project,
		Status:  model.StatusPending,
	}
}

// extractDataviewField extracts the value of a [key:: value] field.
func extractDataviewField(text, key string) string {
	prefix := "[" + key + "::"
	idx := strings.Index(text, prefix)
	if idx == -1 {
		return ""
	}
	rest := text[idx+len(prefix):]
	end := strings.Index(rest, "]")
	if end == -1 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// removeDataviewFields strips all [key:: value] patterns from text.
func removeDataviewFields(text string) string {
	// Simple approach: remove balanced [ ... ] blocks containing "::"
	var result strings.Builder
	i := 0
	for i < len(text) {
		if text[i] == '[' {
			end := strings.Index(text[i:], "]")
			if end != -1 && strings.Contains(text[i:i+end+1], "::") {
				i += end + 1
				continue
			}
		}
		result.WriteByte(text[i])
		i++
	}
	return strings.TrimSpace(result.String())
}

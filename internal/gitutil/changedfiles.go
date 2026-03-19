package gitutil

import (
	"sort"
	"strings"
)

// ParseGitStatus parses `git status --short` output into ChangedFile entries.
func ParseGitStatus(output string) []ChangedFile {
	if output == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	var files []ChangedFile
	for _, line := range lines {
		if len(line) < 4 {
			continue
		}
		status := strings.TrimSpace(line[:2])
		path := strings.TrimSpace(line[3:])
		if path != "" {
			isDir := strings.HasSuffix(path, "/")
			files = append(files, ChangedFile{Status: status, Path: path, IsDir: isDir})
		}
	}
	return files
}

// ParseGitDiffNameStatus parses `git diff --name-status` output into ChangedFile entries.
func ParseGitDiffNameStatus(output string) []ChangedFile {
	if output == "" {
		return nil
	}
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	var files []ChangedFile
	for _, line := range lines {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		status := strings.TrimSpace(parts[0])
		path := strings.TrimSpace(parts[1])
		if path != "" {
			isDir := strings.HasSuffix(path, "/")
			files = append(files, ChangedFile{Status: status, Path: path, IsDir: isDir})
		}
	}
	return files
}

// MergeChangedFiles merges two file lists into a single deduplicated, sorted slice.
// Files in overlay take precedence over files in base when the same path appears in both.
// This is used to combine committed branch files (base) with uncommitted changes (overlay)
// so the file explorer always shows the full picture of what changed on the branch.
func MergeChangedFiles(base, overlay []ChangedFile) []ChangedFile {
	if len(base) == 0 && len(overlay) == 0 {
		return nil
	}
	seen := make(map[string]ChangedFile, len(base)+len(overlay))
	for _, f := range base {
		seen[f.Path] = f
	}
	for _, f := range overlay {
		seen[f.Path] = f // overlay wins on conflict
	}
	result := make([]ChangedFile, 0, len(seen))
	for _, f := range seen {
		result = append(result, f)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path < result[j].Path
	})
	return result
}

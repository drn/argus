// Package codex provides Codex MCP config injection.
package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// InjectGlobal reads ~/.codex/config.toml and adds/updates the argus-kb MCP
// server entry. Idempotent — only writes if the entry is absent or changed.
func InjectGlobal(port int) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("inject codex global: user home dir: %w", err)
	}
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return injectCodexTOML(path, port)
}

// InjectWorktree writes a .codex/config.toml to the worktree for project-scope config.
func InjectWorktree(worktreePath string, port int) error {
	dir := filepath.Join(worktreePath, ".codex")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	path := filepath.Join(dir, "config.toml")
	return injectCodexTOML(path, port)
}

// injectCodexTOML inserts or updates the [mcp_servers.argus-kb] section.
// Uses targeted string manipulation to avoid pulling in a TOML library.
func injectCodexTOML(path string, port int) error {
	url := fmt.Sprintf("http://localhost:%d/mcp", port)

	raw, err := os.ReadFile(path)
	var content string
	if err == nil {
		content = string(raw)
	}

	// Check if already correct.
	if strings.Contains(content, "[mcp_servers.argus-kb]") {
		// Find the url line in the section and check its value.
		idx := strings.Index(content, "[mcp_servers.argus-kb]")
		section := content[idx:]
		// Find the end of this section (next [ or EOF).
		end := strings.Index(section[1:], "\n[")
		var sectionBody string
		if end == -1 {
			sectionBody = section
		} else {
			sectionBody = section[:end+1]
		}
		wantLine := fmt.Sprintf(`url = "%s"`, url)
		if strings.Contains(sectionBody, wantLine) {
			return nil // already correct
		}
		// Port changed — remove old section and re-add below.
		content = removeSection(content, "[mcp_servers.argus-kb]")
	}

	// Ensure experimental_use_rmcp_client is present.
	if !strings.Contains(content, "experimental_use_rmcp_client") {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		content += "experimental_use_rmcp_client = true\n"
	}

	// Append the section.
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += fmt.Sprintf("\n[mcp_servers.argus-kb]\nurl = %q\n", url)

	// Atomic write: write to temp file then rename to avoid partial reads.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".argus-codex-*.tmp")
	if err != nil {
		return fmt.Errorf("inject codex: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck — cleanup on failure
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return fmt.Errorf("inject codex: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("inject codex: close temp: %w", err)
	}
	return os.Rename(tmpName, path)
}

// removeSection removes a TOML section header and its key-value lines.
// section is the header line, e.g. "[mcp_servers.argus-kb]".
func removeSection(content, section string) string {
	idx := strings.Index(content, section)
	if idx == -1 {
		return content
	}
	// Find the next section header after this one.
	rest := content[idx+len(section):]
	nextSection := strings.Index(rest, "\n[")
	if nextSection == -1 {
		// This is the last section — trim from the header backwards to preceding newline.
		before := content[:idx]
		before = strings.TrimRight(before, "\n")
		return before + "\n"
	}
	return content[:idx] + rest[nextSection+1:]
}

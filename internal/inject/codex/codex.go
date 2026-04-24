// Package codex provides Codex MCP config injection.
package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// mcpSection is the TOML header for the Argus MCP entry in Codex config.
// Previously "[mcp_servers.argus-kb]"; renamed to "[mcp_servers.argus]" now
// that the server exposes task tools alongside KB. The legacy section is
// cleaned up on inject.
const mcpSection = "[mcp_servers.argus]"

// legacyMcpSection is the old pre-rename header, removed on the next inject.
const legacyMcpSection = "[mcp_servers.argus-kb]"

// InjectGlobal reads ~/.codex/config.toml and adds/updates the argus MCP
// server entry. Idempotent — only writes if the entry is absent or changed.
// Also removes the legacy [mcp_servers.argus-kb] section if present.
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

// injectCodexTOML inserts or updates the [mcp_servers.argus] section and
// removes any pre-existing [mcp_servers.argus-kb] section. Uses targeted
// string manipulation to avoid pulling in a TOML library. Assumes standard
// Codex-generated TOML — no multi-line values, no inline tables.
func injectCodexTOML(path string, port int) error {
	url := fmt.Sprintf("http://localhost:%d/mcp", port)

	raw, err := os.ReadFile(path)
	var content string
	if err == nil {
		content = string(raw)
	}

	// Migrate: drop the legacy section unconditionally.
	if strings.Contains(content, legacyMcpSection) {
		content = removeSection(content, legacyMcpSection)
	}

	// Check if MCP section already exists.
	urlCorrect := false
	if strings.Contains(content, mcpSection) {
		// Find the url line in the section and check its value.
		idx := strings.Index(content, mcpSection)
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
			urlCorrect = true
		} else {
			// Port changed — remove old section and re-add below.
			content = removeSection(content, mcpSection)
		}
	}

	// Ensure experimental_use_rmcp_client = true is at the TOML top level.
	// Must appear before the first [section] header — appending to the end
	// places it inside the last section (e.g. [notice.model_migrations]),
	// causing a type error ("expected a string" for boolean true).
	updated := ensureTopLevel(content, "experimental_use_rmcp_client", "experimental_use_rmcp_client = true")

	if !urlCorrect {
		// Append the MCP server section.
		if updated != "" && !strings.HasSuffix(updated, "\n") {
			updated += "\n"
		}
		updated += fmt.Sprintf("\n%s\nurl = %q\n", mcpSection, url)
	}

	if updated == content {
		return nil // nothing changed
	}

	// Atomic write: write to temp file then rename to avoid partial reads.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".argus-codex-*.tmp")
	if err != nil {
		return fmt.Errorf("inject codex: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) //nolint:errcheck — cleanup on failure
	if _, err := tmp.WriteString(updated); err != nil {
		tmp.Close()
		return fmt.Errorf("inject codex: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("inject codex: close temp: %w", err)
	}
	return os.Rename(tmpName, path)
}

// ensureTopLevel ensures a key=value line exists at the TOML top level
// (before the first [section] header). If the key exists only inside a
// section, it is removed and re-inserted at the top level.
func ensureTopLevel(content, key, line string) string {
	// Determine where the top-level (pre-section) area ends.
	// Handle files that start directly with a section header (no top-level area).
	firstSection := strings.Index(content, "\n[")
	topLevel := content
	if strings.HasPrefix(content, "[") {
		topLevel = ""
		firstSection = 0 // entire content is sections
	} else if firstSection != -1 {
		topLevel = content[:firstSection]
	}
	if strings.Contains(topLevel, key) {
		return content // already at top level
	}
	// Remove from wrong section if present.
	content = removeLine(content, key)
	// Insert at top level (before first section header).
	if strings.HasPrefix(content, "[") {
		return line + "\n" + content
	}
	firstSection = strings.Index(content, "\n[")
	if firstSection == -1 {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + line + "\n"
	}
	return content[:firstSection+1] + line + "\n" + content[firstSection+1:]
}

// removeLine removes all lines containing substr (substring match) from content.
func removeLine(content, substr string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if !strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// removeSection removes a TOML section header and its key-value lines.
// section is the header line, e.g. "[mcp_servers.argus]".
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

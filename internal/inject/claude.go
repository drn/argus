// Package inject provides idempotent MCP config injection for Claude Code and Codex.
package inject

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// mcpServerName is the key used for Argus in Claude Code's mcpServers map.
// Previously "argus-kb"; renamed to "argus" now that the server exposes both
// KB and task tools. The legacy key is cleaned up on inject.
const mcpServerName = "argus"

// legacyMcpServerName is the old pre-rename key, removed on the next inject.
const legacyMcpServerName = "argus-kb"

// InjectGlobal reads ~/.claude.json, adds/updates the argus MCP server entry,
// and writes the file back. Idempotent — only writes if the entry is absent or
// the port has changed. Also removes the legacy "argus-kb" entry if present.
func InjectGlobal(port int) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("inject claude global: user home dir: %w", err)
	}
	path := filepath.Join(home, ".claude.json")
	return injectClaudeJSON(path, port)
}

// injectClaudeJSON mutates only the mcpServers.argus key in the given JSON
// file, and removes the legacy mcpServers.argus-kb key if present. All other
// keys are preserved verbatim.
func injectClaudeJSON(path string, port int) error {
	var data map[string]interface{}

	raw, err := os.ReadFile(path)
	if err == nil {
		if jsonErr := json.Unmarshal(raw, &data); jsonErr != nil {
			// File exists but is not valid JSON — don't touch it.
			return fmt.Errorf("inject claude: cannot parse %s: %w", path, jsonErr)
		}
	}
	if data == nil {
		data = make(map[string]interface{})
	}

	mcpServers, _ := data["mcpServers"].(map[string]interface{})
	if mcpServers == nil {
		mcpServers = make(map[string]interface{})
	}

	url := fmt.Sprintf("http://localhost:%d/mcp", port)

	_, hasLegacy := mcpServers[legacyMcpServerName]

	// Check if already correct (and no legacy cleanup pending).
	if existing, ok := mcpServers[mcpServerName].(map[string]interface{}); ok && !hasLegacy {
		if existing["url"] == url && existing["type"] == "http" {
			return nil // already correct
		}
	}

	mcpServers[mcpServerName] = map[string]interface{}{"type": "http", "url": url}
	delete(mcpServers, legacyMcpServerName)
	data["mcpServers"] = mcpServers

	return writeJSON(path, data)
}

// writeJSON marshals data as indented JSON and writes it to path atomically.
// Uses os.CreateTemp for a unique tempfile name so concurrent writers do not
// clobber each other before the rename.
func writeJSON(path string, data map[string]interface{}) error {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("inject: marshal: %w", err)
	}
	out = append(out, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".argus-inject-*.tmp")
	if err != nil {
		return fmt.Errorf("inject: create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()        //nolint:errcheck
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("inject: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("inject: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("inject: rename: %w", err)
	}
	return nil
}

// SetClaudeProjectMcpTrust writes enableAllProjectMcpServers: true to
// ~/.claude/settings.json so the first-use MCP approval prompt is suppressed.
func SetClaudeProjectMcpTrust() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	var data map[string]interface{}
	raw, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(raw, &data) //nolint:errcheck
	}
	if data == nil {
		data = make(map[string]interface{})
	}

	// Already set — no write needed.
	if v, ok := data["enableAllProjectMcpServers"].(bool); ok && v {
		return nil
	}

	data["enableAllProjectMcpServers"] = true
	return writeJSON(path, data)
}

// Package inject provides idempotent MCP config injection for Claude Code and Codex.
package inject

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// InjectGlobal reads ~/.claude.json, adds/updates the argus-kb MCP server entry,
// and writes the file back. Idempotent — only writes if the entry is absent or
// the port has changed.
func InjectGlobal(port int) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("inject claude global: user home dir: %w", err)
	}
	path := filepath.Join(home, ".claude.json")
	return injectClaudeJSON(path, port)
}

// InjectWorktree writes a .mcp.json file to the worktree root so the KB MCP
// server is available at the project scope as well.
// Idempotent — only writes if the entry is absent or port has changed.
func InjectWorktree(worktreePath string, port int) error {
	mcpPath := filepath.Join(worktreePath, ".mcp.json")
	return injectMCPJSON(mcpPath, port)
}

// injectClaudeJSON mutates only the mcpServers.argus-kb key in the given JSON file.
// All other keys are preserved verbatim.
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

	// Check if already correct.
	if existing, ok := mcpServers["argus-kb"].(map[string]interface{}); ok {
		if existing["url"] == url {
			return nil // already correct
		}
	}

	mcpServers["argus-kb"] = map[string]interface{}{"url": url}
	data["mcpServers"] = mcpServers

	return writeJSON(path, data)
}

// injectMCPJSON writes a minimal .mcp.json with only the argus-kb entry.
// If the file exists with other entries, they are preserved.
func injectMCPJSON(path string, port int) error {
	var data map[string]interface{}

	raw, err := os.ReadFile(path)
	if err == nil {
		json.Unmarshal(raw, &data) //nolint:errcheck
	}
	if data == nil {
		data = make(map[string]interface{})
	}

	mcpServers, _ := data["mcpServers"].(map[string]interface{})
	if mcpServers == nil {
		mcpServers = make(map[string]interface{})
	}

	url := fmt.Sprintf("http://localhost:%d/mcp", port)
	if existing, ok := mcpServers["argus-kb"].(map[string]interface{}); ok {
		if existing["url"] == url {
			return nil
		}
	}

	mcpServers["argus-kb"] = map[string]interface{}{"url": url}
	data["mcpServers"] = mcpServers

	return writeJSON(path, data)
}

// writeJSON marshals data as indented JSON and writes it to path atomically.
func writeJSON(path string, data map[string]interface{}) error {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("inject: marshal: %w", err)
	}
	out = append(out, '\n')

	// Atomic write via temp file.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0644); err != nil {
		return fmt.Errorf("inject: write temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck
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

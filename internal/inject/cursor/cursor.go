// Package cursor registers Argus with the Cursor CLI. cursor-agent has no
// per-launch MCP flag, so Argus edits ~/.cursor/mcp.json idempotently.
package cursor

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/drn/argus/internal/inject/atomicfile"
)

const (
	mcpServerName = "argus"
	label         = "inject cursor"
)

// ConfigPath returns Cursor's global MCP config, ~/.cursor/mcp.json.
func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%s: user home dir: %w", label, err)
	}
	return filepath.Join(home, ".cursor", "mcp.json"), nil
}

// InjectGlobal sets mcpServers.argus = {"url": ...} (skipped when port <= 0).
// Unrelated keys and an operator's `disabled` choice are preserved, an
// already-correct entry is left alone, and an unparseable file is untouched.
func InjectGlobal(port int) error {
	if port <= 0 {
		return nil
	}
	path, err := ConfigPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("%s: create %s: %w", label, filepath.Dir(path), err)
	}
	data, err := atomicfile.ReadJSONObject(path, label)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://localhost:%d/mcp", port)
	servers := map[string]any{}
	if raw, present := data["mcpServers"]; present && raw != nil {
		m, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: %s mcpServers is %T, not an object; leaving it untouched", label, path, raw)
		}
		servers = m
	}
	entry := map[string]any{"url": url}
	if existing, ok := servers[mcpServerName].(map[string]any); ok {
		if existing["url"] == url {
			return nil
		}
		for k, v := range existing {
			if k != "url" {
				entry[k] = v
			}
		}
	}
	servers[mcpServerName] = entry
	data["mcpServers"] = servers
	return atomicfile.WriteJSON(path, data, label)
}

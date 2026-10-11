// Package antigravity registers Argus with the Antigravity CLI (agy). agy has
// no per-launch MCP flag or config-dir override — it reads only
// $HOME/.gemini/config — so Argus edits two global files idempotently:
// mcp_config.json (the MCP server) and skills.json (the builtin skills dir).
package antigravity

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/drn/argus/internal/inject/atomicfile"
)

const (
	mcpServerName = "argus"
	label         = "inject antigravity"
)

// ConfigDir returns agy's global customization root, ~/.gemini/config.
func ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%s: user home dir: %w", label, err)
	}
	return filepath.Join(home, ".gemini", "config"), nil
}

// InjectGlobal registers Argus's MCP server on port and, when skillsDir is
// non-empty, Argus's builtin skills dir. Both writes preserve every
// unrelated key and are skipped when already correct. An unparseable file is
// left untouched and reported.
func InjectGlobal(port int, skillsDir string) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("%s: create %s: %w", label, dir, err)
	}
	mcpErr := injectMCP(filepath.Join(dir, "mcp_config.json"), port)
	if skillsDir == "" {
		return mcpErr
	}
	if err := injectSkills(filepath.Join(dir, "skills.json"), skillsDir); err != nil {
		if mcpErr != nil {
			return fmt.Errorf("%w; %w", mcpErr, err)
		}
		return err
	}
	return mcpErr
}

// injectMCP sets mcpServers.argus = {"serverUrl": url, "disabled": false}.
// serverUrl is what agy's own `agy mcp add` writes; current agy also reads
// "url", so an entry using either key is accepted as correct. An operator's
// `agy mcp disable argus` is respected: an existing entry's disabled flag is
// carried over when the URL is refreshed, never reset.
func injectMCP(path string, port int) error {
	data, err := atomicfile.ReadJSONObject(path, label)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://localhost:%d/mcp", port)
	servers, _ := data["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	disabled := false
	if existing, ok := servers[mcpServerName].(map[string]any); ok {
		if existing["serverUrl"] == url || existing["url"] == url {
			return nil
		}
		disabled = existing["disabled"] == true
	}
	servers[mcpServerName] = map[string]any{"serverUrl": url, "disabled": disabled}
	data["mcpServers"] = servers
	return atomicfile.WriteJSON(path, data, label)
}

// injectSkills ensures skills.json's entries array holds {"path": skillsDir}.
// agy scans each entry directory one level deep for <name>/SKILL.md.
func injectSkills(path, skillsDir string) error {
	data, err := atomicfile.ReadJSONObject(path, label)
	if err != nil {
		return err
	}
	var entries []any
	if raw, present := data["entries"]; present && raw != nil {
		arr, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%s: %s entries is %T, not an array; leaving it untouched", label, path, raw)
		}
		entries = arr
	}
	for _, e := range entries {
		if m, ok := e.(map[string]any); ok && m["path"] == skillsDir {
			return nil
		}
	}
	data["entries"] = append(entries, map[string]any{"path": skillsDir})
	return atomicfile.WriteJSON(path, data, label)
}

// Package opencode provides idempotent MCP config injection for the opencode CLI.
package opencode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// mcpServerName is the key used for Argus in opencode's `mcp` object.
const mcpServerName = "argus"

// InjectGlobal reads opencode's global config, adds/updates the argus MCP
// server entry under the `mcp` object, and writes the file back. Idempotent —
// only writes if the entry is absent or the port has changed. All other keys
// (including unrelated mcp entries and top-level config) are preserved verbatim.
// The config lives at $XDG_CONFIG_HOME/opencode/opencode.json when
// XDG_CONFIG_HOME is set, else ~/.config/opencode/opencode.json — mirroring
// opencode's own xdg-basedir resolution (and the XDG_DATA_HOME awareness on the
// capture side).
//
// When skillsDir is non-empty, InjectGlobal additionally ensures it is present
// in opencode's own `skills` config array, so opencode's native skill discovery
// finds argus's builtin skills without relying on the generic cross-tool
// `.agents/skills` convention (see
// openspec/changes/add-nonclaude-context-parity/design.md Decision 2). Pass ""
// to skip this — e.g. when skill materialization itself failed.
func InjectGlobal(port int, skillsDir string) error {
	configHome := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("inject opencode global: user home dir: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	path := filepath.Join(configHome, "opencode", "opencode.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return injectOpencodeJSON(path, port, skillsDir)
}

// injectOpencodeJSON mutates the mcp.argus key and, when skillsDir is
// non-empty, ensures skillsDir is present in the `skills` array, in the given
// JSON file. All other keys — including other `skills` entries — are
// preserved. When the file exists but is not valid JSON it is left untouched
// and an error is returned.
func injectOpencodeJSON(path string, port int, skillsDir string) error {
	var data map[string]any

	raw, err := os.ReadFile(path)
	if err == nil {
		if jsonErr := json.Unmarshal(raw, &data); jsonErr != nil {
			// File exists but is not valid JSON — don't touch it.
			return fmt.Errorf("inject opencode: cannot parse %s: %w", path, jsonErr)
		}
	}
	if data == nil {
		data = make(map[string]any)
	}

	mcp, _ := data["mcp"].(map[string]any)
	if mcp == nil {
		mcp = make(map[string]any)
	}

	url := fmt.Sprintf("http://localhost:%d/mcp", port)

	mcpCorrect := false
	if existing, ok := mcp[mcpServerName].(map[string]any); ok {
		mcpCorrect = existing["url"] == url && existing["type"] == "remote" && existing["enabled"] == true
	}

	skills, skillsCorrect := ensureSkillsEntry(data["skills"], skillsDir)

	if mcpCorrect && skillsCorrect {
		return nil
	}

	mcp[mcpServerName] = map[string]any{
		"type":    "remote",
		"url":     url,
		"enabled": true,
	}
	data["mcp"] = mcp
	if skills != nil {
		data["skills"] = skills
	}

	return writeJSON(path, data)
}

// ensureSkillsEntry returns the skills array that should be written (nil if
// there's nothing to change) and whether the existing array already contains
// skillsDir (or skillsDir is empty, meaning nothing to ensure). existing is
// the raw `skills` value decoded from JSON (an []any of strings, or nil/absent).
func ensureSkillsEntry(existing any, skillsDir string) (result []any, alreadyCorrect bool) {
	arr, _ := existing.([]any)
	if skillsDir == "" {
		return arr, true
	}
	for _, s := range arr {
		if s == skillsDir {
			return arr, true
		}
	}
	return append(arr, skillsDir), false
}

// writeJSON marshals data as indented JSON and writes it to path atomically.
func writeJSON(path string, data map[string]any) error {
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("inject opencode: marshal: %w", err)
	}
	out = append(out, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".argus-opencode-*.tmp")
	if err != nil {
		return fmt.Errorf("inject opencode: create temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(out); err != nil {
		tmp.Close()        //nolint:errcheck
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("inject opencode: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("inject opencode: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName) //nolint:errcheck
		return fmt.Errorf("inject opencode: rename: %w", err)
	}
	return nil
}

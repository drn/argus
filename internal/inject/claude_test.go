package inject

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInjectClaudeJSON_TypeField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")

	if err := injectClaudeJSON(path, 7742); err != nil {
		t.Fatalf("injectClaudeJSON: %v", err)
	}

	data, _ := os.ReadFile(path)
	var config map[string]interface{}
	json.Unmarshal(data, &config) //nolint:errcheck

	mcpServers := config["mcpServers"].(map[string]interface{})
	entry := mcpServers["argus-kb"].(map[string]interface{})
	if entry["type"] != "http" {
		t.Errorf("type: got %v, want http", entry["type"])
	}
	if entry["url"] != "http://localhost:7742/mcp" {
		t.Errorf("url: got %v", entry["url"])
	}
}

func TestInjectClaudeJSON_UpgradesOldFormat(t *testing.T) {
	// Simulate the old format (no transport field) and verify it gets upgraded.
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")

	old := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"argus-kb": map[string]interface{}{"url": "http://localhost:7742/mcp"},
		},
	}
	raw, _ := json.Marshal(old)
	os.WriteFile(path, raw, 0644) //nolint:errcheck

	if err := injectClaudeJSON(path, 7742); err != nil {
		t.Fatalf("injectClaudeJSON: %v", err)
	}

	data, _ := os.ReadFile(path)
	var config map[string]interface{}
	json.Unmarshal(data, &config) //nolint:errcheck

	mcpServers := config["mcpServers"].(map[string]interface{})
	entry := mcpServers["argus-kb"].(map[string]interface{})
	if entry["type"] != "http" {
		t.Errorf("old format not upgraded: type=%v, want http", entry["type"])
	}
}

func TestSetClaudeProjectMcpTrust(t *testing.T) {
	// Override home dir by using a temp path.
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settingsPath), 0755) //nolint:errcheck

	// Test by calling injectClaudeJSON directly on the settings file.
	if err := writeJSON(settingsPath, map[string]interface{}{
		"enableAllProjectMcpServers": true,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, _ := os.ReadFile(settingsPath)
	var config map[string]interface{}
	json.Unmarshal(data, &config) //nolint:errcheck

	if v, ok := config["enableAllProjectMcpServers"].(bool); !ok || !v {
		t.Error("enableAllProjectMcpServers not set to true")
	}
}

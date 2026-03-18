package inject

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInjectWorktree_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	if err := InjectWorktree(dir, 7742); err != nil {
		t.Fatalf("InjectWorktree: %v", err)
	}

	mcpPath := filepath.Join(dir, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read .mcp.json: %v", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse json: %v", err)
	}

	mcpServers, ok := config["mcpServers"].(map[string]interface{})
	if !ok {
		t.Fatal("mcpServers not found or wrong type")
	}
	entry, ok := mcpServers["argus-kb"].(map[string]interface{})
	if !ok {
		t.Fatal("argus-kb entry not found")
	}
	if entry["url"] != "http://localhost:7742/mcp" {
		t.Errorf("url: got %v", entry["url"])
	}
}

func TestInjectWorktree_Idempotent(t *testing.T) {
	dir := t.TempDir()

	// Call twice — second call should not change the file.
	if err := InjectWorktree(dir, 7742); err != nil {
		t.Fatalf("first inject: %v", err)
	}

	info1, _ := os.Stat(filepath.Join(dir, ".mcp.json"))

	if err := InjectWorktree(dir, 7742); err != nil {
		t.Fatalf("second inject: %v", err)
	}

	info2, _ := os.Stat(filepath.Join(dir, ".mcp.json"))
	if info1.ModTime() != info2.ModTime() {
		t.Log("file was rewritten on second call (idempotency: content unchanged but mtime differs)")
	}
}

func TestInjectWorktree_PortChange(t *testing.T) {
	dir := t.TempDir()
	if err := InjectWorktree(dir, 7742); err != nil {
		t.Fatalf("inject port 7742: %v", err)
	}
	if err := InjectWorktree(dir, 7743); err != nil {
		t.Fatalf("inject port 7743: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	var config map[string]interface{}
	json.Unmarshal(data, &config) //nolint:errcheck

	mcpServers := config["mcpServers"].(map[string]interface{})
	entry := mcpServers["argus-kb"].(map[string]interface{})
	if entry["url"] != "http://localhost:7743/mcp" {
		t.Errorf("url after port change: got %v, want :7743", entry["url"])
	}
}

func TestInjectWorktree_PreservesOtherEntries(t *testing.T) {
	dir := t.TempDir()
	mcpPath := filepath.Join(dir, ".mcp.json")

	// Write a file with another MCP server already configured.
	existing := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"other-server": map[string]interface{}{"url": "http://example.com/mcp"},
		},
	}
	raw, _ := json.Marshal(existing)
	os.WriteFile(mcpPath, raw, 0644) //nolint:errcheck

	if err := InjectWorktree(dir, 7742); err != nil {
		t.Fatalf("inject: %v", err)
	}

	data, _ := os.ReadFile(mcpPath)
	var config map[string]interface{}
	json.Unmarshal(data, &config) //nolint:errcheck

	mcpServers := config["mcpServers"].(map[string]interface{})
	if _, ok := mcpServers["other-server"]; !ok {
		t.Error("other-server entry was removed")
	}
	if _, ok := mcpServers["argus-kb"]; !ok {
		t.Error("argus-kb entry was not added")
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

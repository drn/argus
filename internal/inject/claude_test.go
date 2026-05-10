package inject

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
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
	entry := mcpServers["argus"].(map[string]interface{})
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
			"argus": map[string]interface{}{"url": "http://localhost:7742/mcp"},
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
	entry := mcpServers["argus"].(map[string]interface{})
	if entry["type"] != "http" {
		t.Errorf("old format not upgraded: type=%v, want http", entry["type"])
	}
}

// TestInjectClaudeJSON_MigratesLegacyKey verifies the pre-rename "argus-kb"
// key is removed when the new "argus" entry is written. Simulates an upgrade
// from an older Argus build where the MCP server was named argus-kb.
func TestInjectClaudeJSON_MigratesLegacyKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")

	old := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"argus-kb": map[string]interface{}{"type": "http", "url": "http://localhost:7742/mcp"},
			"other":    map[string]interface{}{"type": "http", "url": "http://localhost:9000/mcp"},
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
	if _, still := mcpServers["argus-kb"]; still {
		t.Error("legacy argus-kb key was not removed")
	}
	if _, have := mcpServers["argus"]; !have {
		t.Error("new argus key was not added")
	}
	if _, preserved := mcpServers["other"]; !preserved {
		t.Error("unrelated MCP entries were clobbered during migration")
	}
}

func TestInjectClaudeJSON_NoOpWhenAlreadyCorrect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	testutil.NoError(t, injectClaudeJSON(path, 7742))

	first, err := os.Stat(path)
	testutil.NoError(t, err)

	// Second call with same port — should not rewrite the file. Compare
	// modtimes to confirm. The atomic-rename in writeJSON would change mtime.
	testutil.NoError(t, injectClaudeJSON(path, 7742))
	second, err := os.Stat(path)
	testutil.NoError(t, err)
	testutil.Equal(t, first.ModTime(), second.ModTime())
}

func TestInjectClaudeJSON_RewritesOnPortChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	testutil.NoError(t, injectClaudeJSON(path, 7742))
	testutil.NoError(t, injectClaudeJSON(path, 9000))

	raw, err := os.ReadFile(path)
	testutil.NoError(t, err)
	var cfg map[string]any
	testutil.NoError(t, json.Unmarshal(raw, &cfg))
	mcp := cfg["mcpServers"].(map[string]any)
	entry := mcp["argus"].(map[string]any)
	testutil.Equal(t, entry["url"], "http://localhost:9000/mcp")
}

func TestInjectClaudeJSON_MalformedFileFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	testutil.NoError(t, os.WriteFile(path, []byte("{bad json"), 0o644))
	err := injectClaudeJSON(path, 7742)
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "cannot parse")
}

func TestInjectClaudeJSON_MissingFileCreated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deeper")
	testutil.NoError(t, os.MkdirAll(path, 0o755))
	full := filepath.Join(path, "claude.json")
	testutil.NoError(t, injectClaudeJSON(full, 7742))
	_, err := os.Stat(full)
	testutil.NoError(t, err)
}

func TestInjectGlobal_UsesHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	testutil.NoError(t, InjectGlobal(7742))

	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	testutil.NoError(t, err)
	testutil.Contains(t, string(raw), "http://localhost:7742/mcp")
}

func TestSetClaudeProjectMcpTrust_Creates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	testutil.NoError(t, SetClaudeProjectMcpTrust())
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	testutil.NoError(t, err)

	var cfg map[string]any
	testutil.NoError(t, json.Unmarshal(raw, &cfg))
	v, ok := cfg["enableAllProjectMcpServers"].(bool)
	testutil.True(t, ok)
	testutil.True(t, v)
}

func TestSetClaudeProjectMcpTrust_Idempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	testutil.NoError(t, SetClaudeProjectMcpTrust())
	first, err := os.Stat(filepath.Join(home, ".claude", "settings.json"))
	testutil.NoError(t, err)

	testutil.NoError(t, SetClaudeProjectMcpTrust())
	second, err := os.Stat(filepath.Join(home, ".claude", "settings.json"))
	testutil.NoError(t, err)
	testutil.Equal(t, first.ModTime(), second.ModTime())
}

func TestSetClaudeProjectMcpTrust_PreservesExistingKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude")
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	existing := map[string]any{"theme": "dark", "fontSize": 14}
	raw, _ := json.Marshal(existing)
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0o644))

	testutil.NoError(t, SetClaudeProjectMcpTrust())

	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	testutil.NoError(t, err)
	var cfg map[string]any
	testutil.NoError(t, json.Unmarshal(data, &cfg))
	testutil.Equal(t, cfg["theme"], "dark")
	testutil.Equal(t, cfg["enableAllProjectMcpServers"], true)
}

func TestWriteJSON_ErrorOnUnwritableDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, chmod cannot block writes")
	}
	dir := t.TempDir()
	// Make dir read-only so CreateTemp fails.
	testutil.NoError(t, os.Chmod(dir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	err := writeJSON(filepath.Join(dir, "x.json"), map[string]any{"a": 1})
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "create temp")
}

func TestWriteJSON_RenameTargetIsDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	// Create a directory at target — rename of file → directory must fail.
	testutil.NoError(t, os.MkdirAll(filepath.Join(target, "child"), 0o755))
	err := writeJSON(target, map[string]any{"a": 1})
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "rename")
}

func TestWriteJSON_MarshalError(t *testing.T) {
	// Channels are not JSON-serializable; MarshalIndent must fail.
	err := writeJSON(filepath.Join(t.TempDir(), "x.json"), map[string]any{"ch": make(chan int)})
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "marshal")
}

func TestSetClaudeProjectMcpTrust_MkdirAllError(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Block creation of ~/.claude by putting a regular file in its place.
	testutil.NoError(t, os.WriteFile(filepath.Join(home, ".claude"), []byte("blocker"), 0o644))
	err := SetClaudeProjectMcpTrust()
	testutil.Error(t, err)
}

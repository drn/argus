package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectWorktree_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	if err := InjectWorktree(dir, 7742); err != nil {
		t.Fatalf("InjectWorktree: %v", err)
	}

	path := filepath.Join(dir, ".codex", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "[mcp_servers.argus-kb]") {
		t.Error("missing [mcp_servers.argus-kb] section")
	}
	if !strings.Contains(content, `url = "http://localhost:7742/mcp"`) {
		t.Error("missing url entry")
	}
	if !strings.Contains(content, "experimental_use_rmcp_client = true") {
		t.Error("missing experimental_use_rmcp_client flag")
	}
}

func TestInjectCodexTOML_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := injectCodexTOML(path, 7742); err != nil {
		t.Fatalf("first inject: %v", err)
	}
	data1, _ := os.ReadFile(path)

	if err := injectCodexTOML(path, 7742); err != nil {
		t.Fatalf("second inject: %v", err)
	}
	data2, _ := os.ReadFile(path)

	if string(data1) != string(data2) {
		t.Error("idempotency failure: file changed on second call")
	}
}

func TestInjectCodexTOML_PortChange(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := injectCodexTOML(path, 7742); err != nil {
		t.Fatalf("inject 7742: %v", err)
	}
	if err := injectCodexTOML(path, 7743); err != nil {
		t.Fatalf("inject 7743: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, `url = "http://localhost:7743/mcp"`) {
		t.Errorf("expected port 7743 in config:\n%s", content)
	}
	// Old port should not be present.
	if strings.Contains(content, ":7742") {
		t.Errorf("old port 7742 still present:\n%s", content)
	}
}

func TestInjectCodexTOML_PreservesExistingContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	existing := "some_setting = true\n\n[other_servers.foo]\nurl = \"http://example.com/mcp\"\n"
	os.WriteFile(path, []byte(existing), 0644) //nolint:errcheck

	if err := injectCodexTOML(path, 7742); err != nil {
		t.Fatalf("inject: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "some_setting = true") {
		t.Error("existing setting was removed")
	}
	if !strings.Contains(content, "[other_servers.foo]") {
		t.Error("other section was removed")
	}
	if !strings.Contains(content, "[mcp_servers.argus-kb]") {
		t.Error("argus-kb section not added")
	}
}

func TestRemoveSection(t *testing.T) {
	content := "a = 1\n\n[mcp_servers.argus-kb]\nurl = \"http://localhost:7742/mcp\"\n\n[other]\nkey = val\n"
	result := removeSection(content, "[mcp_servers.argus-kb]")

	if strings.Contains(result, "[mcp_servers.argus-kb]") {
		t.Error("section not removed")
	}
	if !strings.Contains(result, "[other]") {
		t.Error("other section was removed")
	}
	if !strings.Contains(result, "a = 1") {
		t.Error("content before section was removed")
	}
}

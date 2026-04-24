package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectCodexTOML_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	if err := injectCodexTOML(path, 7742); err != nil {
		t.Fatalf("injectCodexTOML: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config.toml: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "[mcp_servers.argus]") {
		t.Error("missing [mcp_servers.argus] section")
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
	if !strings.Contains(content, "[mcp_servers.argus]") {
		t.Error("argus section not added")
	}
}

func TestInjectCodexTOML_TopLevelKeyNotInSection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Simulate existing config where a section is the last thing in the file.
	// The old code would append experimental_use_rmcp_client AFTER the section,
	// placing it inside [notice.model_migrations] instead of at the top level.
	existing := `model = "gpt-5.4"

[notice.model_migrations]
"gpt-5.3-codex" = "gpt-5.4"
`
	os.WriteFile(path, []byte(existing), 0644) //nolint:errcheck

	if err := injectCodexTOML(path, 7742); err != nil {
		t.Fatalf("inject: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	// experimental_use_rmcp_client must be before the first section header.
	idx := strings.Index(content, "experimental_use_rmcp_client")
	firstSection := strings.Index(content, "\n[")
	if idx == -1 {
		t.Fatal("missing experimental_use_rmcp_client")
	}
	if firstSection != -1 && idx > firstSection {
		t.Errorf("experimental_use_rmcp_client is inside a section (pos %d > first section at %d):\n%s",
			idx, firstSection, content)
	}
}

func TestInjectCodexTOML_MigratesMisplacedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	// Reproduce the exact broken state: experimental_use_rmcp_client inside
	// [notice.model_migrations] with a correct MCP section already present.
	broken := `model = "gpt-5.4"

[notice.model_migrations]
"gpt-5.3-codex" = "gpt-5.4"
experimental_use_rmcp_client = true

[mcp_servers.argus]
url = "http://localhost:7742/mcp"
`
	os.WriteFile(path, []byte(broken), 0644) //nolint:errcheck

	if err := injectCodexTOML(path, 7742); err != nil {
		t.Fatalf("inject: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	// Key must now be at the top level.
	idx := strings.Index(content, "experimental_use_rmcp_client")
	firstSection := strings.Index(content, "\n[")
	if idx == -1 {
		t.Fatal("missing experimental_use_rmcp_client")
	}
	if firstSection != -1 && idx > firstSection {
		t.Errorf("experimental_use_rmcp_client still inside a section:\n%s", content)
	}
	// Must not appear twice.
	if strings.Count(content, "experimental_use_rmcp_client") != 1 {
		t.Errorf("expected exactly 1 occurrence, got %d:\n%s",
			strings.Count(content, "experimental_use_rmcp_client"), content)
	}
	// MCP section must still be present, correct, and not duplicated.
	if !strings.Contains(content, `url = "http://localhost:7742/mcp"`) {
		t.Errorf("MCP url missing:\n%s", content)
	}
	if strings.Count(content, "[mcp_servers.argus]") != 1 {
		t.Errorf("MCP section duplicated:\n%s", content)
	}
}

// TestInjectCodexTOML_MigratesLegacySection verifies the pre-rename
// [mcp_servers.argus-kb] section is removed when the new [mcp_servers.argus]
// section is written. Simulates an upgrade from an older Argus build.
func TestInjectCodexTOML_MigratesLegacySection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	legacy := `model = "gpt-5.4"

[mcp_servers.argus-kb]
url = "http://localhost:7742/mcp"

[other]
key = "val"
`
	os.WriteFile(path, []byte(legacy), 0644) //nolint:errcheck

	if err := injectCodexTOML(path, 7742); err != nil {
		t.Fatalf("inject: %v", err)
	}

	data, _ := os.ReadFile(path)
	content := string(data)

	if strings.Contains(content, "[mcp_servers.argus-kb]") {
		t.Errorf("legacy section not removed:\n%s", content)
	}
	if !strings.Contains(content, "[mcp_servers.argus]") {
		t.Errorf("new section not added:\n%s", content)
	}
	if !strings.Contains(content, "[other]") {
		t.Errorf("unrelated section was clobbered:\n%s", content)
	}
}

func TestEnsureTopLevel(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "empty file",
			content: "",
		},
		{
			name:    "already at top level",
			content: "experimental_use_rmcp_client = true\n\n[section]\nkey = val\n",
		},
		{
			name:    "inside section",
			content: "model = \"gpt-5\"\n\n[section]\nexperimental_use_rmcp_client = true\n",
		},
		{
			name:    "no sections",
			content: "model = \"gpt-5\"\n",
		},
		{
			name:    "file starts with section header",
			content: "[section]\nkey = val\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ensureTopLevel(tt.content, "experimental_use_rmcp_client", "experimental_use_rmcp_client = true")
			idx := strings.Index(result, "experimental_use_rmcp_client")
			if idx == -1 {
				t.Fatal("key missing from result")
			}
			firstSection := strings.Index(result, "\n[")
			if firstSection != -1 && idx > firstSection {
				t.Errorf("key is inside a section:\n%s", result)
			}
			if strings.Count(result, "experimental_use_rmcp_client") != 1 {
				t.Errorf("duplicate keys:\n%s", result)
			}
		})
	}
}

func TestRemoveSection(t *testing.T) {
	content := "a = 1\n\n[mcp_servers.argus]\nurl = \"http://localhost:7742/mcp\"\n\n[other]\nkey = val\n"
	result := removeSection(content, "[mcp_servers.argus]")

	if strings.Contains(result, "[mcp_servers.argus]") {
		t.Error("section not removed")
	}
	if !strings.Contains(result, "[other]") {
		t.Error("other section was removed")
	}
	if !strings.Contains(result, "a = 1") {
		t.Error("content before section was removed")
	}
}

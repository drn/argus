package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestRemoveGlobal_Codex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".codex", "config.toml")
	testutil.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	before := "model = \"gpt-5\"\n\n[mcp_servers.argus]\nurl = \"old\"\n\n[mcp_servers.other]\nurl = \"keep\"\n\n[mcp_servers.argus-kb]\nurl = \"older\"\n"
	testutil.NoError(t, os.WriteFile(path, []byte(before), 0600))
	testutil.NoError(t, RemoveGlobal())
	raw, err := os.ReadFile(path)
	testutil.NoError(t, err)
	for _, want := range []string{"model = \"gpt-5\"", "[mcp_servers.other]", "url = \"keep\""} {
		testutil.Contains(t, string(raw), want)
	}
	if strings.Contains(string(raw), "[mcp_servers.argus]") || strings.Contains(string(raw), "[mcp_servers.argus-kb]") {
		t.Fatalf("Argus sections survived cleanup: %s", raw)
	}
	testutil.NoError(t, RemoveGlobal())
}

func TestRemoveGlobal_CodexMissingAndMalformed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".codex", "config.toml")
	testutil.NoError(t, RemoveGlobal())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing file was created: %v", err)
	}
	testutil.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	bad := "[mcp_servers.argus]\nurl = [\n"
	testutil.NoError(t, os.WriteFile(path, []byte(bad), 0600))
	if err := RemoveGlobal(); err == nil {
		t.Fatal("expected parse error")
	}
	raw, err := os.ReadFile(path)
	testutil.NoError(t, err)
	testutil.Equal(t, string(raw), bad)
}

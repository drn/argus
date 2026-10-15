package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func readServers(t *testing.T, home string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".cursor", "mcp.json"))
	testutil.NoError(t, err)
	var data map[string]any
	testutil.NoError(t, json.Unmarshal(b, &data))
	return data["mcpServers"].(map[string]any)
}

func TestInjectGlobal_CreatesAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	testutil.NoError(t, InjectGlobal(7742))
	testutil.NoError(t, InjectGlobal(7742))
	argus := readServers(t, home)["argus"].(map[string]any)
	testutil.Equal(t, argus["url"], "http://localhost:7742/mcp")
}

func TestInjectGlobal_PreservesOthersAndDisabled(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".cursor")
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	seed := `{"theme":"x","mcpServers":{"other":{"url":"http://o"},"argus":{"url":"http://localhost:1/mcp","disabled":true}}}`
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(seed), 0o644))
	testutil.NoError(t, InjectGlobal(7742))
	servers := readServers(t, home)
	testutil.Equal(t, servers["other"].(map[string]any)["url"], "http://o")
	argus := servers["argus"].(map[string]any)
	testutil.Equal(t, argus["url"], "http://localhost:7742/mcp")
	testutil.Equal(t, argus["disabled"], true)
}

func TestInjectGlobal_PortZeroAndBadFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	testutil.NoError(t, InjectGlobal(0))
	_, err := os.Stat(filepath.Join(home, ".cursor"))
	testutil.Equal(t, os.IsNotExist(err), true)

	dir := filepath.Join(home, ".cursor")
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "mcp.json"), []byte("{not json"), 0o644))
	if InjectGlobal(7742) == nil {
		t.Fatal("expected error for unparseable file")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "mcp.json"))
	testutil.Equal(t, string(b), "{not json")

	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(`{"mcpServers":[]}`), 0o644))
	if InjectGlobal(7742) == nil {
		t.Fatal("expected error for non-object mcpServers")
	}
}

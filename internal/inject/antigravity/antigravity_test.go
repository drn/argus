package antigravity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func readObj(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	testutil.NoError(t, err)
	var m map[string]any
	testutil.NoError(t, json.Unmarshal(raw, &m))
	return m
}

func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".gemini", "config")
}

func TestInjectGlobal_CreatesBothFiles(t *testing.T) {
	dir := setHome(t)
	testutil.NoError(t, InjectGlobal(7742, "/s/skills"))

	mcp := readObj(t, filepath.Join(dir, "mcp_config.json"))
	argus := mcp["mcpServers"].(map[string]any)["argus"].(map[string]any)
	testutil.Equal(t, argus["serverUrl"], any("http://localhost:7742/mcp"))
	testutil.Equal(t, argus["disabled"], any(false))

	sk := readObj(t, filepath.Join(dir, "skills.json"))
	entries := sk["entries"].([]any)
	testutil.Equal(t, len(entries), 1)
	testutil.Equal(t, entries[0].(map[string]any)["path"], any("/s/skills"))
}

func TestInjectGlobal_NoSkillsDirSkipsManifest(t *testing.T) {
	dir := setHome(t)
	testutil.NoError(t, InjectGlobal(1, ""))
	_, err := os.Stat(filepath.Join(dir, "skills.json"))
	testutil.Equal(t, os.IsNotExist(err), true)
}

func TestInjectGlobal_NoPortSkipsMCP(t *testing.T) {
	dir := setHome(t)
	testutil.NoError(t, InjectGlobal(0, "/s/skills"))
	_, err := os.Stat(filepath.Join(dir, "mcp_config.json"))
	testutil.Equal(t, os.IsNotExist(err), true)
	testutil.Equal(t, len(readObj(t, filepath.Join(dir, "skills.json"))["entries"].([]any)), 1)
}

func TestInjectMCP(t *testing.T) {
	t.Run("idempotent", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "mcp_config.json")
		testutil.NoError(t, injectMCP(p, 9))
		old := time.Now().Add(-time.Hour)
		testutil.NoError(t, os.Chtimes(p, old, old))
		testutil.NoError(t, injectMCP(p, 9))
		info, _ := os.Stat(p)
		testutil.Equal(t, info.ModTime().Unix(), old.Unix())
	})
	t.Run("url key accepted", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "mcp_config.json")
		testutil.NoError(t, os.WriteFile(p, []byte(`{"mcpServers":{"argus":{"url":"http://localhost:9/mcp"}}}`), 0o644))
		before, _ := os.ReadFile(p)
		testutil.NoError(t, injectMCP(p, 9))
		after, _ := os.ReadFile(p)
		testutil.Equal(t, string(after), string(before))
	})
	t.Run("operator disable respected", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "mcp_config.json")
		testutil.NoError(t, os.WriteFile(p, []byte(`{"mcpServers":{"argus":{"serverUrl":"http://localhost:9/mcp","disabled":true}}}`), 0o644))
		before, _ := os.ReadFile(p)
		testutil.NoError(t, injectMCP(p, 9))
		after, _ := os.ReadFile(p)
		testutil.Equal(t, string(after), string(before))
	})
	t.Run("disabled kept across port change", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "mcp_config.json")
		testutil.NoError(t, os.WriteFile(p, []byte(`{"mcpServers":{"argus":{"serverUrl":"http://localhost:9/mcp","disabled":true}}}`), 0o644))
		testutil.NoError(t, injectMCP(p, 10))
		argus := readObj(t, p)["mcpServers"].(map[string]any)["argus"].(map[string]any)
		testutil.Equal(t, argus["serverUrl"], any("http://localhost:10/mcp"))
		testutil.Equal(t, argus["disabled"], any(true))
	})
	t.Run("port change rewrites and preserves others", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "mcp_config.json")
		testutil.NoError(t, os.WriteFile(p, []byte(`{"top":1,"mcpServers":{"fs":{"command":"x"},"argus":{"serverUrl":"http://localhost:1/mcp"}}}`), 0o644))
		testutil.NoError(t, injectMCP(p, 2))
		m := readObj(t, p)
		testutil.Equal(t, m["top"], any(float64(1)))
		servers := m["mcpServers"].(map[string]any)
		testutil.Equal(t, servers["fs"].(map[string]any)["command"], any("x"))
		testutil.Equal(t, servers["argus"].(map[string]any)["serverUrl"], any("http://localhost:2/mcp"))
	})
	t.Run("invalid json untouched", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "mcp_config.json")
		testutil.NoError(t, os.WriteFile(p, []byte("{bad"), 0o644))
		err := injectMCP(p, 2)
		testutil.Contains(t, err.Error(), "cannot parse")
		raw, _ := os.ReadFile(p)
		testutil.Equal(t, string(raw), "{bad")
	})
	t.Run("non-object mcpServers untouched", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "mcp_config.json")
		testutil.NoError(t, os.WriteFile(p, []byte(`{"mcpServers":["x"]}`), 0o644))
		err := injectMCP(p, 2)
		testutil.Contains(t, err.Error(), "not an object")
		raw, _ := os.ReadFile(p)
		testutil.Equal(t, string(raw), `{"mcpServers":["x"]}`)
	})
	t.Run("null mcpServers replaced", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "mcp_config.json")
		testutil.NoError(t, os.WriteFile(p, []byte(`{"mcpServers":null}`), 0o644))
		testutil.NoError(t, injectMCP(p, 2))
		servers := readObj(t, p)["mcpServers"].(map[string]any)
		testutil.Equal(t, servers["argus"].(map[string]any)["serverUrl"], any("http://localhost:2/mcp"))
	})
}

func TestInjectSkills(t *testing.T) {
	t.Run("appends once and preserves entries", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "skills.json")
		testutil.NoError(t, os.WriteFile(p, []byte(`{"inherits":[{"path":"x"}],"entries":[{"path":"~/mine"}]}`), 0o644))
		testutil.NoError(t, injectSkills(p, "/argus"))
		testutil.NoError(t, injectSkills(p, "/argus"))
		m := readObj(t, p)
		entries := m["entries"].([]any)
		testutil.Equal(t, len(entries), 2)
		testutil.Equal(t, entries[1].(map[string]any)["path"], any("/argus"))
		testutil.Equal(t, len(m["inherits"].([]any)), 1)
	})
	t.Run("non-array entries untouched", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "skills.json")
		testutil.NoError(t, os.WriteFile(p, []byte(`{"entries":"weird"}`), 0o644))
		err := injectSkills(p, "/argus")
		testutil.Contains(t, err.Error(), "not an array")
	})
	t.Run("invalid json", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "skills.json")
		testutil.NoError(t, os.WriteFile(p, []byte("nope"), 0o644))
		testutil.Contains(t, injectSkills(p, "/a").Error(), "cannot parse")
	})
}

func TestInjectGlobal_ErrorsJoined(t *testing.T) {
	dir := setHome(t)
	testutil.NoError(t, os.MkdirAll(dir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "mcp_config.json"), []byte("{bad"), 0o644))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "skills.json"), []byte("{bad"), 0o644))
	err := InjectGlobal(1, "/s")
	testutil.Contains(t, err.Error(), "mcp_config.json")
	testutil.Contains(t, err.Error(), "skills.json")
}

func TestInjectGlobal_MkdirFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	testutil.NoError(t, os.WriteFile(filepath.Join(home, ".gemini"), []byte("file"), 0o644))
	testutil.Contains(t, InjectGlobal(1, "").Error(), "create")
}

func TestInjectRouting(t *testing.T) {
	dir := setHome(t)
	target := filepath.Join(dir, "rules", "argus-routing.md")

	testutil.NoError(t, InjectRouting([]byte("ROUTING\n")))
	got, err := os.ReadFile(target)
	testutil.NoError(t, err)
	testutil.Equal(t, string(got), "---\ntrigger: always_on\n---\n\nROUTING\n")

	t.Run("unchanged content is not rewritten", func(t *testing.T) {
		old := time.Now().Add(-time.Hour)
		testutil.NoError(t, os.Chtimes(target, old, old))
		testutil.NoError(t, InjectRouting([]byte("ROUTING\n")))
		info, err := os.Stat(target)
		testutil.NoError(t, err)
		if !info.ModTime().Equal(old.Truncate(time.Second)) && info.ModTime().After(old.Add(time.Minute)) {
			t.Fatal("identical content must not be rewritten")
		}
	})

	t.Run("changed content replaces the file", func(t *testing.T) {
		testutil.NoError(t, InjectRouting([]byte("NEW\n")))
		got, err := os.ReadFile(target)
		testutil.NoError(t, err)
		testutil.Equal(t, string(got), "---\ntrigger: always_on\n---\n\nNEW\n")
	})

	t.Run("sibling user rules are untouched", func(t *testing.T) {
		mine := filepath.Join(dir, "rules", "mine.md")
		testutil.NoError(t, os.WriteFile(mine, []byte("mine"), 0o644))
		testutil.NoError(t, InjectRouting([]byte("X")))
		b, err := os.ReadFile(mine)
		testutil.NoError(t, err)
		testutil.Equal(t, string(b), "mine")
	})
}

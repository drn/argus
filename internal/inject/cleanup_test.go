package inject

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestRemoveGlobal_Claude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude.json")
	testutil.NoError(t, os.WriteFile(path, []byte(`{"mcpServers":{"argus":{"url":"old"},"argus-kb":{"url":"older"},"other":{"url":"keep"}},"theme":"dark"}`), 0600))
	testutil.NoError(t, RemoveGlobal())
	raw, err := os.ReadFile(path)
	testutil.NoError(t, err)
	testutil.Contains(t, string(raw), `"other"`)
	testutil.Contains(t, string(raw), `"theme"`)
	for _, name := range []string{`"argus"`, `"argus-kb"`} {
		if strings.Contains(string(raw), name) {
			t.Fatalf("global entry %s survived cleanup", name)
		}
	}
	testutil.NoError(t, RemoveGlobal())
}

func TestRemoveGlobal_ClaudeMissingAndMalformed(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".claude.json")
	testutil.NoError(t, RemoveGlobal())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing file was created: %v", err)
	}
	testutil.NoError(t, os.WriteFile(path, []byte("{bad"), 0600))
	if err := RemoveGlobal(); err == nil {
		t.Fatal("expected parse error")
	}
	raw, err := os.ReadFile(path)
	testutil.NoError(t, err)
	testutil.Equal(t, string(raw), "{bad")
}

package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestMaterializeContentAddressed(t *testing.T) {
	files := map[string][]byte{"a/b.txt": []byte("x")}

	t.Run("reuses a complete dir without rewriting", func(t *testing.T) {
		root := t.TempDir()
		dir, err := materializeContentAddressed(root, files)
		testutil.NoError(t, err)
		testutil.NoError(t, os.WriteFile(filepath.Join(dir, "a", "b.txt"), []byte("edited"), 0o644))
		again, err := materializeContentAddressed(root, files)
		testutil.NoError(t, err)
		testutil.Equal(t, again, dir)
		body, err := os.ReadFile(filepath.Join(dir, "a", "b.txt"))
		testutil.NoError(t, err)
		testutil.Equal(t, string(body), "edited")
	})

	t.Run("marker-less hash dir fails and is left in place", func(t *testing.T) {
		root := t.TempDir()
		dir, err := materializeContentAddressed(root, files)
		testutil.NoError(t, err)
		testutil.NoError(t, os.Remove(filepath.Join(dir, contentAddressedMarker)))
		_, err = materializeContentAddressed(root, files)
		testutil.Equal(t, err != nil, true)
		_, statErr := os.Stat(filepath.Join(dir, "a", "b.txt"))
		testutil.NoError(t, statErr)
		entries, err := os.ReadDir(root)
		testutil.NoError(t, err)
		testutil.Equal(t, len(entries), 1)
	})
}

func TestArgusCacheDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir, err := argusCacheDir("omp-mcp")
	testutil.NoError(t, err)
	testutil.Equal(t, dir, filepath.Join(home, ".argus", "cache", "omp-mcp"))
}

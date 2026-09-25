package artifacts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/testutil"
)

func TestResolvePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := agent.ArtifactsDir("task")
	testutil.NoError(t, os.MkdirAll(dir, 0700))
	testutil.NoError(t, os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("ok"), 0600))
	t.Run("registered basename shape", func(t *testing.T) {
		path, ok := ResolvePath("task", "ok.txt")
		testutil.True(t, ok)
		real, err := filepath.EvalSymlinks(filepath.Join(dir, "ok.txt"))
		testutil.NoError(t, err)
		testutil.Equal(t, path, real)
	})
	t.Run("traversal", func(t *testing.T) {
		_, ok := ResolvePath("task", "../outside.txt")
		testutil.True(t, !ok)
	})
	t.Run("nested", func(t *testing.T) {
		_, ok := ResolvePath("task", "sub/file.txt")
		testutil.True(t, !ok)
	})
	t.Run("symlink escape", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "outside.txt")
		testutil.NoError(t, os.WriteFile(outside, []byte("bad"), 0600))
		testutil.NoError(t, os.Symlink(outside, filepath.Join(dir, "link.txt")))
		_, ok := ResolvePath("task", "link.txt")
		testutil.True(t, !ok)
	})
	t.Run("missing bytes", func(t *testing.T) {
		_, ok := ResolvePath("task", "missing.txt")
		testutil.True(t, ok)
	})
}

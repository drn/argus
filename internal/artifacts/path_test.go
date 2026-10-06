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
	t.Run("nested folder member", func(t *testing.T) {
		testutil.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0700))
		testutil.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "file.txt"), []byte("ok"), 0600))
		path, ok := ResolvePath("task", "sub/file.txt")
		testutil.True(t, ok)
		real, err := filepath.EvalSymlinks(filepath.Join(dir, "sub", "file.txt"))
		testutil.NoError(t, err)
		testutil.Equal(t, path, real)
	})
	t.Run("nested traversal and reserved", func(t *testing.T) {
		for _, name := range []string{"sub/../../x", "/abs", "sub//x", ".thumbs/a.jpg", "a\\b", ""} {
			_, ok := ResolvePath("task", name)
			testutil.True(t, !ok)
		}
	})
	t.Run("symlinked folder escape", func(t *testing.T) {
		outside := t.TempDir()
		testutil.NoError(t, os.WriteFile(filepath.Join(outside, "s.txt"), []byte("bad"), 0600))
		testutil.NoError(t, os.Symlink(outside, filepath.Join(dir, "linkdir")))
		_, ok := ResolvePath("task", "linkdir/s.txt")
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

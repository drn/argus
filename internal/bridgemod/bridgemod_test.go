package bridgemod

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestFiles_ContainsRuntimeFilesOnly(t *testing.T) {
	files, err := Files()
	testutil.NoError(t, err)
	for _, want := range []string{
		".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.ts",
		"hooks/tracker.ts", "hooks/reporter.ts", "hooks/measure.ts",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("embedded mod is missing %s", want)
		}
	}
	for name := range files {
		if strings.HasPrefix(name, "tests/") || strings.Contains(name, "types/") || name == "tsconfig.json" {
			t.Errorf("dev-only file embedded: %s", name)
		}
	}
	var manifest struct{ Name string }
	testutil.NoError(t, json.Unmarshal(files[".claude-plugin/plugin.json"], &manifest))
	testutil.Equal(t, manifest.Name, "argus-bridge")
}

// TestEmbedCoversRuntimeFilesOnDisk fails when someone adds a runtime file to
// the mod dir (e.g. a new hooks/foo.ts is globbed, but a hooks/lib/foo.ts or a
// .json is not) without the go:embed pattern picking it up: the mod would then
// load fine in `claude plugin test` and break in production.
func TestEmbedCoversRuntimeFilesOnDisk(t *testing.T) {
	files, err := Files()
	testutil.NoError(t, err)
	root := filepath.FromSlash(embedRoot)
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		// .claude-plugin/types is a gitignored dev symlink (a file entry to
		// WalkDir, not a dir), so skip it by name for both kinds.
		switch rel {
		case "tests", "node_modules", ".claude-plugin/types":
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		switch rel {
		case "tsconfig.json", ".gitignore":
			return nil
		}
		if _, ok := files[rel]; !ok {
			t.Errorf("%s exists on disk but is not embedded; extend the go:embed patterns", rel)
		}
		return nil
	})
	testutil.NoError(t, err)
}

func TestMaterialize(t *testing.T) {
	root := t.TempDir()
	dir, err := materialize(root)
	testutil.NoError(t, err)
	hash, _ := Hash()
	testutil.Equal(t, dir, filepath.Join(root, hash, "argus-bridge"))

	files, _ := Files()
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		testutil.NoError(t, err)
		testutil.Equal(t, string(got), string(want))
	}

	t.Run("idempotent and leaves no temp dirs", func(t *testing.T) {
		before, _ := os.Stat(filepath.Join(dir, "hooks", "register.ts"))
		again, err := materialize(root)
		testutil.NoError(t, err)
		testutil.Equal(t, again, dir)
		after, _ := os.Stat(filepath.Join(dir, "hooks", "register.ts"))
		testutil.True(t, before.ModTime().Equal(after.ModTime()))
		entries, _ := os.ReadDir(root)
		testutil.Equal(t, len(entries), 1)
	})

	t.Run("an incomplete directory of the same hash is rebuilt", func(t *testing.T) {
		hashDir := filepath.Dir(dir)
		testutil.NoError(t, os.Remove(filepath.Join(hashDir, completeMarker)))
		testutil.NoError(t, os.WriteFile(filepath.Join(dir, "hooks", "register.ts"), []byte("corrupt"), 0o644))
		_, err := materialize(root)
		testutil.NoError(t, err)
		got, _ := os.ReadFile(filepath.Join(dir, "hooks", "register.ts"))
		testutil.Equal(t, string(got), string(files["hooks/register.ts"]))
	})
}

func TestMaterialize_UnwritableRoot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	testutil.NoError(t, os.WriteFile(file, nil, 0o644))
	_, err := materialize(filepath.Join(file, "sub"))
	testutil.Error(t, err)
}

func TestHash_ChangesWithContent(t *testing.T) {
	a := hashFiles(map[string][]byte{"x": []byte("1")})
	b := hashFiles(map[string][]byte{"x": []byte("2")})
	c := hashFiles(map[string][]byte{"y": []byte("1")})
	testutil.NotEqual(t, a, b)
	testutil.NotEqual(t, a, c)
	testutil.Equal(t, len(a), 16)
}

func TestEnsureBridgeMod_InertUnderGoTest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir, err := EnsureBridgeMod()
	testutil.NoError(t, err)
	testutil.Equal(t, dir, "")
	entries, _ := os.ReadDir(os.Getenv("HOME"))
	testutil.Equal(t, len(entries), 0)
}

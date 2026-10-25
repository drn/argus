package bridgemod

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestFiles_ContainsRuntimeFilesOnly(t *testing.T) {
	files, err := Files()
	testutil.NoError(t, err)
	for _, want := range []string{
		".claude-plugin/plugin.json", "hooks/hooks.json", "hooks/register.ts",
		"hooks/tracker.ts", "hooks/reporter.ts", "hooks/measure.ts",
		"hooks/ui.tsx", "types/index.d.ts",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("embedded mod is missing %s", want)
		}
	}
	for name := range files {
		if strings.HasPrefix(name, "tests/") || strings.Contains(name, ".claude-plugin/types/") || name == "tsconfig.json" {
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
	dir, err := Materialize(root)
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
		again, err := Materialize(root)
		testutil.NoError(t, err)
		testutil.Equal(t, again, dir)
		after, _ := os.Stat(filepath.Join(dir, "hooks", "register.ts"))
		testutil.True(t, before.ModTime().Equal(after.ModTime()))
		entries, _ := os.ReadDir(root)
		testutil.Equal(t, len(entries), 1)
	})

	t.Run("an existing directory without the marker is never clobbered", func(t *testing.T) {
		// A markerless hash dir cannot be produced by Materialize (the marker is
		// written before the rename); if one exists something else made it, and
		// removing it could delete a directory another launch just returned.
		hashDir := filepath.Dir(dir)
		testutil.NoError(t, os.Remove(filepath.Join(hashDir, completeMarker)))
		_, err := Materialize(root)
		testutil.Error(t, err)
		_, serr := os.Stat(filepath.Join(dir, "hooks", "register.ts"))
		testutil.NoError(t, serr)
	})
}

// TestMaterialize_ConcurrentFirstLaunch reproduces the first-launch race: many
// launches materializing the same fresh root at once must all be handed a
// complete directory (the earlier RemoveAll-before-rename let one launch
// delete the directory another had just returned).
func TestMaterialize_ConcurrentFirstLaunch(t *testing.T) {
	files, err := Files()
	testutil.NoError(t, err)
	const workers, rounds = 16, 25
	for r := 0; r < rounds; r++ {
		root := t.TempDir()
		var wg sync.WaitGroup
		results := make([]string, workers)
		errs := make([]error, workers)
		start := make(chan struct{})
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				results[w], errs[w] = Materialize(root)
				// Check at once, inside the goroutine: a racing launch's
				// RemoveAll makes this path vanish only transiently.
				if errs[w] == nil {
					for name := range files {
						if _, err := os.Stat(filepath.Join(results[w], filepath.FromSlash(name))); err != nil {
							errs[w] = err
							return
						}
					}
				}
			}()
		}
		close(start)
		wg.Wait()
		for w := 0; w < workers; w++ {
			if errs[w] != nil {
				t.Fatalf("round %d worker %d: returned path is incomplete: %v", r, w, errs[w])
			}
		}
	}
}

func TestMaterialize_UnwritableRoot(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	testutil.NoError(t, os.WriteFile(file, nil, 0o644))
	_, err := Materialize(filepath.Join(file, "sub"))
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

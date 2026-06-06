package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// writeFile writes contents to a config.toml inside a fresh temp dir and returns
// the path.
func writeFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	testutil.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

func TestFileLoader_NilAndEmptyAreNoOps(t *testing.T) {
	base := DefaultConfig()

	t.Run("nil loader", func(t *testing.T) {
		var l *FileLoader
		testutil.DeepEqual(t, l.Apply(base), base)
		testutil.Equal(t, l.Path(), "")
		testutil.NoError(t, l.Err())
	})

	t.Run("empty path", func(t *testing.T) {
		l := NewFileLoader("")
		testutil.DeepEqual(t, l.Apply(base), base)
		testutil.Equal(t, l.Path(), "")
		testutil.NoError(t, l.Err())
	})
}

func TestFileLoader_MissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	l := NewFileLoader(path)
	base := DefaultConfig()

	got := l.Apply(base)

	testutil.DeepEqual(t, got, base)
	testutil.NoError(t, l.Err())
	testutil.Equal(t, l.Path(), path)
}

func TestFileLoader_OverlaysPresentFieldsOnly(t *testing.T) {
	path := writeFile(t, `
[ui]
theme = "dark"
spinner_style = "braille"

[keybindings]
status = "x"

[backends.custom]
command = "my-agent"
prompt_flag = "-p"
`)
	l := NewFileLoader(path)
	base := DefaultConfig()

	got := l.Apply(base)
	testutil.NoError(t, l.Err())

	// Overridden fields win.
	testutil.Equal(t, got.UI.Theme, "dark")
	testutil.Equal(t, got.UI.SpinnerStyle, "braille")
	testutil.Equal(t, got.Keybindings.Status, "x")

	// Absent fields fall through to the base.
	testutil.Equal(t, got.UI.ShowIcons, base.UI.ShowIcons)
	testutil.Equal(t, got.Keybindings.New, base.Keybindings.New)

	// Map merge: new backend added, defaults preserved.
	testutil.Equal(t, got.Backends["custom"].Command, "my-agent")
	testutil.Equal(t, got.Backends["custom"].PromptFlag, "-p")
	if _, ok := got.Backends["claude"]; !ok {
		t.Error("default claude backend should survive the overlay")
	}

	// The base's maps must not be mutated by the overlay.
	if _, ok := base.Backends["custom"]; ok {
		t.Error("Apply mutated the caller's Backends map")
	}
}

func TestFileLoader_OverridesExistingBackend(t *testing.T) {
	path := writeFile(t, `
[backends.claude]
command = "claude --custom"
`)
	l := NewFileLoader(path)
	base := DefaultConfig()

	got := l.Apply(base)

	testutil.Equal(t, got.Backends["claude"].Command, "claude --custom")
	// The base value is untouched.
	testutil.Equal(t, base.Backends["claude"].Command, "claude")
}

func TestFileLoader_AllocatesNilMaps(t *testing.T) {
	path := writeFile(t, `
[backends.foo]
command = "foo"
`)
	l := NewFileLoader(path)

	// A zero Config has nil Backends/Projects maps; the overlay must allocate.
	got := l.Apply(Config{})

	testutil.NoError(t, l.Err())
	testutil.Equal(t, got.Backends["foo"].Command, "foo")
}

func TestFileLoader_ParseErrorLeavesBaseUnchanged(t *testing.T) {
	path := writeFile(t, "this is = = not valid toml [[[")
	l := NewFileLoader(path)
	base := DefaultConfig()

	got := l.Apply(base)

	testutil.DeepEqual(t, got, base)
	if l.Err() == nil {
		t.Fatal("expected a parse error")
	}
	testutil.Contains(t, l.Err().Error(), "parsing")
}

func TestFileLoader_CacheHitAndReload(t *testing.T) {
	path := writeFile(t, `[ui]
theme = "first"`)
	l := NewFileLoader(path)
	base := DefaultConfig()

	// First read.
	testutil.Equal(t, l.Apply(base).UI.Theme, "first")
	// Second read with no file change is a cache hit, same result.
	testutil.Equal(t, l.Apply(base).UI.Theme, "first")

	// Rewriting with different-length content changes the size, forcing a
	// reload regardless of modtime resolution.
	testutil.NoError(t, os.WriteFile(path, []byte(`[ui]
theme = "second-value"`), 0o644))
	testutil.Equal(t, l.Apply(base).UI.Theme, "second-value")
}

func TestFileLoader_RecoversAfterFileRemoved(t *testing.T) {
	path := writeFile(t, `[ui]
theme = "present"`)
	l := NewFileLoader(path)
	base := DefaultConfig()

	testutil.Equal(t, l.Apply(base).UI.Theme, "present")

	// Removing the file drops back to the base with no error.
	testutil.NoError(t, os.Remove(path))
	got := l.Apply(base)
	testutil.Equal(t, got.UI.Theme, base.UI.Theme)
	testutil.NoError(t, l.Err())
}

func TestFileLoader_ReadErrorSurfaced(t *testing.T) {
	// Pointing the loader at a directory makes Stat succeed but ReadFile fail.
	dir := t.TempDir()
	l := NewFileLoader(dir)
	base := DefaultConfig()

	got := l.Apply(base)

	testutil.DeepEqual(t, got, base)
	if l.Err() == nil {
		t.Fatal("expected a read error for a directory path")
	}
	testutil.Contains(t, l.Err().Error(), "reading")
}

func TestFileLoader_StatErrorSurfaced(t *testing.T) {
	// A path whose parent is a regular file yields ENOTDIR from Stat, which is
	// not fs.ErrNotExist — exercising the stat-error branch.
	parent := writeFile(t, "")
	l := NewFileLoader(filepath.Join(parent, "child.toml"))
	base := DefaultConfig()

	got := l.Apply(base)

	testutil.DeepEqual(t, got, base)
	if l.Err() == nil {
		t.Fatal("expected a stat error for a path under a regular file")
	}
	testutil.Contains(t, l.Err().Error(), "stat")
}

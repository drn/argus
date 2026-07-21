package config

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

[keybindings.tasklist]
status_advance = "x"

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
	testutil.Equal(t, got.Keybindings.TaskList["status_advance"], "x")

	// Absent fields fall through to the base.
	testutil.Equal(t, got.UI.ShowIcons, base.UI.ShowIcons)
	testutil.Nil(t, got.Keybindings.Agent)

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

// TestFileLoader_HeraCoordinatorContextBudgetOverlay pins the
// add-coordinator-context-management config-management delta's "Explicit
// budget overrides the default" scenario: a config.toml
// `hera.coordinator_context_budget` value must win over the 300000 default.
// The field does not exist on HeraConfig yet, so this fails to compile until
// Stage 2 adds it.
func TestFileLoader_HeraCoordinatorContextBudgetOverlay(t *testing.T) {
	path := writeFile(t, `
[hera]
coordinator_context_budget = 350000
`)
	l := NewFileLoader(path)
	base := DefaultConfig()

	got := l.Apply(base)
	testutil.NoError(t, l.Err())

	testutil.Equal(t, got.Hera.CoordinatorContextBudget, 350000)
	// The base value is untouched.
	testutil.Equal(t, base.Hera.CoordinatorContextBudget, 300000)
}

// TestFileLoader_HeraCoordinatorNudgeIncrementOverlay pins the
// throttle-coord-hook-nudge config delta's override precedence: a config.toml
// `hera.coordinator_nudge_increment` value must win over the 50000 default,
// mirroring TestFileLoader_HeraCoordinatorContextBudgetOverlay's shape exactly.
func TestFileLoader_HeraCoordinatorNudgeIncrementOverlay(t *testing.T) {
	path := writeFile(t, `
[hera]
coordinator_nudge_increment = 75000
`)
	l := NewFileLoader(path)
	base := DefaultConfig()

	got := l.Apply(base)
	testutil.NoError(t, l.Err())

	testutil.Equal(t, got.Hera.CoordinatorNudgeIncrement, 75000)
	// The base value is untouched.
	testutil.Equal(t, base.Hera.CoordinatorNudgeIncrement, 50000)
}

// TestFileLoader_HeraWorkerContextWindowOverlay pins the rail-context-high
// config delta's override precedence: a config.toml `hera.worker_context_window`
// value must win over the 1000000 default, mirroring
// TestFileLoader_HeraCoordinatorContextBudgetOverlay's shape exactly.
func TestFileLoader_HeraWorkerContextWindowOverlay(t *testing.T) {
	path := writeFile(t, `
[hera]
worker_context_window = 500000
`)
	l := NewFileLoader(path)
	base := DefaultConfig()

	got := l.Apply(base)
	testutil.NoError(t, l.Err())

	testutil.Equal(t, got.Hera.WorkerContextWindow, 500000)
	// The base value is untouched.
	testutil.Equal(t, base.Hera.WorkerContextWindow, 1000000)
}

func TestFileLoader_BackendModelsOverlay(t *testing.T) {
	path := writeFile(t, `
[backends.claude]
command = "claude"
models = ["opus", "sonnet"]
`)
	l := NewFileLoader(path)
	base := DefaultConfig()

	got := l.Apply(base)

	testutil.DeepEqual(t, got.Backends["claude"].Models, []string{"opus", "sonnet"})
	// DefaultConfig leaves Models nil so the built-in list applies.
	testutil.Nil(t, base.Backends["claude"].Models)
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

	// Second call on the same broken file: cache hit, decode fails again, but
	// the error is already set so it is not re-logged. Still returns base.
	testutil.DeepEqual(t, l.Apply(base), base)
	testutil.Contains(t, l.Err().Error(), "parsing")
}

// TestFileLoader_RecoversAfterParseErrorFixed covers the bad→good transition:
// once the file parses, the overlay applies and Err() clears.
func TestFileLoader_RecoversAfterParseErrorFixed(t *testing.T) {
	path := writeFile(t, "not = = valid [[[")
	l := NewFileLoader(path)
	base := DefaultConfig()

	testutil.DeepEqual(t, l.Apply(base), base)
	if l.Err() == nil {
		t.Fatal("expected a parse error before the fix")
	}

	testutil.NoError(t, os.WriteFile(path, []byte(`[ui]
theme = "fixed"`), 0o644))

	got := l.Apply(base)
	testutil.NoError(t, l.Err())
	testutil.Equal(t, got.UI.Theme, "fixed")
}

// TestFileLoader_ProjectSandboxSnakeCaseIgnored locks the documented footgun:
// ProjectSandboxConfig fields match by lowercased Go name, so snake_case
// (deny_read) is silently ignored while the lowercased form (denyread) decodes.
func TestFileLoader_ProjectSandboxSnakeCaseIgnored(t *testing.T) {
	l := NewFileLoader(writeFile(t, `
[projects.demo.sandbox]
deny_read = ["/secret"]
`))

	got := l.Apply(DefaultConfig())
	testutil.NoError(t, l.Err())

	// snake_case does not match the untagged DenyRead field → stays nil.
	if got.Projects["demo"].Sandbox.DenyRead != nil {
		t.Errorf("snake_case deny_read should be ignored, got %v", got.Projects["demo"].Sandbox.DenyRead)
	}
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

	// Rewriting with different-length content changes the size, which forces a
	// reload (the size half of the size+mtime cache key differs).
	testutil.NoError(t, os.WriteFile(path, []byte(`[ui]
theme = "second-value"`), 0o644))
	testutil.Equal(t, l.Apply(base).UI.Theme, "second-value")
}

// TestFileLoader_ReloadOnMtimeOnlyChange covers the cache-key branch where the
// size is unchanged but the modtime advances (an in-place edit of identical
// length) — the size-change test above can't reach it.
func TestFileLoader_ReloadOnMtimeOnlyChange(t *testing.T) {
	path := writeFile(t, `[ui]
theme = "aaaa"`)
	l := NewFileLoader(path)
	base := DefaultConfig()

	testutil.Equal(t, l.Apply(base).UI.Theme, "aaaa")

	// Same byte length, different content; bump the modtime so the loader can't
	// rely on size alone to detect the change.
	testutil.NoError(t, os.WriteFile(path, []byte(`[ui]
theme = "bbbb"`), 0o644))
	info, err := os.Stat(path)
	testutil.NoError(t, err)
	testutil.NoError(t, os.Chtimes(path, info.ModTime().Add(time.Hour), info.ModTime().Add(time.Hour)))

	testutil.Equal(t, l.Apply(base).UI.Theme, "bbbb")
}

// TestFileLoader_BoolFalseOverridesTrueDefault guards the Go zero-value overlay
// trap: an explicit `false` in the file must override a base `true`, while an
// omitted key must leave the base `true` intact. BurntSushi only writes keys
// present in the document, so both halves must hold.
func TestFileLoader_BoolFalseOverridesTrueDefault(t *testing.T) {
	base := DefaultConfig() // ShowElapsed and ShowIcons both default true

	t.Run("explicit false wins", func(t *testing.T) {
		l := NewFileLoader(writeFile(t, `[ui]
show_elapsed = false`))
		got := l.Apply(base)
		testutil.Equal(t, got.UI.ShowElapsed, false)
		// An untouched bool keeps the base value.
		testutil.Equal(t, got.UI.ShowIcons, true)
	})

	t.Run("omitted key keeps base true", func(t *testing.T) {
		l := NewFileLoader(writeFile(t, `[ui]
theme = "x"`))
		got := l.Apply(base)
		testutil.Equal(t, got.UI.ShowElapsed, true)
		testutil.Equal(t, got.UI.ShowIcons, true)
	})
}

// TestFileLoader_ProjectSandboxDecodesFromTOML locks the behavior documented on
// ProjectSandboxConfig: although its fields carry no `toml:` tags, a
// [projects.<name>.sandbox] table still decodes (matched by lowercased field
// name) because the parent Project.Sandbox field is tagged.
func TestFileLoader_ProjectSandboxDecodesFromTOML(t *testing.T) {
	l := NewFileLoader(writeFile(t, `
[projects.demo]
path = "/repo/demo"

[projects.demo.sandbox]
enabled = true
denyread = ["/secret"]
`))
	base := DefaultConfig()

	got := l.Apply(base)
	testutil.NoError(t, l.Err())

	p, ok := got.Projects["demo"]
	if !ok {
		t.Fatal("demo project should be present after overlay")
	}
	testutil.Equal(t, p.Path, "/repo/demo")
	if p.Sandbox.Enabled == nil || !*p.Sandbox.Enabled {
		t.Fatal("project sandbox.enabled should decode to true from TOML")
	}
	testutil.DeepEqual(t, p.Sandbox.DenyRead, []string{"/secret"})
}

// TestFileLoader_ConcurrentApply exercises the loader's mutex under -race with
// many goroutines reading while the file changes underneath them.
func TestFileLoader_ConcurrentApply(t *testing.T) {
	l := NewFileLoader(writeFile(t, `[ui]
theme = "concurrent"`))
	base := DefaultConfig()

	// The file is never modified during the test, so every Apply must return
	// the same overridden value — this makes it a correctness check under -race,
	// not just a crash/deadlock probe.
	var bad atomic.Int64
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			for range 25 {
				if l.Apply(base).UI.Theme != "concurrent" {
					bad.Add(1)
				}
			}
		})
	}
	wg.Wait()

	testutil.Equal(t, bad.Load(), int64(0))
	testutil.Equal(t, l.Apply(base).UI.Theme, "concurrent")
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

	// A second call with the same persistent error exercises the
	// already-errored (no re-log) branch and must stay consistent.
	testutil.DeepEqual(t, l.Apply(base), base)
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

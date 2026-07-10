package skills

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

func TestBuiltinItems(t *testing.T) {
	items := BuiltinItems()
	testutil.True(t, len(items) > 0)

	byName := make(map[string]SkillItem)
	for _, it := range items {
		byName[it.Name] = it
	}

	// A handful of the known-shipped builtin skills, spot-checked by name.
	for _, name := range []string{"archive", "argus-complete", "argus-schedule", "hera", "hera-plan"} {
		it, ok := byName[name]
		testutil.True(t, ok)
		testutil.NotEqual(t, it.Description, "")
	}
}

func TestBuiltinItems_SortedByName(t *testing.T) {
	items := BuiltinItems()
	for i := 1; i < len(items); i++ {
		testutil.True(t, items[i-1].Name < items[i].Name)
	}
}

// TestTestGuard_BlocksPathsOutsideTempDir pins the guard that keeps
// EnsureBuiltinSkills from ever writing to a real ~/.argus/ during `go test`
// (mirroring internal/agent/cleanup.go's testGuard). Calls testGuard directly
// — a pure function over strings — so this is safe regardless of the guard's
// correctness: no filesystem operation is reachable from this test.
func TestTestGuard_BlocksPathsOutsideTempDir(t *testing.T) {
	fakeHome := filepath.Join(string(filepath.Separator), "argus-test-guard-fixture-home")
	t.Setenv("HOME", fakeHome)
	real := filepath.Join(fakeHome, ".argus", "skills")
	testutil.True(t, testGuard(real))
}

// TestTestGuard_AllowsPathsUnderOSTempDir confirms the tmp-dir exemption: a
// test that legitimately overrides HOME to a t.TempDir() must not be blocked.
func TestTestGuard_AllowsPathsUnderOSTempDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	real := filepath.Join(home, ".argus", "skills")
	testutil.False(t, testGuard(real))
}

// TestEnsureBuiltinSkills_RefusesRealDataDirDuringTest exercises the
// testGuard short-circuit inside EnsureBuiltinSkills itself (as opposed to
// TestTestGuard_BlocksPathsOutsideTempDir, which calls the guard directly).
// Safe regardless of the guard's correctness: the guard is the first
// statement after computing root, so no filesystem write is reachable before
// it returns.
func TestEnsureBuiltinSkills_RefusesRealDataDirDuringTest(t *testing.T) {
	fakeHome := filepath.Join(string(filepath.Separator), "argus-test-guard-fixture-home-2")
	t.Setenv("HOME", fakeHome)

	_, err := EnsureBuiltinSkills()
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "refusing to write")
}

func TestReadEmbeddedFrontmatterField_MissingPath(t *testing.T) {
	got := readEmbeddedFrontmatterField("builtin/does-not-exist/SKILL.md", "description")
	testutil.Equal(t, got, "")
}

// TestPruneStaleSkillDirs_ReadDirErrorPropagates confirms a non-NotExist
// ReadDir failure (as opposed to a simply-missing skillsDir) surfaces as an
// error rather than being silently treated like "nothing to prune".
func TestPruneStaleSkillDirs_ReadDirErrorPropagates(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, chmod cannot block reads")
	}
	dir := t.TempDir()
	unreadable := filepath.Join(dir, "skills")
	testutil.NoError(t, os.MkdirAll(unreadable, 0o755))
	testutil.NoError(t, os.Chmod(unreadable, 0o000))
	t.Cleanup(func() { os.Chmod(unreadable, 0o755) }) //nolint:errcheck,gosec // test fixture cleanup, no secrets

	err := pruneStaleSkillDirs(unreadable, map[string]bool{})
	testutil.Error(t, err)
}

// TestPruneStaleSkillDirs_MissingDirIsNotAnError confirms a skillsDir that
// doesn't exist yet (nothing materialized so far) is not treated as an error.
func TestPruneStaleSkillDirs_MissingDirIsNotAnError(t *testing.T) {
	err := pruneStaleSkillDirs(filepath.Join(t.TempDir(), "does-not-exist"), map[string]bool{})
	testutil.NoError(t, err)
}

// TestPruneStaleSkillDirs_RemoveAllErrorPropagates confirms a RemoveAll
// failure while pruning a stale directory (here: the parent lacks write
// permission, so the child entry can't be unlinked) surfaces as an error.
func TestPruneStaleSkillDirs_RemoveAllErrorPropagates(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, chmod cannot block writes")
	}
	skillsDir := t.TempDir()
	stale := filepath.Join(skillsDir, "stale")
	testutil.NoError(t, os.MkdirAll(stale, 0o755))
	testutil.NoError(t, os.Chmod(skillsDir, 0o500))  //nolint:gosec // test fixture, no secrets
	t.Cleanup(func() { os.Chmod(skillsDir, 0o755) }) //nolint:errcheck,gosec // test fixture cleanup, no secrets

	err := pruneStaleSkillDirs(skillsDir, map[string]bool{})
	testutil.Error(t, err)
}

// TestMaterializeSkillDir_MkdirAllBlockedByFile confirms a regular file
// occupying the destination path (blocking directory creation) surfaces as
// an error rather than silently skipping the skill.
func TestMaterializeSkillDir_MkdirAllBlockedByFile(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "archive")
	testutil.NoError(t, os.WriteFile(dest, []byte("not a directory"), 0o644))

	err := materializeSkillDir(filepath.Join(builtinRoot, "archive"), dest)
	testutil.Error(t, err)
}

// TestWriteIfChanged_MkdirAllErrorPropagates confirms a failure creating the
// destination's parent directory (here: its grandparent is read-only)
// surfaces as an error.
func TestWriteIfChanged_MkdirAllErrorPropagates(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, chmod cannot block writes")
	}
	parent := t.TempDir()
	testutil.NoError(t, os.Chmod(parent, 0o500))  //nolint:gosec // test fixture, no secrets
	t.Cleanup(func() { os.Chmod(parent, 0o755) }) //nolint:errcheck,gosec // test fixture cleanup, no secrets

	err := writeIfChanged(filepath.Join(parent, "newdir", "SKILL.md"), []byte("content"))
	testutil.Error(t, err)
}

// TestEnsureBuiltinSkills_UnwritableSkillDirFails pins the materialize
// error-propagation path: a skill directory that already exists but is
// read-only causes the per-file atomic write (CreateTemp) to fail, and that
// failure must surface through materializeSkillDir up to EnsureBuiltinSkills
// rather than being swallowed.
func TestEnsureBuiltinSkills_UnwritableSkillDirFails(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, chmod cannot block writes")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Create the tree writable first, then lock down only the leaf skill dir
	// — chaining MkdirAll(dest, 0o500) directly would self-sabotage (each
	// intermediate dir needs write access from its parent to be created).
	name := BuiltinItems()[0].Name
	dest := filepath.Join(home, ".argus", "skills", ".claude", "skills", name)
	testutil.NoError(t, os.MkdirAll(dest, 0o755))
	testutil.NoError(t, os.Chmod(dest, 0o500))  //nolint:gosec // test fixture, no secrets
	t.Cleanup(func() { os.Chmod(dest, 0o755) }) //nolint:errcheck,gosec // test fixture cleanup, no secrets

	_, err := EnsureBuiltinSkills()
	testutil.Error(t, err)
	testutil.Contains(t, err.Error(), "materialize")
}

func TestEnsureBuiltinSkills_FirstRunCreatesTree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root, err := EnsureBuiltinSkills()
	testutil.NoError(t, err)
	testutil.Equal(t, root, filepath.Join(home, ".argus", "skills"))

	for _, item := range BuiltinItems() {
		manifest := filepath.Join(root, ".claude", "skills", item.Name, "SKILL.md")
		data, readErr := os.ReadFile(manifest)
		testutil.NoError(t, readErr)
		testutil.True(t, len(data) > 0)
	}
}

func TestEnsureBuiltinSkills_NoRewriteWhenCurrent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root, err := EnsureBuiltinSkills()
	testutil.NoError(t, err)

	name := BuiltinItems()[0].Name
	manifest := filepath.Join(root, ".claude", "skills", name, "SKILL.md")
	before, statErr := os.Stat(manifest)
	testutil.NoError(t, statErr)

	// Ensure the filesystem's mtime resolution can't mask a spurious rewrite.
	time.Sleep(10 * time.Millisecond)

	_, err = EnsureBuiltinSkills()
	testutil.NoError(t, err)

	after, statErr := os.Stat(manifest)
	testutil.NoError(t, statErr)
	testutil.Equal(t, before.ModTime(), after.ModTime())
}

func TestEnsureBuiltinSkills_DriftedFileRestored(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root, err := EnsureBuiltinSkills()
	testutil.NoError(t, err)

	name := BuiltinItems()[0].Name
	manifest := filepath.Join(root, ".claude", "skills", name, "SKILL.md")
	original, err := os.ReadFile(manifest)
	testutil.NoError(t, err)

	testutil.NoError(t, os.WriteFile(manifest, []byte("tampered content"), 0o644))

	_, err = EnsureBuiltinSkills()
	testutil.NoError(t, err)

	restored, err := os.ReadFile(manifest)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, restored, original)
}

func TestEnsureBuiltinSkills_StaleSkillDirPruned(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	root, err := EnsureBuiltinSkills()
	testutil.NoError(t, err)

	skillsDir := filepath.Join(root, ".claude", "skills")
	staleDir := filepath.Join(skillsDir, "no-longer-shipped")
	testutil.NoError(t, os.MkdirAll(staleDir, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(staleDir, "SKILL.md"), []byte("stale"), 0o644))

	_, err = EnsureBuiltinSkills()
	testutil.NoError(t, err)

	_, statErr := os.Stat(staleDir)
	testutil.True(t, os.IsNotExist(statErr))

	// Skills still in the embedded set must survive the prune pass.
	for _, item := range BuiltinItems() {
		_, statErr := os.Stat(filepath.Join(skillsDir, item.Name))
		testutil.NoError(t, statErr)
	}
}

func TestLoadSkills_BuiltinsMerged(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	items := LoadSkills(nil)
	byName := make(map[string]SkillItem)
	for _, it := range items {
		byName[it.Name] = it
	}

	builtinName := BuiltinItems()[0].Name
	_, ok := byName[builtinName]
	testutil.True(t, ok)
}

func TestLoadSkills_PersonalSkillShadowsBuiltin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	builtinName := BuiltinItems()[0].Name

	personal := filepath.Join(home, ".claude", "skills", builtinName)
	testutil.NoError(t, os.MkdirAll(personal, 0o755))
	testutil.NoError(t, os.WriteFile(filepath.Join(personal, "SKILL.md"),
		[]byte("---\ndescription: personal override\n---\n"), 0o644))

	items := LoadSkills(nil)
	count := 0
	var desc string
	for _, it := range items {
		if it.Name == builtinName {
			count++
			desc = it.Description
		}
	}
	testutil.Equal(t, count, 1)
	testutil.Equal(t, desc, "personal override")
}

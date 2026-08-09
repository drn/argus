package gitutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestIsAncestor(t *testing.T) {
	t.Run("true when commit is an ancestor of target", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		baseSHA := headSHA(t, dir)
		gitRun(t, dir, "checkout", "-b", "feature")
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun(t, dir, "add", "f.txt")
		gitRun(t, dir, "commit", "-m", "add f")

		ok, err := IsAncestor(dir, baseSHA, "feature")
		testutil.NoError(t, err)
		testutil.Equal(t, ok, true)
	})

	t.Run("false when commit is not an ancestor of target", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		gitRun(t, dir, "checkout", "-b", "feature")
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun(t, dir, "add", "f.txt")
		gitRun(t, dir, "commit", "-m", "add f")
		featureSHA := headSHA(t, dir)

		gitRun(t, dir, "checkout", "master")

		ok, err := IsAncestor(dir, featureSHA, "master")
		testutil.NoError(t, err)
		testutil.Equal(t, ok, false)
	})

	t.Run("branch name works directly, not just SHAs", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		gitRun(t, dir, "checkout", "-b", "feature")

		ok, err := IsAncestor(dir, "feature", "master")
		testutil.NoError(t, err)
		testutil.Equal(t, ok, true)
	})

	t.Run("error for an unresolvable commit/branch", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		_, err := IsAncestor(dir, "does-not-exist", "master")
		testutil.Error(t, err)
	})

	t.Run("error for a non-repo directory", func(t *testing.T) {
		_, err := IsAncestor(t.TempDir(), "a", "b")
		testutil.Error(t, err)
	})

	t.Run("error for empty arguments", func(t *testing.T) {
		_, err := IsAncestor("", "a", "b")
		testutil.Error(t, err)
	})

	t.Run("a leading-dash branch name is treated as a positional arg, not an option", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		// Should fail as an unresolvable ref, not be silently swallowed as a
		// git option (argument-injection guard).
		_, err := IsAncestor(dir, "-not-a-real-branch", "master")
		testutil.Error(t, err)
	})
}

func TestResolveDefaultBranch(t *testing.T) {
	t.Run("uses configured value when non-empty, without probing the remote", func(t *testing.T) {
		short, ref, err := ResolveDefaultBranch("/nonexistent/path/should/not/be/touched", "drn/master")
		testutil.NoError(t, err)
		testutil.Equal(t, short, "master")
		testutil.Equal(t, ref, "drn/master")
	})

	t.Run("configured value with no slash resolves short == ref", func(t *testing.T) {
		short, ref, err := ResolveDefaultBranch("/nonexistent", "main")
		testutil.NoError(t, err)
		testutil.Equal(t, short, "main")
		testutil.Equal(t, ref, "main")
	})

	t.Run("falls back to origin/HEAD when configured is empty", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		sha := headSHA(t, dir)
		gitRun(t, dir, "update-ref", "refs/remotes/origin/main", sha)
		gitRun(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

		short, ref, err := ResolveDefaultBranch(dir, "")
		testutil.NoError(t, err)
		testutil.Equal(t, short, "main")
		testutil.Equal(t, ref, "origin/main")
	})

	t.Run("falls back to priorityBranches when no origin/HEAD", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		sha := headSHA(t, dir)
		gitRun(t, dir, "update-ref", "refs/remotes/origin/master", sha)

		short, ref, err := ResolveDefaultBranch(dir, "")
		testutil.NoError(t, err)
		testutil.Equal(t, short, "master")
		testutil.Equal(t, ref, "origin/master")
	})

	t.Run("falls back further to a local master/main branch", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())

		short, ref, err := ResolveDefaultBranch(dir, "")
		testutil.NoError(t, err)
		testutil.Equal(t, short, "master")
		testutil.Equal(t, ref, "master")
	})

	t.Run("error when nothing resolves", func(t *testing.T) {
		_, _, err := ResolveDefaultBranch(t.TempDir(), "")
		testutil.Error(t, err)
	})
}

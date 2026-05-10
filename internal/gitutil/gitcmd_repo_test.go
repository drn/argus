package gitutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// initRepo initializes a git repo at dir with one initial commit on master.
// Returns dir for chaining.
func initRepo(t *testing.T, dir string) string {
	t.Helper()
	gitInit := exec.Command("git", "init", "-b", "master", dir)
	if out, err := gitInit.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	gitConfig(t, dir, "user.email", "test@example.com")
	gitConfig(t, dir, "user.name", "Test")
	gitConfig(t, dir, "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-m", "init")
	return dir
}

func gitConfig(t *testing.T, dir, key, value string) {
	t.Helper()
	gitRun(t, dir, "config", key, value)
}

func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestFetchGitStatus(t *testing.T) {
	t.Run("empty worktree returns empty msg", func(t *testing.T) {
		got := FetchGitStatus("task-1", "")
		testutil.Equal(t, got.TaskID, "task-1")
		testutil.Equal(t, got.Status, "")
		testutil.Equal(t, got.Diff, "")
		testutil.Equal(t, got.BranchDiff, "")
	})

	t.Run("clean repo", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		got := FetchGitStatus("t-clean", dir)
		testutil.Equal(t, got.TaskID, "t-clean")
		testutil.Equal(t, got.Status, "")
	})

	t.Run("dirty worktree captures status and diff", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		// Modify the tracked file
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# init\nadded\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// And add an untracked file
		if err := os.WriteFile(filepath.Join(dir, "newfile.txt"), []byte("new\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		got := FetchGitStatus("t-dirty", dir)
		testutil.Equal(t, got.TaskID, "t-dirty")
		testutil.Contains(t, got.Status, "README.md")
		testutil.Contains(t, got.Status, "newfile.txt")
		testutil.Contains(t, got.Diff, "README.md")
	})

	t.Run("branch with extra commit captures branch diff", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		// Make a new branch and add a commit
		gitRun(t, dir, "checkout", "-b", "feature")
		if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun(t, dir, "add", "feature.txt")
		gitRun(t, dir, "commit", "-m", "add feature")

		got := FetchGitStatus("t-branch", dir)
		testutil.Contains(t, got.BranchDiff, "feature.txt")
		testutil.Contains(t, got.BranchFiles, "feature.txt")
	})
}

func TestFindMergeBase(t *testing.T) {
	t.Run("no merge base for non-repo", func(t *testing.T) {
		// runGit returns "" with err, so findMergeBase returns "".
		got := findMergeBase("/nonexistent/path")
		testutil.Equal(t, got, "")
	})

	t.Run("falls back to master branch", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		gitRun(t, dir, "checkout", "-b", "feature")
		got := findMergeBase(dir)
		if got == "" {
			t.Error("expected non-empty merge-base")
		}
	})

	t.Run("falls back to main branch when master absent", func(t *testing.T) {
		dir := t.TempDir()
		gitInit := exec.Command("git", "init", "-b", "main", dir)
		if out, err := gitInit.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		gitConfig(t, dir, "user.email", "t@example.com")
		gitConfig(t, dir, "user.name", "T")
		gitConfig(t, dir, "commit.gpgsign", "false")
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("v1"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun(t, dir, "add", ".")
		gitRun(t, dir, "commit", "-m", "init")
		gitRun(t, dir, "checkout", "-b", "feature")

		got := findMergeBase(dir)
		if got == "" {
			t.Error("expected non-empty merge-base via main fallback")
		}
	})
}

func TestFetchFileDiff(t *testing.T) {
	t.Run("empty worktree returns empty msg", func(t *testing.T) {
		got := FetchFileDiff("tid", "", "file.txt")
		testutil.Equal(t, got.TaskID, "tid")
		testutil.Equal(t, got.Diff, "")
	})

	t.Run("empty filePath returns empty msg", func(t *testing.T) {
		got := FetchFileDiff("tid", "/some/dir", "")
		testutil.Equal(t, got.Diff, "")
	})

	t.Run("uncommitted modification produces diff", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# init\nfoo\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		got := FetchFileDiff("t1", dir, "README.md")
		testutil.Contains(t, got.Diff, "README.md")
		testutil.Contains(t, got.Diff, "foo")
	})

	t.Run("untracked file produces add diff", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		got := FetchFileDiff("t2", dir, "untracked.txt")
		testutil.Contains(t, got.Diff, "untracked.txt")
	})

	t.Run("branch-only commit produces diff via merge-base", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		gitRun(t, dir, "checkout", "-b", "feature")
		if err := os.WriteFile(filepath.Join(dir, "added.txt"), []byte("added\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun(t, dir, "add", "added.txt")
		gitRun(t, dir, "commit", "-m", "add file")

		got := FetchFileDiff("t3", dir, "added.txt")
		testutil.Contains(t, got.Diff, "added.txt")
	})
}

func TestFetchDirFiles(t *testing.T) {
	t.Run("empty worktree", func(t *testing.T) {
		got := FetchDirFiles("tid", "", "sub")
		testutil.Equal(t, got.TaskID, "tid")
		testutil.Equal(t, len(got.Files), 0)
	})

	t.Run("empty dirPath", func(t *testing.T) {
		got := FetchDirFiles("tid", t.TempDir(), "")
		testutil.Equal(t, len(got.Files), 0)
	})

	t.Run("path traversal rejected", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		got := FetchDirFiles("tid", dir, "../escape")
		testutil.Equal(t, len(got.Files), 0)
	})

	t.Run("lists changed and untracked files", func(t *testing.T) {
		dir := initRepo(t, t.TempDir())
		// Make a subdirectory with mixed files
		sub := filepath.Join(dir, "sub")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		// Tracked + modified (need to commit the tracked file first)
		if err := os.WriteFile(filepath.Join(sub, "tracked.txt"), []byte("v1\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitRun(t, dir, "add", "sub/tracked.txt")
		gitRun(t, dir, "commit", "-m", "add tracked")
		// Modify it
		if err := os.WriteFile(filepath.Join(sub, "tracked.txt"), []byte("v2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// New untracked file
		if err := os.WriteFile(filepath.Join(sub, "new.txt"), []byte("new\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		got := FetchDirFiles("t-dir", dir, "sub")
		if len(got.Files) < 2 {
			t.Fatalf("expected at least 2 files, got %d: %+v", len(got.Files), got.Files)
		}
		// Check we get a ChangedFile back with status set.
		paths := map[string]string{}
		for _, f := range got.Files {
			paths[f.Path] = f.Status
		}
		if _, ok := paths["sub/tracked.txt"]; !ok {
			t.Errorf("missing sub/tracked.txt; got %v", paths)
		}
		if _, ok := paths["sub/new.txt"]; !ok {
			t.Errorf("missing sub/new.txt; got %v", paths)
		}
	})
}

func TestListRemoteBranches_Repo(t *testing.T) {
	dir := initRepo(t, t.TempDir())
	// Create a fake remote-tracking ref by adding a remote and fetching a fake URL.
	// Instead we just create the ref directly via update-ref.
	headSHA := func() string {
		c := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
		out, err := c.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out[:len(out)-1])
	}()
	gitRun(t, dir, "update-ref", "refs/remotes/origin/master", headSHA)
	gitRun(t, dir, "update-ref", "refs/remotes/origin/feature", headSHA)
	// HEAD ref should be ignored.
	gitRun(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/master")

	got := ListRemoteBranches(dir)
	if len(got) == 0 {
		t.Fatal("expected non-empty branches list")
	}
	// origin/master should come first (priority).
	testutil.Equal(t, got[0], "origin/master")
	// HEAD ref should be filtered out.
	for _, b := range got {
		if b == "origin/HEAD" {
			t.Error("origin/HEAD should be filtered out")
		}
	}
}

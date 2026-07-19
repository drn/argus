package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/drn/argus/internal/testutil"
)

// TestRemoveWorktreeAndBranch_ConcurrentSameRepo reproduces the exact scenario
// hypothesized for a bulk Hera cascade-nuke: many goroutines (one per nuked
// task, per heraDoCascadeNuke) call RemoveWorktreeAndBranch concurrently
// against worktrees/branches that all share the SAME repo. Run under
// `-race -count=1` this confirms whether concurrent git subprocess fan-out
// against one repo's `.git` administrative area produces a Go-level data race
// or panic. It does not — each call shells out to independent `git` processes
// with no shared Go memory to race on — but the git-level contention (lock
// files, `worktree prune` racing a concurrent `worktree remove`) is real, so
// this also asserts the cleanup still converges (every worktree/branch gone)
// rather than silently leaving some behind.
func TestRemoveWorktreeAndBranch_ConcurrentSameRepo(t *testing.T) {
	repoDir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")

	const n = 12
	wtBase := filepath.Join(t.TempDir(), ".argus", "worktrees", "proj")
	if err := os.MkdirAll(wtBase, 0o755); err != nil {
		t.Fatal(err)
	}

	type target struct {
		path, branch string
	}
	targets := make([]target, n)
	for i := 0; i < n; i++ {
		branch := fmt.Sprintf("argus/task-%d", i)
		path := filepath.Join(wtBase, fmt.Sprintf("task-%d", i))
		run("worktree", "add", "-b", branch, path, "HEAD")
		targets[i] = target{path: path, branch: branch}
	}

	// Fan out one unrecovered-goroutine-equivalent per target, all racing
	// against the SAME repoDir — mirrors heraDoCascadeNuke's
	// `go func(){ agent.RemoveWorktreeAndBranch(...) }()` fan-out for a bulk
	// nuke of many tasks under one coordinator/project.
	var wg sync.WaitGroup
	panics := make(chan any, n)
	for _, tg := range targets {
		wg.Add(1)
		go func(tg target) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panics <- r
				}
			}()
			RemoveWorktreeAndBranch(tg.path, tg.branch, repoDir)
		}(tg)
	}
	wg.Wait()
	close(panics)

	for p := range panics {
		t.Fatalf("RemoveWorktreeAndBranch panicked under concurrent same-repo load: %v", p)
	}

	for _, tg := range targets {
		if dirExists(tg.path) {
			t.Errorf("worktree %q should have been removed", tg.path)
		}
		if branchExists(repoDir, tg.branch) {
			t.Errorf("branch %q should have been deleted", tg.branch)
		}
	}
}

// TestLockRepo_SerializesSameRepoParallelizesAcrossRepos pins the per-repo
// throttle's contract directly: concurrent callers targeting the SAME repoDir
// never overlap (Lock/Unlock alternate strictly), while callers targeting
// DIFFERENT repoDirs run fully in parallel (never serialized against each
// other). This is the invariant RemoveWorktreeAndBranch's per-repo lock relies
// on — see gotchas/worktree.md.
func TestLockRepo_SerializesSameRepoParallelizesAcrossRepos(t *testing.T) {
	t.Run("same repo never overlaps", func(t *testing.T) {
		const n = 8
		var active, maxActive int32
		var mu sync.Mutex
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				unlock := lockRepo("/repo/shared")
				defer unlock()
				mu.Lock()
				active++
				if active > maxActive {
					maxActive = active
				}
				mu.Unlock()
				// Yield to give a buggy (non-serializing) implementation a
				// chance to overlap.
				for i := 0; i < 1000; i++ {
				}
				mu.Lock()
				active--
				mu.Unlock()
			}()
		}
		wg.Wait()
		testutil.Equal(t, maxActive, int32(1))
	})

	t.Run("different repos run in parallel", func(t *testing.T) {
		unlockA := lockRepo("/repo/a")
		defer unlockA()

		done := make(chan struct{})
		go func() {
			unlockB := lockRepo("/repo/b")
			defer unlockB()
			close(done)
		}()

		select {
		case <-done:
			// A different repoDir acquired its lock without waiting on repo/a.
		case <-time.After(2 * time.Second):
			t.Fatal("lockRepo(\"/repo/b\") blocked on an unrelated repo's lock")
		}
	})

	t.Run("empty repoDir is a no-op", func(t *testing.T) {
		unlock := lockRepo("")
		unlock() // must not panic
	})
}

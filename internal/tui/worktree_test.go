package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/drn/argus/internal/agent"
)

func TestCountOrphanedWorktrees(t *testing.T) {
	// Create a fake worktree structure in a temp dir.
	wtRoot := filepath.Join(t.TempDir(), "worktrees")
	os.MkdirAll(filepath.Join(wtRoot, "proj1", "task-a"), 0o755) //nolint:errcheck
	os.MkdirAll(filepath.Join(wtRoot, "proj1", "task-b"), 0o755) //nolint:errcheck
	os.MkdirAll(filepath.Join(wtRoot, "proj2", "task-c"), 0o755) //nolint:errcheck

	// task-a is known, task-b and task-c are orphans.
	known := map[string]bool{
		filepath.Join(wtRoot, "proj1", "task-a"): true,
	}

	count := countOrphanedWorktrees(wtRoot, known)
	if count != 2 {
		t.Errorf("expected 2 orphans, got %d", count)
	}
}

func TestSweepOrphanedWorktrees(t *testing.T) {
	wtRoot := filepath.Join(t.TempDir(), ".argus", "worktrees")
	orphanPath := filepath.Join(wtRoot, "proj1", "orphan-task")
	os.MkdirAll(orphanPath, 0o755) //nolint:errcheck

	// Write a dummy file so the dir is non-empty.
	os.WriteFile(filepath.Join(orphanPath, "dummy.txt"), []byte("x"), 0o644) //nolint:errcheck

	known := map[string]bool{} // no known paths — everything is an orphan

	// Pass empty projects map — RemoveWorktreeAndBranch will skip git ops
	// but os.RemoveAll will still clean the dir.
	swept := sweepOrphanedWorktrees(wtRoot, known, map[string]string{})
	if swept != 1 {
		t.Errorf("expected 1 swept, got %d", swept)
	}

	// The orphan path should be gone (IsWorktreeSubdir check will pass since
	// the path contains /.argus/worktrees/).
	if agent.DirExists(orphanPath) {
		t.Error("orphan directory should have been removed")
	}

	// Parent project dir should also be cleaned up since it's now empty.
	projDir := filepath.Join(wtRoot, "proj1")
	if agent.DirExists(projDir) {
		t.Error("empty project directory should have been removed")
	}
}

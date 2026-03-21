package gitutil

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestSortBranchesWithPriority(t *testing.T) {
	t.Run("priority branches first", func(t *testing.T) {
		branches := []string{"origin/dev", "origin/main", "upstream/master", "origin/feature-a"}
		got := sortBranchesWithPriority(branches)
		testutil.DeepEqual(t, got, []string{
			"upstream/master",
			"origin/main",
			"origin/dev",
			"origin/feature-a",
		})
	})

	t.Run("all priority branches in order", func(t *testing.T) {
		branches := []string{"origin/main", "origin/master", "upstream/main", "upstream/master"}
		got := sortBranchesWithPriority(branches)
		testutil.DeepEqual(t, got, []string{
			"upstream/master",
			"origin/master",
			"upstream/main",
			"origin/main",
		})
	})

	t.Run("no priority branches", func(t *testing.T) {
		branches := []string{"origin/dev", "origin/feature-b", "origin/feature-a"}
		got := sortBranchesWithPriority(branches)
		testutil.DeepEqual(t, got, []string{
			"origin/dev",
			"origin/feature-a",
			"origin/feature-b",
		})
	})

	t.Run("empty input", func(t *testing.T) {
		got := sortBranchesWithPriority(nil)
		testutil.Nil(t, got)
	})

	t.Run("single branch", func(t *testing.T) {
		got := sortBranchesWithPriority([]string{"origin/main"})
		testutil.DeepEqual(t, got, []string{"origin/main"})
	})
}

func TestListRemoteBranches_EmptyPath(t *testing.T) {
	got := ListRemoteBranches("")
	testutil.Nil(t, got)
}

func TestListRemoteBranches_InvalidPath(t *testing.T) {
	got := ListRemoteBranches("/nonexistent/path")
	testutil.Nil(t, got)
}

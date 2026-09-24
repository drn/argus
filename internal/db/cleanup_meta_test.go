package db

import (
	"sort"
	"testing"

	"github.com/drn/argus/internal/testutil"
)

func TestDB_ExcludeCleanupBranch_RecordsBranch(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.ExcludeCleanupBranch("task-1", "argus/worker-a"))

	got, err := d.ExcludedCleanupBranches("task-1")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got, []string{"argus/worker-a"})
}

func TestDB_ExcludeCleanupBranch_IdempotentReRecording(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.ExcludeCleanupBranch("task-1", "argus/worker-a"))
	testutil.NoError(t, d.ExcludeCleanupBranch("task-1", "argus/worker-a"))

	got, err := d.ExcludedCleanupBranches("task-1")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got, []string{"argus/worker-a"})
}

func TestDB_ExcludedCleanupBranches_EmptyWhenNoneRecorded(t *testing.T) {
	d := testDB(t)
	got, err := d.ExcludedCleanupBranches("task-1")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)
	if got == nil {
		t.Fatal("expected a non-nil empty slice, got nil")
	}
}

func TestDB_ExcludedCleanupBranches_MultipleBranchesForOneTask(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.ExcludeCleanupBranch("task-1", "argus/worker-a"))
	testutil.NoError(t, d.ExcludeCleanupBranch("task-1", "argus/worker-b"))
	testutil.NoError(t, d.ExcludeCleanupBranch("task-1", "argus/worker-c"))

	got, err := d.ExcludedCleanupBranches("task-1")
	testutil.NoError(t, err)
	sort.Strings(got)
	testutil.DeepEqual(t, got, []string{"argus/worker-a", "argus/worker-b", "argus/worker-c"})
}

func TestDB_ExcludeCleanupBranch_ScopedPerTask(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.ExcludeCleanupBranch("task-1", "argus/worker-a"))
	testutil.NoError(t, d.ExcludeCleanupBranch("task-2", "argus/worker-z"))

	got1, err := d.ExcludedCleanupBranches("task-1")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got1, []string{"argus/worker-a"})

	got2, err := d.ExcludedCleanupBranches("task-2")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got2, []string{"argus/worker-z"})
}

func TestDB_ExcludeCleanupBranch_DoesNotCollideWithCleanupVerdictCache(t *testing.T) {
	d := testDB(t)
	// The "cleanup" namespace already caches merge-safety verdicts under
	// keys "safe"/"tier"/"reason" (internal/api/cleanup_candidates.go). A
	// branch literally named one of those must not be mistaken for a verdict
	// row, so excluded-branch bookkeeping lives under its own namespace.
	testutil.NoError(t, d.SetMeta("task-1", "cleanup", "safe", "true"))
	testutil.NoError(t, d.ExcludeCleanupBranch("task-1", "safe"))

	got, err := d.ExcludedCleanupBranches("task-1")
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got, []string{"safe"})

	verdict, err := d.ListMeta("task-1", "cleanup")
	testutil.NoError(t, err)
	testutil.Equal(t, len(verdict), 1)
	testutil.Equal(t, verdict[0].Value, "true")
}

func TestDB_ExcludeCleanupBranch_ValidationErrors(t *testing.T) {
	d := testDB(t)
	if err := d.ExcludeCleanupBranch("", "argus/worker-a"); err == nil {
		t.Fatal("expected validation error for empty task_id")
	}
	if err := d.ExcludeCleanupBranch("task-1", ""); err == nil {
		t.Fatal("expected validation error for empty branch name")
	}
}

func TestDB_CleanupMeta_ErrorBranchesAfterClose(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.ExcludeCleanupBranch("t", "argus/worker-a"))
	testutil.NoError(t, d.Close())

	t.Run("ExcludeCleanupBranch", func(t *testing.T) {
		if err := d.ExcludeCleanupBranch("t", "argus/worker-a"); err == nil {
			t.Fatal("expected error on closed DB")
		}
	})
	t.Run("ExcludedCleanupBranches", func(t *testing.T) {
		if _, err := d.ExcludedCleanupBranches("t"); err == nil {
			t.Fatal("expected error on closed DB")
		}
	})
}

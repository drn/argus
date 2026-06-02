package db

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

func TestDB_SetMeta_Insert(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetMeta("task-1", "ns-a", "role", "coordinator"))

	entries, err := d.ListMeta("task-1", "ns-a")
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 1)
	testutil.Equal(t, entries[0].Key, "role")
	testutil.Equal(t, entries[0].Value, "coordinator")
	if entries[0].UpdatedAt.IsZero() {
		t.Fatal("expected UpdatedAt to be stamped")
	}
}

func TestDB_SetMeta_Update(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetMeta("task-1", "ns-a", "role", "coordinator"))
	entries, err := d.ListMeta("task-1", "ns-a")
	testutil.NoError(t, err)
	first := entries[0].UpdatedAt
	// SQLite RFC3339Nano resolution is fine, but two writes in the same
	// nanosecond would compare equal. Sleep to guarantee strict ordering.
	time.Sleep(2 * time.Millisecond)
	testutil.NoError(t, d.SetMeta("task-1", "ns-a", "role", "worker"))

	entries, err = d.ListMeta("task-1", "ns-a")
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 1)
	testutil.Equal(t, entries[0].Value, "worker")
	if !entries[0].UpdatedAt.After(first) {
		t.Fatalf("expected UpdatedAt to advance: first=%v second=%v", first, entries[0].UpdatedAt)
	}
}

func TestDB_SetMeta_Validation(t *testing.T) {
	d := testDB(t)
	cases := []struct {
		name            string
		taskID, ns, key string
	}{
		{"empty task_id", "", "ns", "k"},
		{"empty namespace", "t", "", "k"},
		{"empty key", "t", "ns", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := d.SetMeta(tc.taskID, tc.ns, tc.key, "v")
			if err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestDB_SetMetaBatch(t *testing.T) {
	d := testDB(t)
	err := d.SetMetaBatch("task-1", "ns-a", map[string]string{
		"role":   "worker",
		"status": "active",
		"label":  "alpha",
	})
	testutil.NoError(t, err)

	entries, err := d.ListMeta("task-1", "ns-a")
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 3)

	values := map[string]string{}
	for _, e := range entries {
		values[e.Key] = e.Value
	}
	testutil.Equal(t, values["role"], "worker")
	testutil.Equal(t, values["status"], "active")
	testutil.Equal(t, values["label"], "alpha")
}

func TestDB_SetMetaBatch_EmptyMapIsNoop(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetMetaBatch("task-1", "ns-a", nil))
	testutil.NoError(t, d.SetMetaBatch("task-1", "ns-a", map[string]string{}))

	entries, err := d.ListMeta("task-1", "ns-a")
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 0)
}

func TestDB_SetMetaBatch_Atomic_RejectsAnyInvalidKey(t *testing.T) {
	d := testDB(t)
	err := d.SetMetaBatch("task-1", "ns-a", map[string]string{
		"good": "v",
		"":     "bad",
	})
	if err == nil {
		t.Fatal("expected validation error from empty key")
	}
	// No partial write — atomic via WithTx.
	entries, err := d.ListMeta("task-1", "ns-a")
	testutil.NoError(t, err)
	testutil.Equal(t, len(entries), 0)
}

func TestDB_ListMeta_NamespaceIsolation(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetMeta("task-1", "ns-a", "k", "v1"))
	testutil.NoError(t, d.SetMeta("task-1", "ns-b", "k", "v2"))
	testutil.NoError(t, d.SetMeta("task-2", "ns-a", "k", "v3"))

	a, err := d.ListMeta("task-1", "ns-a")
	testutil.NoError(t, err)
	testutil.Equal(t, len(a), 1)
	testutil.Equal(t, a[0].Value, "v1")

	b, err := d.ListMeta("task-1", "ns-b")
	testutil.NoError(t, err)
	testutil.Equal(t, len(b), 1)
	testutil.Equal(t, b[0].Value, "v2")

	a2, err := d.ListMeta("task-2", "ns-a")
	testutil.NoError(t, err)
	testutil.Equal(t, len(a2), 1)
	testutil.Equal(t, a2[0].Value, "v3")
}

func TestDB_ListMeta_EmptyNamespaceReturnsAllNamespaces(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetMeta("task-1", "ns-a", "k", "v1"))
	testutil.NoError(t, d.SetMeta("task-1", "ns-b", "k", "v2"))
	testutil.NoError(t, d.SetMeta("task-2", "ns-a", "k", "v3"))

	got, err := d.ListMeta("task-1", "")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 2)
	seen := map[string]string{}
	for _, e := range got {
		seen[e.Namespace+"/"+e.Key] = e.Value
	}
	testutil.Equal(t, seen["ns-a/k"], "v1")
	testutil.Equal(t, seen["ns-b/k"], "v2")
}

func TestDB_ListMeta_ReturnsOrderedByKey(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetMeta("t", "ns", "zeta", "z"))
	testutil.NoError(t, d.SetMeta("t", "ns", "alpha", "a"))
	testutil.NoError(t, d.SetMeta("t", "ns", "mu", "m"))

	got, err := d.ListMeta("t", "ns")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 3)
	testutil.Equal(t, got[0].Key, "alpha")
	testutil.Equal(t, got[1].Key, "mu")
	testutil.Equal(t, got[2].Key, "zeta")
}

func TestDB_DeleteMetaForTask(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetMeta("task-1", "ns-a", "k1", "v1"))
	testutil.NoError(t, d.SetMeta("task-1", "ns-b", "k2", "v2"))
	testutil.NoError(t, d.SetMeta("task-2", "ns-a", "k1", "v3"))

	n, err := d.DeleteMetaForTask("task-1")
	testutil.NoError(t, err)
	testutil.Equal(t, n, 2)

	got, err := d.ListMeta("task-1", "")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)

	// Other task untouched.
	got, err = d.ListMeta("task-2", "ns-a")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
}

func TestDB_DeleteMetaForTask_NoRows(t *testing.T) {
	d := testDB(t)
	n, err := d.DeleteMetaForTask("missing-task")
	testutil.NoError(t, err)
	testutil.Equal(t, n, 0)
}

func TestDB_Delete_CascadesMeta(t *testing.T) {
	d := testDB(t)
	task := &model.Task{Name: "doomed"}
	testutil.NoError(t, d.Add(task))
	testutil.NoError(t, d.SetMeta(task.ID, "ns-a", "k", "v"))

	testutil.NoError(t, d.Delete(task.ID))

	got, err := d.ListMeta(task.ID, "")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)
}

func TestDB_SetArchived_CascadesMeta(t *testing.T) {
	d := testDB(t)
	task := &model.Task{Name: "to-archive"}
	testutil.NoError(t, d.Add(task))
	testutil.NoError(t, d.SetMeta(task.ID, "ns-a", "k", "v"))

	testutil.NoError(t, d.SetArchived(task.ID, true))

	got, err := d.ListMeta(task.ID, "")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)
}

func TestDB_SetArchived_UnarchiveLeavesMetaAlone(t *testing.T) {
	d := testDB(t)
	task := &model.Task{Name: "t", Archived: true}
	testutil.NoError(t, d.Add(task))
	// Seed meta AFTER archive so we have something to retain when we
	// flip back to unarchived. Mirrors the message-cascade pattern.
	testutil.NoError(t, d.SetMeta(task.ID, "ns-a", "k", "v"))

	testutil.NoError(t, d.SetArchived(task.ID, false))

	got, err := d.ListMeta(task.ID, "ns-a")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
}

// TestDB_TaskMeta_ErrorBranchesAfterClose pokes the database-closed error
// paths so every task_meta method's SQL-failure branch shows up in coverage.
func TestDB_TaskMeta_ErrorBranchesAfterClose(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetMeta("t", "ns", "k", "v"))
	testutil.NoError(t, d.Close())

	t.Run("SetMeta", func(t *testing.T) {
		if err := d.SetMeta("t", "ns", "k", "v"); err == nil {
			t.Fatal("expected error on closed DB")
		}
	})
	t.Run("SetMetaBatch", func(t *testing.T) {
		err := d.SetMetaBatch("t", "ns", map[string]string{"k": "v"})
		if err == nil {
			t.Fatal("expected error on closed DB")
		}
	})
	t.Run("ListMeta", func(t *testing.T) {
		if _, err := d.ListMeta("t", "ns"); err == nil {
			t.Fatal("expected error on closed DB")
		}
	})
	t.Run("DeleteMetaForTask", func(t *testing.T) {
		if _, err := d.DeleteMetaForTask("t"); err == nil {
			t.Fatal("expected error on closed DB")
		}
	})
}

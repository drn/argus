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

// TestDB_ListMetaByNamespace verifies the batch-read shape: one indexed
// query returns all (taskID → key → value) rows for the given namespace.
func TestDB_ListMetaByNamespace(t *testing.T) {
	d := testDB(t)

	// Two tasks, each with two keys in the "pr" namespace.
	testutil.NoError(t, d.SetMeta("task-1", "pr", "state", "approved"))
	testutil.NoError(t, d.SetMeta("task-1", "pr", "url", "https://github.com/x/y/pull/1"))
	testutil.NoError(t, d.SetMeta("task-2", "pr", "state", "awaiting-review"))
	testutil.NoError(t, d.SetMeta("task-2", "pr", "url", "https://github.com/x/y/pull/2"))
	// Unrelated namespace rows — must not appear in the result.
	testutil.NoError(t, d.SetMeta("task-1", "other", "k", "v"))

	got, err := d.ListMetaByNamespace("pr")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 2)

	t1 := got["task-1"]
	testutil.Equal(t, t1["state"], "approved")
	testutil.Equal(t, t1["url"], "https://github.com/x/y/pull/1")

	t2 := got["task-2"]
	testutil.Equal(t, t2["state"], "awaiting-review")
	testutil.Equal(t, t2["url"], "https://github.com/x/y/pull/2")
}

func TestDB_ListMetaByNamespace_Empty(t *testing.T) {
	d := testDB(t)
	got, err := d.ListMetaByNamespace("pr")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 0)
}

func TestDB_ListMetaByNamespace_MultipleNamespacesIsolated(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetMeta("task-1", "pr", "state", "approved"))
	testutil.NoError(t, d.SetMeta("task-1", "mcp", "last_run", "now"))

	got, err := d.ListMetaByNamespace("pr")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)

	// "mcp" namespace must not appear.
	if _, ok := got["task-1"]["last_run"]; ok {
		t.Error("expected no mcp namespace key in pr result")
	}
}

func TestDB_DeleteMetaForTask_ClearsPRNamespace(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetMeta("task-1", "pr", "state", "approved"))
	testutil.NoError(t, d.SetMeta("task-1", "pr", "url", "https://github.com/x/y/pull/1"))
	testutil.NoError(t, d.SetMeta("task-2", "pr", "state", "draft"))

	n, err := d.DeleteMetaForTask("task-1")
	testutil.NoError(t, err)
	testutil.Equal(t, n, 2)

	got, err := d.ListMetaByNamespace("pr")
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 1)
	if _, ok := got["task-2"]; !ok {
		t.Error("expected task-2 to remain after deleting task-1")
	}
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
	t.Run("ListMetaByNamespace", func(t *testing.T) {
		if _, err := d.ListMetaByNamespace("ns"); err == nil {
			t.Fatal("expected error on closed DB")
		}
	})
}

// dvDB opens a second connection to the same file so data_version (which is
// blind to a connection's own writes) can observe the first one's commits.
func dvPair(t *testing.T) (writer, observer *DB) {
	t.Helper()
	path := t.TempDir() + "/data.sql"
	w, err := Open(path)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	o, err := Open(path)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = o.Close() })
	return w, o
}

func TestDB_SetMetaBatchIfChanged(t *testing.T) {
	t.Run("identical values commit nothing", func(t *testing.T) {
		w, o := dvPair(t)
		entries := map[string]string{"state": "none", "url": ""}
		wrote, err := w.SetMetaBatchIfChanged("t1", "pr", entries)
		testutil.NoError(t, err)
		testutil.Equal(t, wrote, true) // first write: row absent

		before, err := ListMetaEntry(t, w, "t1", "pr", "state")
		testutil.NoError(t, err)
		dv1, err := o.DataVersion()
		testutil.NoError(t, err)
		time.Sleep(2 * time.Millisecond)

		wrote, err = w.SetMetaBatchIfChanged("t1", "pr", entries)
		testutil.NoError(t, err)
		testutil.Equal(t, wrote, false)

		dv2, err := o.DataVersion()
		testutil.NoError(t, err)
		testutil.Equal(t, dv2, dv1) // no commit, no data_version bump
		after, err := ListMetaEntry(t, w, "t1", "pr", "state")
		testutil.NoError(t, err)
		testutil.Equal(t, after.UpdatedAt, before.UpdatedAt)
	})

	t.Run("old SetMetaBatch always commits", func(t *testing.T) {
		w, o := dvPair(t)
		entries := map[string]string{"state": "none", "url": ""}
		testutil.NoError(t, w.SetMetaBatch("t1", "pr", entries))
		dv1, _ := o.DataVersion()
		testutil.NoError(t, w.SetMetaBatch("t1", "pr", entries))
		dv2, _ := o.DataVersion()
		if dv2 == dv1 {
			t.Fatal("control: SetMetaBatch of identical values should still bump data_version")
		}
	})

	t.Run("changed state is written", func(t *testing.T) {
		w, _ := dvPair(t)
		_, err := w.SetMetaBatchIfChanged("t1", "pr", map[string]string{"state": "none", "url": ""})
		testutil.NoError(t, err)
		wrote, err := w.SetMetaBatchIfChanged("t1", "pr", map[string]string{"state": "awaiting-review", "url": "https://x/1"})
		testutil.NoError(t, err)
		testutil.Equal(t, wrote, true)
		got, err := w.ListMetaByNamespace("pr")
		testutil.NoError(t, err)
		testutil.Equal(t, got["t1"]["state"], "awaiting-review")
		testutil.Equal(t, got["t1"]["url"], "https://x/1")
	})

	t.Run("changed url alone is written", func(t *testing.T) {
		w, _ := dvPair(t)
		_, _ = w.SetMetaBatchIfChanged("t1", "pr", map[string]string{"state": "draft", "url": "a"})
		wrote, err := w.SetMetaBatchIfChanged("t1", "pr", map[string]string{"state": "draft", "url": "b"})
		testutil.NoError(t, err)
		testutil.Equal(t, wrote, true)
		got, _ := w.ListMetaByNamespace("pr")
		testutil.Equal(t, got["t1"]["url"], "b")
	})

	t.Run("a new key on an existing row is written", func(t *testing.T) {
		w, _ := dvPair(t)
		_, _ = w.SetMetaBatchIfChanged("t1", "pr", map[string]string{"state": "draft"})
		wrote, err := w.SetMetaBatchIfChanged("t1", "pr", map[string]string{"state": "draft", "url": ""})
		testutil.NoError(t, err)
		testutil.Equal(t, wrote, true)
	})

	t.Run("validation matches SetMetaBatch", func(t *testing.T) {
		w, _ := dvPair(t)
		_, err := w.SetMetaBatchIfChanged("", "pr", map[string]string{"a": "b"})
		testutil.ErrorIs(t, err, ErrMetaInvalidKey)
		_, err = w.SetMetaBatchIfChanged("t1", "", map[string]string{"a": "b"})
		testutil.ErrorIs(t, err, ErrMetaInvalidKey)
		_, err = w.SetMetaBatchIfChanged("t1", "pr", map[string]string{"": "b"})
		testutil.ErrorIs(t, err, ErrMetaInvalidKey)
		wrote, err := w.SetMetaBatchIfChanged("t1", "pr", nil)
		testutil.NoError(t, err)
		testutil.Equal(t, wrote, false)
	})
}

// ListMetaEntry returns one (task, ns, key) entry (test helper).
func ListMetaEntry(t *testing.T, d *DB, taskID, ns, key string) (TaskMetaEntry, error) {
	t.Helper()
	entries, err := d.ListMeta(taskID, ns)
	if err != nil {
		return TaskMetaEntry{}, err
	}
	for _, e := range entries {
		if e.Key == key {
			return e, nil
		}
	}
	t.Fatalf("no entry %s/%s/%s", taskID, ns, key)
	return TaskMetaEntry{}, nil
}

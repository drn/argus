package db

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestUpdate_AllBoolFields exercises the Sandboxed / Archived boolean branches
// in Update.
func TestDB_Update_AllBoolFields(t *testing.T) {
	d := testDB(t)
	task := &model.Task{Name: "x"}
	testutil.NoError(t, d.Add(task))

	task.Sandboxed = true
	task.Archived = true
	testutil.NoError(t, d.Update(task))

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Sandboxed, true)
	testutil.Equal(t, got.Archived, true)
}

// TestRenameIfName_RowGoneAfterUpdate covers the row-disappeared error branch.
func TestDB_RenameIfName_RowGone(t *testing.T) {
	d := testDB(t)
	// CAS against an id that doesn't exist; the row-gone branch fires the
	// QueryRow which finds no row and returns the not-found error.
	_, err := d.RenameIfName("ghost-id", "expected", "new")
	testutil.Error(t, err)
}

// TestDB_OrchestrationFields exercises the BaseBranch / DependsOn / Result
// round-trip through Add → Get → Update → Get. DependsOn in particular
// runs through encodeDependsOn JSON serialization and the scan-side
// json.Unmarshal so any drift between writer and reader trips this test.
func TestDB_OrchestrationFields(t *testing.T) {
	d := testDB(t)
	task := &model.Task{
		Name:       "stacked-m2",
		BaseBranch: "argus/m1",
		DependsOn:  []string{"id-a", "id-b"},
		Result:     `{"pr_url":"https://x/pull/1"}`,
	}
	testutil.NoError(t, d.Add(task))

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.BaseBranch, "argus/m1")
	testutil.DeepEqual(t, got.DependsOn, []string{"id-a", "id-b"})
	testutil.Equal(t, got.Result, `{"pr_url":"https://x/pull/1"}`)

	// Updating clears DependsOn (orchestrator transferred control) and
	// rewrites Result.
	got.DependsOn = nil
	got.Result = `{"pr_url":"https://x/pull/2"}`
	testutil.NoError(t, d.Update(got))

	got2, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(got2.DependsOn), 0)
	testutil.Equal(t, got2.Result, `{"pr_url":"https://x/pull/2"}`)
}

// TestDB_SetResult exercises the partial-update path used by task_set_result.
// Concurrent agent status changes must not be clobbered, which is why
// SetResult bypasses the full-row Update.
func TestDB_SetResult(t *testing.T) {
	d := testDB(t)
	task := &model.Task{Name: "t", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(task))

	// Simulate the agent flipping status to complete while the orchestrator
	// is mid-result-write — SetResult should leave Status alone.
	task.Status = model.StatusComplete
	testutil.NoError(t, d.Update(task))

	testutil.NoError(t, d.SetResult(task.ID, `{"ok":true}`))

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.Result, `{"ok":true}`)
	testutil.Equal(t, got.Status, model.StatusComplete)

	// Idempotent: re-set wins.
	testutil.NoError(t, d.SetResult(task.ID, `{"ok":false}`))
	got2, _ := d.Get(task.ID)
	testutil.Equal(t, got2.Result, `{"ok":false}`)

	// Missing row surfaces an error so callers don't silently no-op.
	testutil.Error(t, d.SetResult("does-not-exist", "{}"))
}

// TestDB_FindByNameProject exercises the idempotency lookup. Archived rows
// must not be matched so the same slug can be reused after archive.
func TestDB_FindByNameProject(t *testing.T) {
	d := testDB(t)

	got, err := d.FindByNameProject("missing", "proj")
	testutil.NoError(t, err)
	testutil.Nil(t, got)

	live := &model.Task{Name: "stacked-m1", Project: "proj"}
	testutil.NoError(t, d.Add(live))

	got, err = d.FindByNameProject("stacked-m1", "proj")
	testutil.NoError(t, err)
	if got == nil || got.ID != live.ID {
		t.Fatalf("expected to find live task, got %v", got)
	}

	// Different project — no match.
	got, err = d.FindByNameProject("stacked-m1", "other")
	testutil.NoError(t, err)
	testutil.Nil(t, got)

	// Archived row must be skipped.
	live.Archived = true
	testutil.NoError(t, d.Update(live))
	got, err = d.FindByNameProject("stacked-m1", "proj")
	testutil.NoError(t, err)
	testutil.Nil(t, got)
}

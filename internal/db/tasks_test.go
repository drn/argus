package db

import (
	"errors"
	"sync"
	"testing"

	"github.com/drn/argus/internal/events"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// recordingSink captures events emitted via the global events bus so tests
// can assert on which event types fired. Local helper — the events package
// has its own internal recordingSink, but it isn't exported. Goroutine-safe
// because db.Update emits AFTER unlocking d.mu, on whatever goroutine ran
// the call.
type recordingSink struct {
	mu     sync.Mutex
	events []model.Event
}

func (r *recordingSink) Emit(ev model.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

func (r *recordingSink) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, ev := range r.events {
		out = append(out, ev.Type)
	}
	return out
}

func installRecordingSink(t *testing.T) *recordingSink {
	t.Helper()
	sink := &recordingSink{}
	prev := events.SetSink(sink)
	t.Cleanup(func() { events.SetSink(prev) })
	return sink
}

func sliceContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

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
		PlanSlug:   "thanxai-marketplace-mcp-v1",
	}
	testutil.NoError(t, d.Add(task))

	got, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.BaseBranch, "argus/m1")
	testutil.DeepEqual(t, got.DependsOn, []string{"id-a", "id-b"})
	testutil.Equal(t, got.Result, `{"pr_url":"https://x/pull/1"}`)
	testutil.Equal(t, got.PlanSlug, "thanxai-marketplace-mcp-v1")

	// Updating clears DependsOn (orchestrator transferred control), rewrites
	// Result, and re-stamps PlanSlug.
	got.DependsOn = nil
	got.Result = `{"pr_url":"https://x/pull/2"}`
	got.PlanSlug = "retry-1"
	testutil.NoError(t, d.Update(got))

	got2, err := d.Get(task.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(got2.DependsOn), 0)
	testutil.Equal(t, got2.Result, `{"pr_url":"https://x/pull/2"}`)
	testutil.Equal(t, got2.PlanSlug, "retry-1")
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

// TestDB_SetDependsOn exercises the partial-update path used by orch.Link /
// Unlink. Like SetResult, the column write must not clobber concurrent
// status changes from the agent's task_complete call.
func TestDB_SetDependsOn(t *testing.T) {
	d := testDB(t)
	a := &model.Task{Name: "A"}
	b := &model.Task{Name: "B", DependsOn: []string{"a-id"}}
	testutil.NoError(t, d.Add(a))
	testutil.NoError(t, d.Add(b))

	// Simulate a concurrent status flip — orch.Link's caller may have read
	// the task while the agent was in_progress; the agent then completed.
	b.SetStatus(model.StatusComplete)
	testutil.NoError(t, d.Update(b))

	testutil.NoError(t, d.SetDependsOn(b.ID, []string{a.ID, "extra-id"}))

	got, err := d.Get(b.ID)
	testutil.NoError(t, err)
	testutil.DeepEqual(t, got.DependsOn, []string{a.ID, "extra-id"})
	testutil.Equal(t, got.Status, model.StatusComplete) // not clobbered

	// Empty slice clears the column.
	testutil.NoError(t, d.SetDependsOn(b.ID, nil))
	got2, _ := d.Get(b.ID)
	testutil.Equal(t, len(got2.DependsOn), 0)

	// Missing row surfaces ErrTaskNotFound.
	err = d.SetDependsOn("ghost", nil)
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}

// TestDB_SetPlanSlug — partial update of the orchestrator grouping label.
func TestDB_SetPlanSlug(t *testing.T) {
	d := testDB(t)
	task := &model.Task{Name: "t"}
	testutil.NoError(t, d.Add(task))
	task.SetStatus(model.StatusInReview)
	testutil.NoError(t, d.Update(task))

	testutil.NoError(t, d.SetPlanSlug(task.ID, "my-stack"))
	got, _ := d.Get(task.ID)
	testutil.Equal(t, got.PlanSlug, "my-stack")
	testutil.Equal(t, got.Status, model.StatusInReview) // not clobbered

	testutil.NoError(t, d.SetPlanSlug(task.ID, ""))
	got2, _ := d.Get(task.ID)
	testutil.Equal(t, got2.PlanSlug, "")

	if err := d.SetPlanSlug("ghost", "x"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
}

// TestDB_Update_EmitsArchivedOnTransition pins the regression: when Update
// flips archived from false to true, task.archived MUST fire. Before the fix
// only SetArchived's partial-column path emitted, so HTTP /api/tasks PUT and
// MCP task_archive (both flow through Update) silently archived rows and
// downstream views of "live bindings" went stale. See the matching emission
// in db.Update next to the status-change branch.
func TestDB_Update_EmitsArchivedOnTransition(t *testing.T) {
	t.Run("false_to_true_emits", func(t *testing.T) {
		sink := installRecordingSink(t)
		d := testDB(t)
		task := &model.Task{Name: "archive-me"}
		testutil.NoError(t, d.Add(task))
		testutil.Equal(t, task.Archived, false)

		task.Archived = true
		testutil.NoError(t, d.Update(task))

		if !sliceContains(sink.types(), model.EventTypeTaskArchived) {
			t.Fatalf("expected %q event, got %v", model.EventTypeTaskArchived, sink.types())
		}
	})

	t.Run("already_archived_does_not_reemit", func(t *testing.T) {
		sink := installRecordingSink(t)
		d := testDB(t)
		task := &model.Task{Name: "already"}
		task.Archived = true
		testutil.NoError(t, d.Add(task))

		// Update without flipping archived. Must not fire task.archived again —
		// hera would otherwise see duplicate archive events on every PUT.
		task.Name = "renamed-via-update"
		testutil.NoError(t, d.Update(task))

		for _, ty := range sink.types() {
			if ty == model.EventTypeTaskArchived {
				t.Fatalf("did not expect %q on a no-op archive write, got %v",
					model.EventTypeTaskArchived, sink.types())
			}
		}
	})

	t.Run("unarchive_does_not_emit", func(t *testing.T) {
		sink := installRecordingSink(t)
		d := testDB(t)
		task := &model.Task{Name: "unarchive"}
		task.Archived = true
		testutil.NoError(t, d.Add(task))

		task.Archived = false
		testutil.NoError(t, d.Update(task))

		for _, ty := range sink.types() {
			if ty == model.EventTypeTaskArchived {
				t.Fatalf("unarchive must not emit %q, got %v",
					model.EventTypeTaskArchived, sink.types())
			}
		}
	})
}

// TestDB_SetArchived_StillEmits is a belt-and-braces check that the existing
// SetArchived emission did not regress when Update gained its own emit.
func TestDB_SetArchived_StillEmits(t *testing.T) {
	sink := installRecordingSink(t)
	d := testDB(t)
	task := &model.Task{Name: "x"}
	testutil.NoError(t, d.Add(task))

	testutil.NoError(t, d.SetArchived(task.ID, true))

	if !sliceContains(sink.types(), model.EventTypeTaskArchived) {
		t.Fatalf("SetArchived must still emit %q, got %v",
			model.EventTypeTaskArchived, sink.types())
	}
}

// TestDB_SetArchived covers the partial-update + pinned-clearing invariant
// the halt cascade relies on. Archiving a pinned task MUST yield a clean
// archived row, not a (pinned=1, archived=1) Frankenstein the task list
// would render in both the Pinned and Archive sections.
func TestDB_SetArchived(t *testing.T) {
	d := testDB(t)
	pinned := &model.Task{Name: "pinned"}
	pinned.SetPinned(true)
	testutil.NoError(t, d.Add(pinned))
	testutil.Equal(t, pinned.Pinned, true)

	// Concurrent status flip simulation — must not be clobbered.
	pinned.SetStatus(model.StatusInProgress)
	testutil.NoError(t, d.Update(pinned))

	testutil.NoError(t, d.SetArchived(pinned.ID, true))
	got, _ := d.Get(pinned.ID)
	testutil.Equal(t, got.Archived, true)
	testutil.Equal(t, got.Pinned, false) // mutual exclusivity preserved
	testutil.Equal(t, got.Status, model.StatusInProgress)

	// Unarchive leaves pinned alone — pinning state survives a round trip.
	// To prove the "leaves pinned alone" claim load-bearingly, re-pin the
	// row directly in the DB (without going through SetArchived), then
	// unarchive and verify the pin survived. If SetArchived ever started
	// clearing pinned on the false branch too, this assertion would fail.
	_, err := d.conn.Exec(`UPDATE tasks SET pinned=1 WHERE id=?`, pinned.ID)
	testutil.NoError(t, err)
	testutil.NoError(t, d.SetArchived(pinned.ID, false))
	got2, _ := d.Get(pinned.ID)
	testutil.Equal(t, got2.Archived, false)
	testutil.Equal(t, got2.Pinned, true)

	if err := d.SetArchived("ghost", true); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected ErrTaskNotFound, got %v", err)
	}
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

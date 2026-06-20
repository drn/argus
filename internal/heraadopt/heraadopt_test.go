package heraadopt

import (
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// fakeStore is a minimal in-memory implementation of the trimmed Store surface
// ReconcileBindings needs (Get / ListHeraLiveBindings / EndHeraBinding).
type fakeStore struct {
	tasks    []*model.Task
	bindings []*db.HeraBinding
	getErr   error
	endErr   error
}

func newFakeStore() *fakeStore { return &fakeStore{} }

func (f *fakeStore) addLiveBinding(id int64, taskID string) {
	f.bindings = append(f.bindings, &db.HeraBinding{ID: id, ArgusTaskID: taskID})
}

func (f *fakeStore) Get(id string) (*model.Task, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, t := range f.tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return nil, db.ErrTaskNotFound
}

func (f *fakeStore) ListHeraLiveBindings() ([]*db.HeraBinding, error) {
	out := make([]*db.HeraBinding, 0, len(f.bindings))
	for _, b := range f.bindings {
		if b.EndedAt == nil {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeStore) EndHeraBinding(bindingID int64, _ string) error {
	if f.endErr != nil {
		return f.endErr
	}
	for _, b := range f.bindings {
		if b.ID == bindingID {
			tm := time.Unix(1, 0)
			b.EndedAt = &tm
			return nil
		}
	}
	return nil
}

func (f *fakeStore) liveBindingCountForTask(taskID string) int {
	n := 0
	for _, b := range f.bindings {
		if b.ArgusTaskID == taskID && b.EndedAt == nil {
			n++
		}
	}
	return n
}

func TestReconcileBindings(t *testing.T) {
	t.Run("ends bindings whose task is missing", func(t *testing.T) {
		f := newFakeStore()
		f.addLiveBinding(1, "gone")    // task "gone" does not exist in f.tasks
		f.addLiveBinding(2, "present") // referenced below
		f.tasks = []*model.Task{{ID: "present", Project: "proj"}}

		n, err := ReconcileBindings(f)
		testutil.NoError(t, err)
		testutil.Equal(t, n, 1)
		testutil.Equal(t, f.liveBindingCountForTask("gone"), 0)
		testutil.Equal(t, f.liveBindingCountForTask("present"), 1)
	})

	t.Run("no live bindings is a no-op", func(t *testing.T) {
		f := newFakeStore()
		n, err := ReconcileBindings(f)
		testutil.NoError(t, err)
		testutil.Equal(t, n, 0)
	})

	t.Run("end error is non-fatal and counted as skipped", func(t *testing.T) {
		f := newFakeStore()
		f.addLiveBinding(1, "gone")
		f.endErr = errors.New("write failed")
		n, err := ReconcileBindings(f)
		testutil.NoError(t, err)
		testutil.Equal(t, n, 0) // not counted because End failed
	})

	t.Run("transient get error keeps the binding live", func(t *testing.T) {
		f := newFakeStore()
		f.addLiveBinding(1, "x")
		f.getErr = errors.New("db transient")
		n, err := ReconcileBindings(f)
		testutil.NoError(t, err)
		testutil.Equal(t, n, 0)
		testutil.Equal(t, f.liveBindingCountForTask("x"), 1)
	})
}

// TestReconcileBindings_RealDB catches query-wiring bugs the fake can't: a live
// binding pointing at a deleted task row must be ended on the startup sweep.
func TestReconcileBindings_RealDB(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	o, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	r, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: "w", Kind: db.HeraKindWorker, ArgusProject: "proj"})
	testutil.NoError(t, err)
	// Live binding pointing at a task ID that does not exist in the tasks table.
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: r.ID, ArgusTaskID: "ghost", WorktreePath: "/wt/ghost"})
	testutil.NoError(t, err)

	n, err := ReconcileBindings(d)
	testutil.NoError(t, err)
	testutil.Equal(t, n, 1)

	live, err := d.ListHeraLiveBindings()
	testutil.NoError(t, err)
	testutil.Equal(t, len(live), 0)
}

// TestReconcileBindings_LeavesPlannedRoleIntact pins the add-hera-plan-substrate
// invariant: a planned node is a worker role with NO binding, so the
// binding-keyed reconcile sweep must never see it, end it, or mangle it.
func TestReconcileBindings_LeavesPlannedRoleIntact(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	o, err := d.CreateHeraOrchestrator("orch", "")
	testutil.NoError(t, err)
	planned, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: o.ID, Name: "2c-planned", ArgusProject: "proj", Prompt: "later",
	})
	testutil.NoError(t, err)

	n, err := ReconcileBindings(d)
	testutil.NoError(t, err)
	testutil.Equal(t, n, 0) // no live bindings to reconcile

	// The planned role still exists and is still a planned node.
	got, err := d.HeraRole(planned.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ID, planned.ID)
	nodes, err := d.ListHeraPlannedNodes()
	testutil.NoError(t, err)
	testutil.Equal(t, len(nodes), 1)
}

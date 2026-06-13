package heraadopt

import (
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// ptrTime returns a non-nil *time.Time for marking a row archived/ended in the
// fake. The exact value is irrelevant — the watcher only checks for non-nil.
func ptrTime() *time.Time { tm := time.Unix(1, 0); return &tm }

// fakeStore is an in-memory implementation of Store giving full control over
// every D4 branch and failure injection. The hera_* maps mirror just enough of
// the real DB semantics (live = ended_at nil; orchestrator archive via flag).
type fakeStore struct {
	tasks []*model.Task
	meta  map[string]map[string]string // taskID -> key -> value (namespace "hera")

	roles    map[int64]*db.HeraRole
	orchs    map[int64]*db.HeraOrchestrator
	bindings []*db.HeraBinding

	nextRole    int64
	nextBinding int64

	// failure injection / call recording
	failCreate       bool
	createCalls      int
	endedBindings    []int64
	endErr           error
	tasksErr         error
	metaErr          error
	roleErr          error // HeraRole returns this
	orchErr          error // HeraOrchestrator returns this
	bindingLookupErr error // HeraLiveBindingByTaskAndOrchestrator returns this (non-NotFound)
	uniqueErr        error // UniqueHeraRoleName returns this
	setMetaErr       error // SetMeta returns this
	getErr           error // Get returns this (transient, non-NotFound)
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		meta:  map[string]map[string]string{},
		roles: map[int64]*db.HeraRole{},
		orchs: map[int64]*db.HeraOrchestrator{},
	}
}

func (f *fakeStore) addOrch(id int64, name string, archived bool) {
	o := &db.HeraOrchestrator{ID: id, Name: name}
	if archived {
		o.ArchivedAt = ptrTime()
	}
	f.orchs[id] = o
}

func (f *fakeStore) addRole(id, orchID int64, name string, kind db.HeraRoleKind) {
	f.roles[id] = &db.HeraRole{ID: id, OrchestratorID: orchID, Name: name, Kind: kind}
}

func (f *fakeStore) addLiveBinding(roleID, orchID int64, taskID string) {
	f.nextBinding++
	f.bindings = append(f.bindings, &db.HeraBinding{
		ID: f.nextBinding, RoleID: roleID, OrchestratorID: orchID, ArgusTaskID: taskID,
	})
}

// --- Store impl ---

func (f *fakeStore) Tasks() ([]*model.Task, error) {
	if f.tasksErr != nil {
		return nil, f.tasksErr
	}
	return f.tasks, nil
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

func (f *fakeStore) ListMetaByNamespace(string) (map[string]map[string]string, error) {
	if f.metaErr != nil {
		return nil, f.metaErr
	}
	return f.meta, nil
}

func (f *fakeStore) ListHeraLiveBindingsByTask(taskID string) ([]*db.HeraBinding, error) {
	var out []*db.HeraBinding
	for _, b := range f.bindings {
		if b.ArgusTaskID == taskID && b.EndedAt == nil {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeStore) ListHeraLiveBindings() ([]*db.HeraBinding, error) {
	var out []*db.HeraBinding
	for _, b := range f.bindings {
		if b.EndedAt == nil {
			out = append(out, b)
		}
	}
	return out, nil
}

func (f *fakeStore) HeraRole(id int64) (*db.HeraRole, error) {
	if f.roleErr != nil {
		return nil, f.roleErr
	}
	if r, ok := f.roles[id]; ok {
		return r, nil
	}
	return nil, db.ErrHeraNotFound
}

func (f *fakeStore) HeraOrchestrator(id int64) (*db.HeraOrchestrator, error) {
	if f.orchErr != nil {
		return nil, f.orchErr
	}
	if o, ok := f.orchs[id]; ok {
		return o, nil
	}
	return nil, db.ErrHeraNotFound
}

func (f *fakeStore) HeraLiveBindingByTaskAndOrchestrator(taskID string, orchID int64) (*db.HeraBinding, error) {
	if f.bindingLookupErr != nil {
		return nil, f.bindingLookupErr
	}
	for _, b := range f.bindings {
		if b.ArgusTaskID == taskID && b.OrchestratorID == orchID && b.EndedAt == nil {
			return b, nil
		}
	}
	return nil, db.ErrHeraNotFound
}

func (f *fakeStore) UniqueHeraRoleName(_ int64, base string) (string, error) {
	if f.uniqueErr != nil {
		return "", f.uniqueErr
	}
	if base == "" {
		return "worker", nil
	}
	return base, nil
}

func (f *fakeStore) CreateHeraRoleWithBinding(roleIn db.CreateHeraRoleInput, taskID, worktree string) (*db.HeraRole, *db.HeraBinding, error) {
	f.createCalls++
	if f.failCreate {
		return nil, nil, errors.New("create role+binding failed")
	}
	f.nextRole++
	r := &db.HeraRole{
		ID: f.nextRole, OrchestratorID: roleIn.OrchestratorID, Name: roleIn.Name,
		Kind: roleIn.Kind, ArgusProject: roleIn.ArgusProject, Prompt: roleIn.Prompt,
	}
	f.roles[r.ID] = r
	f.nextBinding++
	b := &db.HeraBinding{
		ID: f.nextBinding, RoleID: r.ID, OrchestratorID: roleIn.OrchestratorID,
		ArgusTaskID: taskID, WorktreePath: worktree,
	}
	f.bindings = append(f.bindings, b)
	return r, b, nil
}

func (f *fakeStore) EndHeraBinding(bindingID int64, _ string) error {
	if f.endErr != nil {
		return f.endErr
	}
	for _, b := range f.bindings {
		if b.ID == bindingID && b.EndedAt == nil {
			b.EndedAt = ptrTime()
			f.endedBindings = append(f.endedBindings, bindingID)
			return nil
		}
	}
	return db.ErrHeraNotFound
}

func (f *fakeStore) SetMeta(string, string, string, string) error { return f.setMetaErr }

// liveBindingCountForTask counts live bindings for taskID — the assertion
// surface for "did adoption happen / get skipped".
func (f *fakeStore) liveBindingCountForTask(taskID string) int {
	n := 0
	for _, b := range f.bindings {
		if b.ArgusTaskID == taskID && b.EndedAt == nil {
			n++
		}
	}
	return n
}

// --- scenario builder: parent coordinator + worker-meta child linked to it ---

func d4Scenario(t *testing.T) *fakeStore {
	t.Helper()
	f := newFakeStore()
	f.addOrch(1, "orch", false)
	f.addRole(10, 1, "coord", db.HeraKindCoordinator)
	f.addLiveBinding(10, 1, "parent")
	f.tasks = []*model.Task{
		{ID: "parent", Name: "parent", Project: "proj", Status: model.StatusInProgress},
		{ID: "child", Name: "child", Project: "proj", Worktree: "/wt/child", DependsOn: []string{"parent"}, Status: model.StatusInProgress},
	}
	f.meta["child"] = map[string]string{db.HeraMetaKeyRole: string(db.HeraKindWorker)}
	return f
}

func TestAutoAdopt_D4Positive(t *testing.T) {
	f := d4Scenario(t)
	f.meta["child"][db.HeraMetaKeyPrompt] = "verbatim worker prompt"

	var adoptedChild string
	w := New(f)
	w.SetOnAdopt(func(childID string, _ *db.HeraRole, _ *db.HeraBinding) { adoptedChild = childID })
	w.Tick()

	testutil.Equal(t, f.liveBindingCountForTask("child"), 1)
	testutil.Equal(t, adoptedChild, "child")
	// Adopted role carries the child's project + the verbatim prompt from meta,
	// kind worker, under the parent's orchestrator.
	var adopted *db.HeraRole
	for _, r := range f.roles {
		if r.Name == "child" {
			adopted = r
		}
	}
	if adopted == nil {
		t.Fatal("adopted role not created")
	}
	testutil.Equal(t, adopted.Kind, db.HeraKindWorker)
	testutil.Equal(t, adopted.OrchestratorID, int64(1))
	testutil.Equal(t, adopted.ArgusProject, "proj")
	testutil.Equal(t, adopted.Prompt, "verbatim worker prompt")
}

func TestAutoAdopt_D4Negatives(t *testing.T) {
	t.Run("two coordinator bindings on parent (ambiguous)", func(t *testing.T) {
		f := d4Scenario(t)
		// Parent also coordinates a second orchestrator.
		f.addOrch(2, "orch2", false)
		f.addRole(20, 2, "coord2", db.HeraKindCoordinator)
		f.addLiveBinding(20, 2, "parent")
		w := New(f)
		w.Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
		testutil.Equal(t, f.createCalls, 0)
	})

	t.Run("child missing meta:hera.role", func(t *testing.T) {
		f := d4Scenario(t)
		delete(f.meta, "child")
		w := New(f)
		w.Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
		testutil.Equal(t, f.createCalls, 0)
	})

	t.Run("child meta role is not worker", func(t *testing.T) {
		f := d4Scenario(t)
		f.meta["child"][db.HeraMetaKeyRole] = string(db.HeraKindCoordinator)
		w := New(f)
		w.Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
	})

	t.Run("archived parent orchestrator", func(t *testing.T) {
		f := d4Scenario(t)
		f.orchs[1].ArchivedAt = ptrTime() // archive the parent's orchestrator
		w := New(f)
		w.Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
	})

	t.Run("parent has no coordinator binding", func(t *testing.T) {
		f := d4Scenario(t)
		f.roles[10].Kind = db.HeraKindWorker // parent is a worker, not a coordinator
		w := New(f)
		w.Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
	})

	t.Run("parent not bound at all", func(t *testing.T) {
		f := d4Scenario(t)
		f.bindings = nil // remove the parent's binding
		w := New(f)
		w.Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
	})

	t.Run("child not linked to parent", func(t *testing.T) {
		f := d4Scenario(t)
		f.tasks[1].DependsOn = nil // unlink
		w := New(f)
		w.Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
	})

	t.Run("archived child is skipped", func(t *testing.T) {
		f := d4Scenario(t)
		f.tasks[1].Archived = true
		w := New(f)
		w.Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
	})
}

func TestAutoAdopt_IdempotentReAdopt(t *testing.T) {
	f := d4Scenario(t)
	w := New(f)
	w.Tick()
	w.Tick()
	w.Tick()
	// Adopted exactly once despite three passes — the live-binding check is the
	// idempotent guard.
	testutil.Equal(t, f.liveBindingCountForTask("child"), 1)
	testutil.Equal(t, f.createCalls, 1)
}

func TestAutoAdopt_HandlerErrorRetriesNextTick(t *testing.T) {
	f := d4Scenario(t)
	f.failCreate = true
	w := New(f)
	w.Tick()
	// Failed create → no binding, but the eligible link is NOT lost (re-derived).
	testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
	testutil.Equal(t, f.createCalls, 1)

	f.failCreate = false
	w.Tick()
	testutil.Equal(t, f.liveBindingCountForTask("child"), 1)
	testutil.Equal(t, f.createCalls, 2)
}

func TestAutoAdopt_NeverTouchesTaskStatus(t *testing.T) {
	f := d4Scenario(t)
	w := New(f)
	w.Tick()
	// The watcher owns binding rows only — task statuses are untouched.
	for _, tk := range f.tasks {
		testutil.Equal(t, tk.Status, model.StatusInProgress)
		testutil.Equal(t, tk.Archived, false)
	}
}

func TestAutoAdopt_AlreadyBornBoundSkips(t *testing.T) {
	// A born-bound spawned worker already has a live binding under the
	// orchestrator before any link exists; auto-adopt must skip it.
	f := d4Scenario(t)
	f.addRole(99, 1, "child", db.HeraKindWorker)
	f.addLiveBinding(99, 1, "child")
	w := New(f)
	w.Tick()
	// Still exactly the one pre-existing binding; no second adoption.
	testutil.Equal(t, f.liveBindingCountForTask("child"), 1)
	testutil.Equal(t, f.createCalls, 0)
}

func TestAutoAdopt_LoadErrorsAreNonFatal(t *testing.T) {
	t.Run("tasks error", func(t *testing.T) {
		f := d4Scenario(t)
		f.tasksErr = errors.New("db down")
		w := New(f)
		w.Tick() // must not panic; no adoption
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
	})
	t.Run("meta error", func(t *testing.T) {
		f := d4Scenario(t)
		f.metaErr = errors.New("db down")
		w := New(f)
		w.Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
	})
}

func TestReconcileBindings(t *testing.T) {
	t.Run("ends bindings whose task is missing", func(t *testing.T) {
		f := newFakeStore()
		f.addOrch(1, "orch", false)
		f.addRole(10, 1, "w", db.HeraKindWorker)
		f.addLiveBinding(10, 1, "gone")    // task "gone" does not exist in f.tasks
		f.addLiveBinding(10, 1, "present") // referenced below
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
		f.addLiveBinding(10, 1, "gone")
		f.endErr = errors.New("write failed")
		n, err := ReconcileBindings(f)
		testutil.NoError(t, err)
		testutil.Equal(t, n, 0) // not counted because End failed
	})
}

// --- real *db.DB integration (catches query-wiring bugs the fake can't) ---

func TestAutoAdopt_RealDB_D4Positive(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	// Parent task bound as coordinator.
	parent := &model.Task{Name: "parent", Project: "proj", Status: model.StatusInProgress}
	testutil.NoError(t, d.Add(parent))
	o, err := d.CreateHeraOrchestrator("orch")
	testutil.NoError(t, err)
	coord, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: o.ID, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "proj"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: coord.ID, ArgusTaskID: parent.ID, WorktreePath: "/wt/p"})
	testutil.NoError(t, err)

	// Child task linked to parent, with meta:hera.role=worker.
	child := &model.Task{Name: "child", Project: "proj", Worktree: "/wt/c", Status: model.StatusInProgress, DependsOn: []string{parent.ID}}
	testutil.NoError(t, d.Add(child))
	testutil.NoError(t, d.SetMeta(child.ID, db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindWorker)))

	New(d).Tick()

	// Child now has a live worker binding under the orchestrator.
	bnd, err := d.HeraLiveBindingByTaskAndOrchestrator(child.ID, o.ID)
	testutil.NoError(t, err)
	role, err := d.HeraRole(bnd.RoleID)
	testutil.NoError(t, err)
	testutil.Equal(t, role.Kind, db.HeraKindWorker)
	testutil.Equal(t, role.ArgusProject, "proj")

	// Idempotent: a second tick creates no second binding.
	New(d).Tick()
	all, err := d.ListHeraLiveBindingsByTask(child.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, len(all), 1)
}

func TestReconcileBindings_RealDB(t *testing.T) {
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })

	o, err := d.CreateHeraOrchestrator("orch")
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

func TestAutoAdopt_MaybeAdoptErrorBranches(t *testing.T) {
	t.Run("parent role lookup error", func(t *testing.T) {
		f := d4Scenario(t)
		f.roleErr = errors.New("db down")
		New(f).Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
	})
	t.Run("parent orchestrator lookup error", func(t *testing.T) {
		f := d4Scenario(t)
		f.orchErr = errors.New("db down")
		New(f).Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
	})
	t.Run("child binding lookup non-notfound error", func(t *testing.T) {
		f := d4Scenario(t)
		f.bindingLookupErr = errors.New("db down")
		New(f).Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
		testutil.Equal(t, f.createCalls, 0)
	})
	t.Run("unique name error", func(t *testing.T) {
		f := d4Scenario(t)
		f.uniqueErr = errors.New("db down")
		New(f).Tick()
		testutil.Equal(t, f.liveBindingCountForTask("child"), 0)
		testutil.Equal(t, f.createCalls, 0)
	})
}

// TestAutoAdopt_StartStopLifecycle exercises the goroutine loop: a short
// interval drives an adoption, the onAdopt callback signals it, and Stop ends
// the loop. Stop is also idempotent.
func TestAutoAdopt_StartStopLifecycle(t *testing.T) {
	f := d4Scenario(t)
	w := New(f)
	w.SetInterval(5 * time.Millisecond)
	adopted := make(chan string, 1)
	w.SetOnAdopt(func(childID string, _ *db.HeraRole, _ *db.HeraBinding) {
		select {
		case adopted <- childID:
		default:
		}
	})

	go w.Start()
	select {
	case got := <-adopted:
		testutil.Equal(t, got, "child")
	case <-time.After(2 * time.Second):
		t.Fatal("watcher never adopted within timeout")
	}
	w.Stop()
	w.Stop() // idempotent — must not panic
}

func TestAutoAdopt_MetaMirrorFailureStillAdopts(t *testing.T) {
	// A failed meta re-stamp must NOT undo the adoption (best-effort soft-fail).
	f := d4Scenario(t)
	f.setMetaErr = errors.New("meta write failed")
	New(f).Tick()
	testutil.Equal(t, f.liveBindingCountForTask("child"), 1)
}

func TestReconcileBindings_TransientGetErrorKeepsBinding(t *testing.T) {
	f := newFakeStore()
	f.addLiveBinding(10, 1, "maybe")
	f.getErr = errors.New("db transient")
	n, err := ReconcileBindings(f)
	testutil.NoError(t, err)
	testutil.Equal(t, n, 0) // transient error → binding left live for a later sweep
	testutil.Equal(t, f.liveBindingCountForTask("maybe"), 1)
}

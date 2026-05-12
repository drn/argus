package orch

import (
	"errors"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// fakeStore is an in-memory Store for unit tests. The hot path through orch
// is small enough that recreating db.DB's full surface here is cheaper than
// importing the SQLite-backed implementation just for tests.
type fakeStore struct {
	rows []*model.Task
}

func (f *fakeStore) Tasks() ([]*model.Task, error) {
	// Return deep copies so a caller's mutation of Task.DependsOn does not
	// alias the store's slice — matches the *db.DB contract where Tasks()
	// scans fresh objects per row.
	out := make([]*model.Task, len(f.rows))
	for i, t := range f.rows {
		c := *t
		c.DependsOn = append([]string(nil), t.DependsOn...)
		out[i] = &c
	}
	return out, nil
}

func (f *fakeStore) Get(id string) (*model.Task, error) {
	for _, t := range f.rows {
		if t.ID == id {
			c := *t
			c.DependsOn = append([]string(nil), t.DependsOn...)
			return &c, nil
		}
	}
	return nil, errors.New("task not found: " + id)
}

func (f *fakeStore) Update(t *model.Task) error {
	for i, row := range f.rows {
		if row.ID == t.ID {
			c := *t
			c.DependsOn = append([]string(nil), t.DependsOn...)
			f.rows[i] = &c
			return nil
		}
	}
	return errors.New("task not found: " + t.ID)
}

func (f *fakeStore) SetDependsOn(id string, deps []string) error {
	for _, t := range f.rows {
		if t.ID == id {
			t.DependsOn = append([]string(nil), deps...)
			return nil
		}
	}
	return errors.New("task not found: " + id)
}

func (f *fakeStore) SetPlanSlug(id, slug string) error {
	for _, t := range f.rows {
		if t.ID == id {
			t.PlanSlug = slug
			return nil
		}
	}
	return errors.New("task not found: " + id)
}

func (f *fakeStore) SetArchived(id string, archived bool) error {
	for _, t := range f.rows {
		if t.ID == id {
			t.Archived = archived
			// Mirror *db.DB.SetArchived: archiving clears pinned to
			// uphold the model's mutual-exclusivity invariant. A test
			// that drifts from this would be reasoning about state the
			// DB cannot produce.
			if archived {
				t.Pinned = false
			}
			return nil
		}
	}
	return errors.New("task not found: " + id)
}

func newStore(tasks ...*model.Task) *fakeStore {
	return &fakeStore{rows: append([]*model.Task(nil), tasks...)}
}

// recordingStopper captures Stop calls without doing any work, so HaltDownstream
// can be asserted against the exact set of IDs the runner would have been
// asked to abort.
type recordingStopper struct {
	stopped []string
}

func (s *recordingStopper) Stop(id string) error {
	s.stopped = append(s.stopped, id)
	return nil
}

func TestFindCycle(t *testing.T) {
	cases := []struct {
		name      string
		tasks     []*model.Task
		start     string
		wantCycle []string
	}{
		{
			name: "linear chain (no cycle)",
			tasks: []*model.Task{
				{ID: "A"},
				{ID: "B", DependsOn: []string{"A"}},
				{ID: "C", DependsOn: []string{"B"}},
			},
			start: "C",
		},
		{
			name: "two-node cycle",
			tasks: []*model.Task{
				{ID: "A", DependsOn: []string{"B"}},
				{ID: "B", DependsOn: []string{"A"}},
			},
			start:     "A",
			wantCycle: []string{"A", "B", "A"},
		},
		{
			name: "three-node cycle",
			tasks: []*model.Task{
				{ID: "A", DependsOn: []string{"B"}},
				{ID: "B", DependsOn: []string{"C"}},
				{ID: "C", DependsOn: []string{"A"}},
			},
			start:     "A",
			wantCycle: []string{"A", "B", "C", "A"},
		},
		{
			name: "diamond (no cycle)",
			tasks: []*model.Task{
				{ID: "A"},
				{ID: "B", DependsOn: []string{"A"}},
				{ID: "C", DependsOn: []string{"A"}},
				{ID: "D", DependsOn: []string{"B", "C"}},
			},
			start: "D",
		},
		{
			name:  "missing parent treated as terminal",
			tasks: []*model.Task{{ID: "A", DependsOn: []string{"ghost"}}},
			start: "A",
		},
		{
			name: "deep cycle six levels",
			tasks: []*model.Task{
				{ID: "A", DependsOn: []string{"B"}},
				{ID: "B", DependsOn: []string{"C"}},
				{ID: "C", DependsOn: []string{"D"}},
				{ID: "D", DependsOn: []string{"E"}},
				{ID: "E", DependsOn: []string{"F"}},
				{ID: "F", DependsOn: []string{"A"}},
			},
			start:     "A",
			wantCycle: []string{"A", "B", "C", "D", "E", "F", "A"},
		},
		{
			name: "cycle in disconnected component does not surface from clean start",
			tasks: []*model.Task{
				{ID: "A"},
				{ID: "B", DependsOn: []string{"A"}},
				{ID: "X", DependsOn: []string{"Y"}},
				{ID: "Y", DependsOn: []string{"X"}},
			},
			start: "B",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FindCycle(tc.tasks, tc.start)
			if tc.wantCycle == nil {
				testutil.Equal(t, len(got), 0)
				return
			}
			testutil.DeepEqual(t, got, tc.wantCycle)
		})
	}
}

func TestLink_Happy(t *testing.T) {
	s := newStore(&model.Task{ID: "p"}, &model.Task{ID: "c"})
	testutil.NoError(t, Link(s, "c", "p"))
	got, _ := s.Get("c")
	testutil.DeepEqual(t, got.DependsOn, []string{"p"})
}

func TestLink_Idempotent(t *testing.T) {
	s := newStore(&model.Task{ID: "p"}, &model.Task{ID: "c", DependsOn: []string{"p"}})
	testutil.NoError(t, Link(s, "c", "p"))
	got, _ := s.Get("c")
	testutil.DeepEqual(t, got.DependsOn, []string{"p"})
}

func TestLink_RejectsCycle_WithPath(t *testing.T) {
	s := newStore(
		&model.Task{ID: "A", DependsOn: []string{"B"}},
		&model.Task{ID: "B"},
	)
	err := Link(s, "B", "A")
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("expected CycleError, got %v", err)
	}
	if len(ce.Path) == 0 {
		t.Fatal("CycleError.Path must be populated for the UI")
	}
	// Original child untouched.
	got, _ := s.Get("B")
	testutil.Equal(t, len(got.DependsOn), 0)
}

func TestLink_SelfLoop(t *testing.T) {
	s := newStore(&model.Task{ID: "A"})
	err := Link(s, "A", "A")
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("self-loop should return CycleError, got %v", err)
	}
	testutil.DeepEqual(t, ce.Path, []string{"A", "A"})
}

func TestLink_EmptyIDs(t *testing.T) {
	s := newStore()
	if err := Link(s, "", "x"); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("expected ErrEmptyID, got %v", err)
	}
}

func TestLink_MissingNodes(t *testing.T) {
	s := newStore(&model.Task{ID: "A"})
	if err := Link(s, "ghost", "A"); err == nil {
		t.Fatal("expected error for missing child")
	}
}

func TestUnlink_Happy(t *testing.T) {
	s := newStore(&model.Task{ID: "p"}, &model.Task{ID: "c", DependsOn: []string{"p"}})
	testutil.NoError(t, Unlink(s, "c", "p"))
	got, _ := s.Get("c")
	testutil.Equal(t, len(got.DependsOn), 0)
}

func TestUnlink_Noop(t *testing.T) {
	s := newStore(&model.Task{ID: "p"}, &model.Task{ID: "c"})
	testutil.NoError(t, Unlink(s, "c", "p"))
}

func TestDeps_Neighbors(t *testing.T) {
	s := newStore(
		&model.Task{ID: "A"},
		&model.Task{ID: "B", DependsOn: []string{"A"}},
		&model.Task{ID: "C", DependsOn: []string{"A"}},
	)
	view, err := Deps(s, "A")
	testutil.NoError(t, err)
	testutil.Equal(t, len(view.Upstream), 0)
	testutil.Equal(t, len(view.Downstream), 2)

	view2, _ := Deps(s, "B")
	testutil.DeepEqual(t, view2.Upstream, []string{"A"})
	testutil.Equal(t, len(view2.Downstream), 0)
}

func TestListDAG_Filters(t *testing.T) {
	a := &model.Task{ID: "a", Project: "p1", PlanSlug: "s1", Name: "A"}
	b := &model.Task{ID: "b", Project: "p1", PlanSlug: "s1", Name: "B", DependsOn: []string{"a"}}
	c := &model.Task{ID: "c", Project: "p2", PlanSlug: "s2", Name: "C"}
	d := &model.Task{ID: "d", Project: "p1", PlanSlug: "s1", Name: "D", Archived: true}
	s := newStore(a, b, c, d)

	got, err := ListDAG(s, DAGFilter{})
	testutil.NoError(t, err)
	testutil.Equal(t, len(got), 3) // archived excluded

	got2, _ := ListDAG(s, DAGFilter{IncludeArchived: true})
	testutil.Equal(t, len(got2), 4)

	got3, _ := ListDAG(s, DAGFilter{Project: "p2"})
	testutil.Equal(t, len(got3), 1)
	testutil.Equal(t, got3[0].ID, "c")
}

// TestHaltDownstream_InReviewArchivedNotStopped covers the bug surfaced in
// the rereview: an in_review descendant has no live session, so the previous
// `default:` branch called stopper.Stop (no-op) and falsely added the row to
// report.Stopped. The fix folds in_review into the archive bucket.
func TestHaltDownstream_InReviewArchivedNotStopped(t *testing.T) {
	a := &model.Task{ID: "a", Status: model.StatusInProgress}
	b := &model.Task{ID: "b", Status: model.StatusInReview, DependsOn: []string{"a"}}
	s := newStore(a, b)
	stopper := &recordingStopper{}

	report, err := HaltDownstream(s, stopper, "a", nil)
	testutil.NoError(t, err)
	testutil.Equal(t, len(report.Stopped), 0)
	testutil.DeepEqual(t, report.Archived, []string{"b"})
	if len(stopper.stopped) != 0 {
		t.Fatalf("stopper.Stop should not fire on in_review rows; got %v", stopper.stopped)
	}

	got, _ := s.Get("b")
	testutil.True(t, got.Archived)
}

// TestHaltDownstream_MixedStatuses verifies the per-status routing:
// complete → skipped, pending → archived, in_progress → stopped.
func TestHaltDownstream_MixedStatuses(t *testing.T) {
	root := &model.Task{ID: "root", Status: model.StatusInProgress}
	p := &model.Task{ID: "p", Status: model.StatusPending, DependsOn: []string{"root"}}
	r := &model.Task{ID: "r", Status: model.StatusInProgress, DependsOn: []string{"p"}}
	c := &model.Task{ID: "c", Status: model.StatusComplete, DependsOn: []string{"root"}}
	s := newStore(root, p, r, c)
	stopper := &recordingStopper{}

	report, err := HaltDownstream(s, stopper, "root", nil)
	testutil.NoError(t, err)
	// Seed is NOT halted.
	gotRoot, _ := s.Get("root")
	testutil.False(t, gotRoot.Archived)
	testutil.Equal(t, gotRoot.Status, model.StatusInProgress)

	// p was pending → archived.
	gotP, _ := s.Get("p")
	testutil.True(t, gotP.Archived)
	// r was in_progress → stopped (no archive flip).
	testutil.DeepEqual(t, stopper.stopped, []string{"r"})
	testutil.Contains(t, joinIDs(report.Stopped), "r")
	testutil.Contains(t, joinIDs(report.Archived), "p")
}

// TestHaltDownstream_SessionAlreadyExited covers the race where a session
// exits between the snapshot and Stop — Stopper.Stop returns the predicate-
// matched error, and the row MUST NOT be added to report.Stopped (it would
// inflate the "halted N tasks" summary with sessions that exited on their
// own). Mirrors the depswatcher race documented in orchestration.md.
func TestHaltDownstream_SessionAlreadyExited(t *testing.T) {
	notFound := errors.New("session not found")
	stopper := &fakeStopperWithErr{err: notFound}
	a := &model.Task{ID: "a", Status: model.StatusInProgress}
	b := &model.Task{ID: "b", Status: model.StatusInProgress, DependsOn: []string{"a"}}
	s := newStore(a, b)

	report, err := HaltDownstream(s, stopper, "a", func(e error) bool {
		return errors.Is(e, notFound)
	})
	testutil.NoError(t, err)
	// Stop was called, but matched the notFound predicate → b is NOT in
	// report.Stopped. The actual halt outcome is "session was already gone."
	testutil.Equal(t, len(report.Stopped), 0)
}

// fakeStopperWithErr returns the configured error from every Stop call.
type fakeStopperWithErr struct{ err error }

func (s *fakeStopperWithErr) Stop(_ string) error { return s.err }

func TestSetPlanSlug_WriteAndClear(t *testing.T) {
	t1 := &model.Task{ID: "t1"}
	s := newStore(t1)
	testutil.NoError(t, SetPlanSlug(s, "t1", "my-stack"))
	got, _ := s.Get("t1")
	testutil.Equal(t, got.PlanSlug, "my-stack")

	testutil.NoError(t, SetPlanSlug(s, "t1", ""))
	got2, _ := s.Get("t1")
	testutil.Equal(t, got2.PlanSlug, "")
}

func TestSetPlanSlug_MissingTask(t *testing.T) {
	s := newStore()
	if err := SetPlanSlug(s, "ghost", "slug"); err == nil {
		t.Fatal("expected error for missing task")
	}
}

// joinIDs is a tiny helper so testutil.Contains can be used on string slices
// without pulling strings.Join into every assertion.
func joinIDs(ids []string) string {
	out := ""
	for i, s := range ids {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

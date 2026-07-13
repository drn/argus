package hera

import (
	"errors"
	"fmt"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

// fakeMutStore is a minimal in-memory MutationStore for unit-testing the
// mutation helpers without spinning up SQLite.
type fakeMutStore struct {
	names      map[int64]map[string]bool // orchID -> name -> exists
	planned    []*db.HeraRole
	nextID     int64
	blocks     map[[2]int64]bool
	hasBinding map[int64]bool
	updated    map[int64][2]string // roleID -> [prompt, project]
	cancelled  map[int64]bool
	planErr    error
	uniqueErr  error
	createErr  error
}

func newFakeMutStore() *fakeMutStore {
	return &fakeMutStore{
		names:      map[int64]map[string]bool{},
		blocks:     map[[2]int64]bool{},
		hasBinding: map[int64]bool{},
		updated:    map[int64][2]string{},
		cancelled:  map[int64]bool{},
	}
}

func (f *fakeMutStore) UniqueHeraRoleName(orchID int64, base string) (string, error) {
	if f.uniqueErr != nil {
		return "", f.uniqueErr
	}
	if f.names[orchID] == nil {
		f.names[orchID] = map[string]bool{}
	}
	name := base
	for i := 2; f.names[orchID][name]; i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	f.names[orchID][name] = true
	return name, nil
}

func (f *fakeMutStore) CreateHeraPlannedRole(in db.CreateHeraRoleInput) (*db.HeraRole, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.nextID++
	r := &db.HeraRole{
		ID: f.nextID, OrchestratorID: in.OrchestratorID, Name: in.Name,
		Kind: db.HeraKindWorker, NodeKind: in.NodeKind, ArgusProject: in.ArgusProject, Prompt: in.Prompt,
	}
	f.planned = append(f.planned, r)
	return r, nil
}

func (f *fakeMutStore) AddHeraBlock(blockedRoleID, blockerRoleID int64) error {
	f.blocks[[2]int64{blockedRoleID, blockerRoleID}] = true
	return nil
}

func (f *fakeMutStore) CreateHeraPlan(orchID int64, nodes []db.HeraPlannedNodeSpec, edges []db.HeraBlockSpec) ([]*db.HeraRole, error) {
	if f.planErr != nil {
		return nil, f.planErr
	}
	created := make([]*db.HeraRole, 0, len(nodes))
	for _, n := range nodes {
		role, err := f.CreateHeraPlannedRole(db.CreateHeraRoleInput{OrchestratorID: orchID, Name: n.Name, NodeKind: n.NodeKind, ArgusProject: n.ArgusProject, Prompt: n.Prompt})
		if err != nil {
			return nil, err
		}
		created = append(created, role)
	}
	resolve := func(idx int, roleID int64) int64 {
		if idx >= 0 {
			return created[idx].ID
		}
		return roleID
	}
	for _, e := range edges {
		blocked := resolve(e.BlockedNodeIdx, e.BlockedRoleID)
		blocker := resolve(e.BlockerNodeIdx, e.BlockerRoleID)
		if err := f.AddHeraBlock(blocked, blocker); err != nil {
			return nil, err
		}
	}
	return created, nil
}

func (f *fakeMutStore) HeraRoleHasBinding(roleID int64) (bool, error) {
	return f.hasBinding[roleID], nil
}

func (f *fakeMutStore) UpdateHeraPlannedNode(roleID int64, prompt, project string) error {
	f.updated[roleID] = [2]string{prompt, project}
	return nil
}

func (f *fakeMutStore) CancelHeraPlannedNode(roleID int64) error {
	f.cancelled[roleID] = true
	return nil
}

func (f *fakeMutStore) RemoveHeraBlock(blockedRoleID, blockerRoleID int64) error {
	delete(f.blocks, [2]int64{blockedRoleID, blockerRoleID})
	return nil
}

func TestResolveProject(t *testing.T) {
	t.Run("explicit wins", func(t *testing.T) {
		p, err := ResolveProject("explicit", "coord-project")
		testutil.NoError(t, err)
		testutil.Equal(t, p, "explicit")
	})
	t.Run("falls back to coordinator project", func(t *testing.T) {
		p, err := ResolveProject("", "coord-project")
		testutil.NoError(t, err)
		testutil.Equal(t, p, "coord-project")
	})
	t.Run("both blank is an error", func(t *testing.T) {
		_, err := ResolveProject("  ", "  ")
		testutil.ErrorIs(t, err, ErrNoProject)
	})
}

func TestResolvePlanNodeKind(t *testing.T) {
	t.Run("defaults to worker, requires prompt", func(t *testing.T) {
		kind, prompt, err := ResolvePlanNodeKind("", "do the thing", "")
		testutil.NoError(t, err)
		testutil.Equal(t, kind, db.HeraNodeKindWorker)
		testutil.Equal(t, prompt, "do the thing")
	})
	t.Run("worker with blank prompt errors", func(t *testing.T) {
		_, _, err := ResolvePlanNodeKind("worker", "  ", "")
		testutil.ErrorIs(t, err, ErrPromptRequired)
	})
	t.Run("subcoord uses goal as prompt", func(t *testing.T) {
		kind, prompt, err := ResolvePlanNodeKind("subcoord", "", "run the sub-team")
		testutil.NoError(t, err)
		testutil.Equal(t, kind, db.HeraNodeKindSubCoord)
		testutil.Equal(t, prompt, "run the sub-team")
	})
	t.Run("subcoord with blank goal errors", func(t *testing.T) {
		_, _, err := ResolvePlanNodeKind("subcoord", "", "  ")
		testutil.ErrorIs(t, err, ErrGoalRequired)
	})
}

func TestSpawnWorker(t *testing.T) {
	t.Run("happy path derives name and resolves project", func(t *testing.T) {
		var captured SpawnInput
		spawn := func(in SpawnInput) (*SpawnResult, error) {
			captured = in
			return &SpawnResult{Role: &db.HeraRole{Name: in.BaseName}}, nil
		}
		res, project, err := SpawnWorker(spawn, SpawnWorkerParams{
			OrchID: 1, OrchName: "myorch", CoordinatorName: "coord",
			CoordinatorProject: "coord-project", Prompt: "Implement the parser",
		})
		testutil.NoError(t, err)
		testutil.Equal(t, project, "coord-project")
		testutil.Equal(t, res.Role.Name, captured.BaseName)
		testutil.Contains(t, captured.TaskPrompt, "\n\n---\n\nImplement the parser")
		testutil.Equal(t, captured.RolePrompt, "Implement the parser")
		testutil.Equal(t, captured.Project, "coord-project")
	})

	t.Run("explicit role name skips derivation", func(t *testing.T) {
		var captured SpawnInput
		spawn := func(in SpawnInput) (*SpawnResult, error) {
			captured = in
			return &SpawnResult{}, nil
		}
		_, _, err := SpawnWorker(spawn, SpawnWorkerParams{
			OrchID: 1, OrchName: "o", CoordinatorName: "c", CoordinatorProject: "p",
			Prompt: "x", RoleName: "explicit-name",
		})
		testutil.NoError(t, err)
		testutil.Equal(t, captured.BaseName, "explicit-name")
	})

	t.Run("blank prompt rejected", func(t *testing.T) {
		_, _, err := SpawnWorker(func(SpawnInput) (*SpawnResult, error) { return nil, nil }, SpawnWorkerParams{Prompt: "   "})
		testutil.ErrorIs(t, err, ErrPromptRequired)
	})

	t.Run("no project resolved", func(t *testing.T) {
		_, _, err := SpawnWorker(func(SpawnInput) (*SpawnResult, error) { return &SpawnResult{}, nil }, SpawnWorkerParams{Prompt: "x"})
		testutil.ErrorIs(t, err, ErrNoProject)
	})

	t.Run("spawner error propagates", func(t *testing.T) {
		_, _, err := SpawnWorker(func(SpawnInput) (*SpawnResult, error) {
			return nil, errors.New("worktree creation failed")
		}, SpawnWorkerParams{Prompt: "x", CoordinatorProject: "p"})
		if err == nil || err.Error() != "worktree creation failed" {
			t.Fatalf("expected raw spawner error, got %v", err)
		}
	})
}

func TestSpawnWorker_NilSpawnerRejectedProperly(t *testing.T) {
	_, _, err := SpawnWorker(nil, SpawnWorkerParams{Prompt: "x", CoordinatorProject: "p"})
	if err == nil {
		t.Fatal("expected error for nil spawner")
	}
}

func TestCreatePlanNode(t *testing.T) {
	store := newFakeMutStore()
	t.Run("creates with defaulted project and unique name", func(t *testing.T) {
		role, err := CreatePlanNode(store, 1, "coord-project", "worker-a", db.HeraNodeKindWorker, "do work", "")
		testutil.NoError(t, err)
		testutil.Equal(t, role.Name, "worker-a")
		testutil.Equal(t, role.ArgusProject, "coord-project")
	})
	t.Run("uniquifies a duplicate name", func(t *testing.T) {
		role, err := CreatePlanNode(store, 1, "coord-project", "worker-a", db.HeraNodeKindWorker, "more work", "")
		testutil.NoError(t, err)
		testutil.Equal(t, role.Name, "worker-a-2")
	})
	t.Run("no project resolved", func(t *testing.T) {
		_, err := CreatePlanNode(store, 1, "", "worker-b", db.HeraNodeKindWorker, "work", "")
		testutil.ErrorIs(t, err, ErrNoProject)
	})
}

func TestCreatePlan_WholeGraph(t *testing.T) {
	store := newFakeMutStore()
	resolveExisting := func(name string) (*db.HeraRole, error) {
		return nil, fmt.Errorf("role %q not found", name)
	}
	created, err := CreatePlan(store, 1, "proj", []PlanNodeSpec{
		{Name: "1a", Prompt: "stage one a"},
		{Name: "1b", Prompt: "stage one b"},
		{Name: "2a", Prompt: "stage two a"},
	}, []PlanEdgeSpec{
		{Blocked: "2a", Blocker: "1a"},
		{Blocked: "2a", Blocker: "1b"},
	}, resolveExisting)
	testutil.NoError(t, err)
	testutil.Equal(t, len(created), 3)
	testutil.Equal(t, len(store.blocks), 2)
}

func TestCreatePlan_UnknownEdgeName(t *testing.T) {
	store := newFakeMutStore()
	resolveExisting := func(name string) (*db.HeraRole, error) {
		return nil, fmt.Errorf("role %q not found in this plan or orchestrator", name)
	}
	_, err := CreatePlan(store, 1, "proj", []PlanNodeSpec{
		{Name: "a", Prompt: "p"},
	}, []PlanEdgeSpec{
		{Blocked: "a", Blocker: "nonexistent"},
	}, resolveExisting)
	if err == nil {
		t.Fatal("expected error for unresolvable edge endpoint")
	}
	testutil.Contains(t, err.Error(), "edges[0].blocker")
}

func TestCreatePlan_SubcoordRequiresGoal(t *testing.T) {
	store := newFakeMutStore()
	_, err := CreatePlan(store, 1, "proj", []PlanNodeSpec{
		{Name: "a", Kind: "subcoord"},
	}, nil, nil)
	testutil.Contains(t, err.Error(), "subcoord node requires a goal")
}

func TestUpdatePlanNode(t *testing.T) {
	store := newFakeMutStore()
	t.Run("empty update rejected", func(t *testing.T) {
		err := UpdatePlanNode(store, 1, "  ", "  ")
		testutil.ErrorIs(t, err, ErrEmptyPlanUpdate)
	})
	t.Run("materialized role rejected", func(t *testing.T) {
		store.hasBinding[2] = true
		err := UpdatePlanNode(store, 2, "new prompt", "")
		testutil.ErrorIs(t, err, ErrAlreadyMaterialized)
	})
	t.Run("updates a planned role", func(t *testing.T) {
		err := UpdatePlanNode(store, 3, "new prompt", "new-project")
		testutil.NoError(t, err)
		testutil.Equal(t, store.updated[3], [2]string{"new prompt", "new-project"})
	})
}

func TestCancelPlanNode(t *testing.T) {
	store := newFakeMutStore()
	t.Run("materialized role rejected", func(t *testing.T) {
		store.hasBinding[1] = true
		err := CancelPlanNode(store, 1)
		testutil.ErrorIs(t, err, ErrAlreadyMaterialized)
	})
	t.Run("cancels a planned role", func(t *testing.T) {
		err := CancelPlanNode(store, 2)
		testutil.NoError(t, err)
		if !store.cancelled[2] {
			t.Fatal("expected role 2 to be cancelled")
		}
	})
}

func TestAddRemoveBlock(t *testing.T) {
	store := newFakeMutStore()
	testutil.NoError(t, AddBlock(store, 10, 20))
	if !store.blocks[[2]int64{10, 20}] {
		t.Fatal("expected block edge to be added")
	}
	testutil.NoError(t, RemoveBlock(store, 10, 20))
	if store.blocks[[2]int64{10, 20}] {
		t.Fatal("expected block edge to be removed")
	}
}

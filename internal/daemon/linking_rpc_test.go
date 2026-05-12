package daemon

import (
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// addTask is a tiny helper that inserts a task with a known ID and minimal
// fields. The linking tests don't exercise worktree/agent lifecycle so
// HeadlessCreateTask would be overkill.
func addTask(t *testing.T, d *Daemon, id string, deps []string) *model.Task {
	t.Helper()
	task := &model.Task{
		ID:        id,
		Name:      id,
		Project:   "proj",
		Status:    model.StatusPending,
		DependsOn: append([]string(nil), deps...),
	}
	testutil.NoError(t, d.db.Add(task))
	return task
}

// TestRPC_LinkTasks_Happy adds a fresh edge between two existing tasks.
func TestRPC_LinkTasks_Happy(t *testing.T) {
	d, _ := testDaemon(t)
	addTask(t, d, "A", nil)
	addTask(t, d, "B", nil)

	svc := &RPCService{daemon: d}
	var resp LinkTasksResp
	testutil.NoError(t, svc.LinkTasks(&LinkTasksReq{ChildID: "B", ParentID: "A"}, &resp))
	testutil.True(t, resp.OK)
	testutil.Equal(t, len(resp.Cycle), 0)

	got, _ := d.db.Get("B")
	testutil.DeepEqual(t, got.DependsOn, []string{"A"})
}

// TestRPC_LinkTasks_Idempotent verifies re-linking an existing edge is a
// no-op rather than duplicating the parent ID.
func TestRPC_LinkTasks_Idempotent(t *testing.T) {
	d, _ := testDaemon(t)
	addTask(t, d, "A", nil)
	addTask(t, d, "B", []string{"A"})

	svc := &RPCService{daemon: d}
	var resp LinkTasksResp
	testutil.NoError(t, svc.LinkTasks(&LinkTasksReq{ChildID: "B", ParentID: "A"}, &resp))
	testutil.True(t, resp.OK)

	got, _ := d.db.Get("B")
	testutil.DeepEqual(t, got.DependsOn, []string{"A"})
}

// TestRPC_LinkTasks_RejectsSelfLoop rejects A → A without consulting the
// DFS — the path is fabricated for the UI.
func TestRPC_LinkTasks_RejectsSelfLoop(t *testing.T) {
	d, _ := testDaemon(t)
	addTask(t, d, "A", nil)

	svc := &RPCService{daemon: d}
	var resp LinkTasksResp
	testutil.NoError(t, svc.LinkTasks(&LinkTasksReq{ChildID: "A", ParentID: "A"}, &resp))
	testutil.False(t, resp.OK)
	testutil.DeepEqual(t, resp.Cycle, []string{"A", "A"})
}

// TestRPC_LinkTasks_RejectsCycle covers the DFS-discovered cycle case.
// A already depends on B; linking B onto A would close the cycle.
func TestRPC_LinkTasks_RejectsCycle(t *testing.T) {
	d, _ := testDaemon(t)
	addTask(t, d, "A", []string{"B"})
	addTask(t, d, "B", nil)

	svc := &RPCService{daemon: d}
	var resp LinkTasksResp
	testutil.NoError(t, svc.LinkTasks(&LinkTasksReq{ChildID: "B", ParentID: "A"}, &resp))
	testutil.False(t, resp.OK)
	if len(resp.Cycle) == 0 {
		t.Fatal("expected non-empty cycle path")
	}
	// B's deps must NOT have been mutated.
	got, _ := d.db.Get("B")
	testutil.Equal(t, len(got.DependsOn), 0)
}

// TestRPC_LinkTasks_MissingNode surfaces the error from db.Get.
func TestRPC_LinkTasks_MissingNode(t *testing.T) {
	d, _ := testDaemon(t)
	addTask(t, d, "A", nil)

	svc := &RPCService{daemon: d}
	var resp LinkTasksResp
	testutil.NoError(t, svc.LinkTasks(&LinkTasksReq{ChildID: "ghost", ParentID: "A"}, &resp))
	testutil.False(t, resp.OK)
	testutil.NotEqual(t, resp.Error, "")
}

// TestRPC_LinkTasks_RequiresIDs short-circuits the empty-ID case so the UI
// gets a usable error instead of a confusing db-not-found.
func TestRPC_LinkTasks_RequiresIDs(t *testing.T) {
	d, _ := testDaemon(t)
	svc := &RPCService{daemon: d}
	var resp LinkTasksResp
	testutil.NoError(t, svc.LinkTasks(&LinkTasksReq{}, &resp))
	testutil.False(t, resp.OK)
	testutil.NotEqual(t, resp.Error, "")
}

// TestRPC_UnlinkTasks_Happy removes an existing edge.
func TestRPC_UnlinkTasks_Happy(t *testing.T) {
	d, _ := testDaemon(t)
	addTask(t, d, "A", nil)
	addTask(t, d, "B", []string{"A"})

	svc := &RPCService{daemon: d}
	var resp LinkTasksResp
	testutil.NoError(t, svc.UnlinkTasks(&UnlinkTasksReq{ChildID: "B", ParentID: "A"}, &resp))
	testutil.True(t, resp.OK)

	got, _ := d.db.Get("B")
	testutil.Equal(t, len(got.DependsOn), 0)
}

// TestRPC_UnlinkTasks_Noop tolerates a missing edge — important because the
// UI may double-tap and the orchestrator may retry.
func TestRPC_UnlinkTasks_Noop(t *testing.T) {
	d, _ := testDaemon(t)
	addTask(t, d, "A", nil)
	addTask(t, d, "B", nil)

	svc := &RPCService{daemon: d}
	var resp LinkTasksResp
	testutil.NoError(t, svc.UnlinkTasks(&UnlinkTasksReq{ChildID: "B", ParentID: "A"}, &resp))
	testutil.True(t, resp.OK)
}

// TestRPC_GetDeps returns one-hop neighbours in both directions.
func TestRPC_GetDeps(t *testing.T) {
	d, _ := testDaemon(t)
	addTask(t, d, "A", nil)
	addTask(t, d, "B", []string{"A"})
	addTask(t, d, "C", []string{"A"})
	addTask(t, d, "D", []string{"B"})

	svc := &RPCService{daemon: d}
	var resp DepsResp
	testutil.NoError(t, svc.GetDeps(&DepsReq{TaskID: "A"}, &resp))
	testutil.Equal(t, len(resp.Upstream), 0)
	testutil.Equal(t, len(resp.Downstream), 2) // B and C depend on A

	var resp2 DepsResp
	testutil.NoError(t, svc.GetDeps(&DepsReq{TaskID: "B"}, &resp2))
	testutil.DeepEqual(t, resp2.Upstream, []string{"A"})
	testutil.DeepEqual(t, resp2.Downstream, []string{"D"})
}

// TestRPC_ListDAG filters by project, plan slug, and archived flag.
func TestRPC_ListDAG(t *testing.T) {
	d, _ := testDaemon(t)
	a := addTask(t, d, "A", nil)
	a.PlanSlug = "stack1"
	testutil.NoError(t, d.db.Update(a))
	b := addTask(t, d, "B", []string{"A"})
	b.PlanSlug = "stack1"
	testutil.NoError(t, d.db.Update(b))
	c := addTask(t, d, "C", nil)
	c.PlanSlug = "stack2"
	c.SetArchived(true)
	testutil.NoError(t, d.db.Update(c))

	svc := &RPCService{daemon: d}

	var all DAGResp
	testutil.NoError(t, svc.ListDAG(&DAGReq{Project: "proj"}, &all))
	testutil.Equal(t, len(all.Nodes), 2) // archived C excluded

	var withArchived DAGResp
	testutil.NoError(t, svc.ListDAG(&DAGReq{Project: "proj", IncludeArchived: true}, &withArchived))
	testutil.Equal(t, len(withArchived.Nodes), 3)

	var stack1 DAGResp
	testutil.NoError(t, svc.ListDAG(&DAGReq{Project: "proj", PlanSlug: "stack1"}, &stack1))
	testutil.Equal(t, len(stack1.Nodes), 2)

	// Edge data flows through DependsOn on each node.
	var bNode DAGNode
	for _, n := range stack1.Nodes {
		if n.ID == "B" {
			bNode = n
		}
	}
	testutil.DeepEqual(t, bNode.DependsOn, []string{"A"})
}

// TestRPC_HaltDownstream stops in_progress descendants and archives pending
// ones. The seed task itself is NOT halted — only its descendants — matching
// the orchestrator skill's halt contract (the failed task already reported,
// the cleanup is for the rest of the stack).
func TestRPC_HaltDownstream(t *testing.T) {
	d, _ := testDaemon(t)
	a := addTask(t, d, "A", nil)
	a.SetStatus(model.StatusInProgress)
	testutil.NoError(t, d.db.Update(a))

	b := addTask(t, d, "B", []string{"A"})
	b.SetStatus(model.StatusPending)
	testutil.NoError(t, d.db.Update(b))

	c := addTask(t, d, "C", []string{"B"})
	c.SetStatus(model.StatusPending)
	testutil.NoError(t, d.db.Update(c))

	// D is complete — must be skipped, not double-archived.
	dTask := addTask(t, d, "D", []string{"A"})
	dTask.SetStatus(model.StatusComplete)
	testutil.NoError(t, d.db.Update(dTask))

	svc := &RPCService{daemon: d}
	var resp HaltDownstreamResp
	testutil.NoError(t, svc.HaltDownstream(&HaltDownstreamReq{TaskID: "A"}, &resp))

	// B and C were pending → archived; D was complete → untouched.
	if len(resp.Archived) != 2 {
		t.Fatalf("expected 2 archived, got %v", resp.Archived)
	}

	gotB, _ := d.db.Get("B")
	testutil.True(t, gotB.Archived)
	gotC, _ := d.db.Get("C")
	testutil.True(t, gotC.Archived)
	gotD, _ := d.db.Get("D")
	testutil.False(t, gotD.Archived)
	// Seed task A itself must remain untouched.
	gotA, _ := d.db.Get("A")
	testutil.False(t, gotA.Archived)
	testutil.Equal(t, gotA.Status, model.StatusInProgress)
}

// TestRPC_HaltDownstream_RequiresID surfaces the bad-input path.
func TestRPC_HaltDownstream_RequiresID(t *testing.T) {
	d, _ := testDaemon(t)
	svc := &RPCService{daemon: d}
	var resp HaltDownstreamResp
	testutil.NoError(t, svc.HaltDownstream(&HaltDownstreamReq{}, &resp))
	testutil.NotEqual(t, resp.Error, "")
}

// TestRPC_SetPlanSlug writes and clears the orchestrator grouping label.
func TestRPC_SetPlanSlug(t *testing.T) {
	d, _ := testDaemon(t)
	addTask(t, d, "A", nil)

	svc := &RPCService{daemon: d}
	var resp StatusResp
	testutil.NoError(t, svc.SetPlanSlug(&SetPlanSlugReq{TaskID: "A", PlanSlug: "myplan"}, &resp))
	testutil.True(t, resp.OK)

	got, _ := d.db.Get("A")
	testutil.Equal(t, got.PlanSlug, "myplan")

	// Clearing is allowed — empty string is a valid value.
	var resp2 StatusResp
	testutil.NoError(t, svc.SetPlanSlug(&SetPlanSlugReq{TaskID: "A", PlanSlug: ""}, &resp2))
	testutil.True(t, resp2.OK)
	got2, _ := d.db.Get("A")
	testutil.Equal(t, got2.PlanSlug, "")
}

// TestRPC_SetPlanSlug_RequiresID covers the bad-input branch.
func TestRPC_SetPlanSlug_RequiresID(t *testing.T) {
	d, _ := testDaemon(t)
	svc := &RPCService{daemon: d}
	var resp StatusResp
	testutil.NoError(t, svc.SetPlanSlug(&SetPlanSlugReq{}, &resp))
	testutil.NotEqual(t, resp.Error, "")
}

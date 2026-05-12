package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// TestTaskLink_Happy and TestTaskLink_Cycle round-trip the link/unlink/cycle
// flow through the MCP handlers, which is the same code path orchestrate-stack
// will exercise from agents.
func TestMCP_TaskLink_Happy(t *testing.T) {
	s, dbm, _ := testServerWithTasks()
	dbm.tasks = append(dbm.tasks,
		&model.Task{ID: "p1", Name: "parent"},
		&model.Task{ID: "c1", Name: "child"},
	)

	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_link",
		Arguments: json.RawMessage(`{"child_id": "c1", "parent_id": "p1"}`),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("link errored: %s", cr.Content[0].Text)
	}
	testutil.Contains(t, cr.Content[0].Text, "Linked")

	got, _ := dbm.Get("c1")
	testutil.DeepEqual(t, got.DependsOn, []string{"p1"})
}

func TestMCP_TaskLink_Cycle(t *testing.T) {
	s, dbm, _ := testServerWithTasks()
	dbm.tasks = append(dbm.tasks,
		&model.Task{ID: "a", Name: "A", DependsOn: []string{"b"}},
		&model.Task{ID: "b", Name: "B"},
	)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_link",
		Arguments: json.RawMessage(`{"child_id": "b", "parent_id": "a"}`),
	})
	cr := callResult(t, resp)
	if !cr.IsError {
		t.Fatalf("expected cycle error, got success: %s", cr.Content[0].Text)
	}
	if !strings.Contains(cr.Content[0].Text, "cycle") {
		t.Fatalf("expected 'cycle' in error, got %q", cr.Content[0].Text)
	}
}

func TestMCP_TaskUnlink(t *testing.T) {
	s, dbm, _ := testServerWithTasks()
	dbm.tasks = append(dbm.tasks,
		&model.Task{ID: "p1", Name: "parent"},
		&model.Task{ID: "c1", Name: "child", DependsOn: []string{"p1"}},
	)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_unlink",
		Arguments: json.RawMessage(`{"child_id": "c1", "parent_id": "p1"}`),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("unlink errored: %s", cr.Content[0].Text)
	}
	got, _ := dbm.Get("c1")
	testutil.Equal(t, len(got.DependsOn), 0)
}

func TestMCP_TaskDeps(t *testing.T) {
	s, dbm, _ := testServerWithTasks()
	dbm.tasks = append(dbm.tasks,
		&model.Task{ID: "p1", Name: "parent"},
		&model.Task{ID: "c1", Name: "c1", DependsOn: []string{"p1"}},
		&model.Task{ID: "c2", Name: "c2", DependsOn: []string{"p1"}},
	)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_deps",
		Arguments: json.RawMessage(`{"id": "p1"}`),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("deps errored: %s", cr.Content[0].Text)
	}
	testutil.Contains(t, cr.Content[0].Text, "Downstream (2)")
	testutil.Contains(t, cr.Content[0].Text, "c1")
	testutil.Contains(t, cr.Content[0].Text, "c2")
}

func TestMCP_TaskHaltDownstream(t *testing.T) {
	s, dbm, stopper := testServerWithTasks()
	dbm.tasks = append(dbm.tasks,
		&model.Task{ID: "a", Name: "A", Status: model.StatusInProgress},
		&model.Task{ID: "b", Name: "B", DependsOn: []string{"a"}, Status: model.StatusPending},
	)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_halt_downstream",
		Arguments: json.RawMessage(`{"id": "a"}`),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("halt errored: %s", cr.Content[0].Text)
	}
	gotB, _ := dbm.Get("b")
	testutil.True(t, gotB.Archived)
	_ = stopper // pending-only graph; no stops expected
}

// TestMCP_TaskHaltDownstream_InProgressStops covers the stop-path that
// TestMCP_TaskHaltDownstream did not exercise: an in_progress descendant
// must route through the runner's Stop instead of being archived. The
// orch-layer test exists, but this verifies the MCP plumbing all the way
// through.
func TestMCP_TaskHaltDownstream_InProgressStops(t *testing.T) {
	s, dbm, stopper := testServerWithTasks()
	dbm.tasks = append(dbm.tasks,
		&model.Task{ID: "seed", Name: "seed", Status: model.StatusInProgress},
		&model.Task{ID: "running", Name: "running", DependsOn: []string{"seed"}, Status: model.StatusInProgress},
		&model.Task{ID: "waiting", Name: "waiting", DependsOn: []string{"seed"}, Status: model.StatusPending},
	)
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_halt_downstream",
		Arguments: json.RawMessage(`{"id": "seed"}`),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("halt errored: %s", cr.Content[0].Text)
	}
	// in_progress descendant routed through stopper; pending archived.
	testutil.DeepEqual(t, stopper.stopped, []string{"running"})
	gotWaiting, _ := dbm.Get("waiting")
	testutil.True(t, gotWaiting.Archived)
	// Seed is NEVER halted.
	gotSeed, _ := dbm.Get("seed")
	testutil.False(t, gotSeed.Archived)
	testutil.Equal(t, gotSeed.Status, model.StatusInProgress)
}

func TestMCP_TaskSetPlanSlug(t *testing.T) {
	s, dbm, _ := testServerWithTasks()
	dbm.tasks = append(dbm.tasks, &model.Task{ID: "t1", Name: "T"})
	resp := doRequest(t, s, "tools/call", ToolCallParams{
		Name:      "task_set_plan_slug",
		Arguments: json.RawMessage(`{"id": "t1", "plan_slug": "my-stack"}`),
	})
	cr := callResult(t, resp)
	if cr.IsError {
		t.Fatalf("set plan_slug errored: %s", cr.Content[0].Text)
	}
	got, _ := dbm.Get("t1")
	testutil.Equal(t, got.PlanSlug, "my-stack")
}

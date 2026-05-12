package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/orch"
)

// linkingToolDefs are appended to taskToolDefs when SetTaskManager has been
// called. Kept in a separate file so the linking surface is easy to audit
// independently of task_create / task_archive / etc.
var linkingToolDefs = []Tool{
	{
		Name:        "task_link",
		Description: `Add a dependency edge so child_id depends on parent_id. Child stays in 'pending' until parent reaches status=complete. Run the cycle check before persisting — if the new edge would close a cycle the call errors and returns the offending path ("A -> B -> A"). Idempotent: re-linking an already-existing edge is a no-op.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"child_id":  map[string]interface{}{"type": "string", "description": "Task that will gain a new upstream dep."},
				"parent_id": map[string]interface{}{"type": "string", "description": "Task that child_id will depend on."},
			},
			"required": []string{"child_id", "parent_id"},
		},
	},
	{
		Name:        "task_unlink",
		Description: `Remove the dependency edge so child_id no longer depends on parent_id. No-op when the edge does not exist; never produces a cycle.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"child_id":  map[string]interface{}{"type": "string"},
				"parent_id": map[string]interface{}{"type": "string"},
			},
			"required": []string{"child_id", "parent_id"},
		},
	},
	{
		Name:        "task_deps",
		Description: `Return the one-hop upstream + downstream neighbours of a task. Upstream is the task's own depends_on; downstream is every task whose depends_on contains this ID.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string"},
			},
			"required": []string{"id"},
		},
	},
	{
		Name: "task_halt_downstream",
		Description: `Cascade stop/archive through every transitive descendant of TaskID. Running tasks are stopped via the runner; pending tasks (still blocked) are archived. The seed task is NOT halted — only its descendants.

Use after a stack milestone fails so the rest of the chain doesn't waste effort. Tasks already 'complete' are skipped. Returns the per-row summary: which IDs were stopped, archived, or not-found.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id": map[string]interface{}{"type": "string", "description": "Seed task ID; descendants of this task are halted."},
			},
			"required": []string{"id"},
		},
	},
	{
		Name:        "task_set_plan_slug",
		Description: `Stamp a task with the orchestrator grouping label for the DAG view. Opaque to the daemon — same opacity contract as result. Empty string clears the slug. Use this in an orchestrator's stack-creation loop to mark every sub-task with the plan filename's slug so the DAG view can scope to one stack.`,
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"id":        map[string]interface{}{"type": "string"},
				"plan_slug": map[string]interface{}{"type": "string"},
			},
			"required": []string{"id", "plan_slug"},
		},
	},
}

func (s *Server) toolTaskLink(id interface{}, args json.RawMessage) *Response {
	if !s.taskMgmtEnabled() {
		return toolError(id, "task management not configured")
	}
	var p struct {
		ChildID  string `json:"child_id"`
		ParentID string `json:"parent_id"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck
	err := orch.Link(s.taskDB, p.ChildID, p.ParentID)
	var ce *orch.CycleError
	if errors.As(err, &ce) {
		return toolError(id, fmt.Sprintf("cycle detected: %s", strings.Join(ce.Path, " -> ")))
	}
	if err != nil {
		return toolError(id, err.Error())
	}
	return toolResult(id, fmt.Sprintf("Linked: %s now depends on %s", p.ChildID, p.ParentID))
}

func (s *Server) toolTaskUnlink(id interface{}, args json.RawMessage) *Response {
	if !s.taskMgmtEnabled() {
		return toolError(id, "task management not configured")
	}
	var p struct {
		ChildID  string `json:"child_id"`
		ParentID string `json:"parent_id"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck
	if err := orch.Unlink(s.taskDB, p.ChildID, p.ParentID); err != nil {
		return toolError(id, err.Error())
	}
	return toolResult(id, fmt.Sprintf("Unlinked: %s no longer depends on %s", p.ChildID, p.ParentID))
}

func (s *Server) toolTaskDeps(id interface{}, args json.RawMessage) *Response {
	if !s.taskMgmtEnabled() {
		return toolError(id, "task management not configured")
	}
	var p struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck
	view, err := orch.Deps(s.taskDB, p.ID)
	if err != nil {
		return toolError(id, err.Error())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Upstream (%d): %s\n", len(view.Upstream), strings.Join(view.Upstream, ", "))
	fmt.Fprintf(&b, "Downstream (%d): %s", len(view.Downstream), strings.Join(view.Downstream, ", "))
	return toolResult(id, b.String())
}

func (s *Server) toolTaskHaltDownstream(id interface{}, args json.RawMessage) *Response {
	if !s.taskMgmtEnabled() {
		return toolError(id, "task management not configured")
	}
	var p struct {
		ID string `json:"id"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck
	report, err := orch.HaltDownstream(s.taskDB, s.taskStopper, p.ID, func(err error) bool {
		return errors.Is(err, agent.ErrSessionNotFound)
	})
	if err != nil {
		return toolError(id, err.Error())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Halt cascade from %s:\n", p.ID)
	fmt.Fprintf(&b, "  Stopped (%d): %s\n", len(report.Stopped), strings.Join(report.Stopped, ", "))
	fmt.Fprintf(&b, "  Archived (%d): %s", len(report.Archived), strings.Join(report.Archived, ", "))
	if len(report.NotFound) > 0 {
		fmt.Fprintf(&b, "\n  Not found (%d): %s", len(report.NotFound), strings.Join(report.NotFound, ", "))
	}
	return toolResult(id, b.String())
}

func (s *Server) toolTaskSetPlanSlug(id interface{}, args json.RawMessage) *Response {
	if !s.taskMgmtEnabled() {
		return toolError(id, "task management not configured")
	}
	var p struct {
		ID       string `json:"id"`
		PlanSlug string `json:"plan_slug"`
	}
	json.Unmarshal(args, &p) //nolint:errcheck
	if err := orch.SetPlanSlug(s.taskDB, p.ID, p.PlanSlug); err != nil {
		return toolError(id, err.Error())
	}
	return toolResult(id, fmt.Sprintf("Set plan_slug=%q on %s", p.PlanSlug, p.ID))
}

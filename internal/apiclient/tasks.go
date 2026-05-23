package apiclient

import (
	"context"
	"fmt"

	"github.com/drn/argus/internal/model"
)

// TaskJSON mirrors api.taskJSON — the wire shape returned by /api/tasks*.
// The fields match exactly so the TUI's store adapter can convert to/from
// model.Task without a separate type definition per surface.
type TaskJSON struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Idle         bool   `json:"idle,omitempty"`
	Project      string `json:"project"`
	Branch       string `json:"branch,omitempty"`
	Backend      string `json:"backend,omitempty"`
	Elapsed      string `json:"elapsed,omitempty"`
	CreatedAt    string `json:"created_at"`
	Archived     bool   `json:"archived,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	Prompt       string `json:"prompt,omitempty"`
}

// ListTasksFilter narrows the list returned by ListTasks. Empty fields are
// treated as "no filter". Archived has three modes: "0" (exclude archived),
// "1" (only archived), "all" (both); empty string is treated as "0".
type ListTasksFilter struct {
	Status   string
	Project  string
	Archived string
}

// ListTasks fetches tasks matching the filter.
func (c *Client) ListTasks(ctx context.Context, f ListTasksFilter) ([]TaskJSON, error) {
	path := "/api/tasks" + query("status", f.Status, "project", f.Project, "archived", f.Archived)
	var resp struct {
		Tasks []TaskJSON `json:"tasks"`
	}
	if err := c.doJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

// GetTask fetches a single task by ID.
func (c *Client) GetTask(ctx context.Context, id string) (*TaskJSON, error) {
	var t TaskJSON
	if err := c.doJSON(ctx, "GET", "/api/tasks/"+id, nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateTaskReq mirrors api.createTaskReq.
type CreateTaskReq struct {
	Name    string `json:"name"`
	Prompt  string `json:"prompt"`
	Project string `json:"project"`
	Backend string `json:"backend,omitempty"`
}

// CreateTaskResp is the create-task response envelope.
type CreateTaskResp struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// CreateTask creates a new task and starts its agent session. Multipart
// uploads (attachments) are not supported by this method — use the dedicated
// HTTP form path for those.
func (c *Client) CreateTask(ctx context.Context, req CreateTaskReq) (*CreateTaskResp, error) {
	var resp CreateTaskResp
	if err := c.doJSON(ctx, "POST", "/api/tasks", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// StopTask stops the agent session and flips the task to in_review.
func (c *Client) StopTask(ctx context.Context, id string) error {
	return c.doJSON(ctx, "POST", "/api/tasks/"+id+"/stop", nil, nil)
}

// ResumeResp is the resume-task response envelope.
type ResumeResp struct {
	Status string `json:"status"`
	PID    int    `json:"pid"`
	Healed bool   `json:"healed,omitempty"`
}

// ResumeTask resumes (or starts fresh) the agent session for an existing task.
func (c *Client) ResumeTask(ctx context.Context, id string) (*ResumeResp, error) {
	var resp ResumeResp
	if err := c.doJSON(ctx, "POST", "/api/tasks/"+id+"/resume", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteTask stops the session, removes the worktree + branch, and deletes
// the DB row.
func (c *Client) DeleteTask(ctx context.Context, id string) error {
	return c.doJSON(ctx, "DELETE", "/api/tasks/"+id, nil, nil)
}

// ArchiveTask flips task.archived=true.
func (c *Client) ArchiveTask(ctx context.Context, id string) error {
	return c.doJSON(ctx, "POST", "/api/tasks/"+id+"/archive", nil, nil)
}

// UnarchiveTask flips task.archived=false.
func (c *Client) UnarchiveTask(ctx context.Context, id string) error {
	return c.doJSON(ctx, "POST", "/api/tasks/"+id+"/unarchive", nil, nil)
}

// RenameTask renames an existing task.
func (c *Client) RenameTask(ctx context.Context, id, name string) error {
	return c.doJSON(ctx, "POST", "/api/tasks/"+id+"/rename", map[string]string{"name": name}, nil)
}

// SetStatus moves a task to one of pending/in_progress/in_review/complete.
func (c *Client) SetStatus(ctx context.Context, id, status string) error {
	return c.doJSON(ctx, "POST", "/api/tasks/"+id+"/status", map[string]string{"status": status}, nil)
}

// ForkReq mirrors api.forkReq.
type ForkReq struct {
	Name    string `json:"name"`
	Prompt  string `json:"prompt"`
	Project string `json:"project"`
}

// ForkTask creates a new task forking from the given source. Empty fields in
// req inherit from the source.
func (c *Client) ForkTask(ctx context.Context, srcID string, req ForkReq) (*CreateTaskResp, error) {
	var resp CreateTaskResp
	if err := c.doJSON(ctx, "POST", "/api/tasks/"+srcID+"/fork", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SetPlanSlug stamps a task with the orchestrator grouping label. Empty
// string clears it.
func (c *Client) SetPlanSlug(ctx context.Context, id, slug string) error {
	return c.doJSON(ctx, "POST", "/api/tasks/"+id+"/plan-slug", map[string]string{"plan_slug": slug}, nil)
}

// LinkTask attaches a parent task to a child via depends_on. Returns
// *Error{Status: 409} when the link would create a cycle.
func (c *Client) LinkTask(ctx context.Context, childID, parentID string) error {
	return c.doJSON(ctx, "POST", "/api/tasks/"+childID+"/deps", map[string]string{"parent_id": parentID}, nil)
}

// UnlinkTask removes a parent from a child's depends_on.
func (c *Client) UnlinkTask(ctx context.Context, childID, parentID string) error {
	return c.doJSON(ctx, "DELETE", fmt.Sprintf("/api/tasks/%s/deps/%s", childID, parentID), nil, nil)
}

// GetDeps returns the one-hop upstream + downstream view for a task.
// Returned shape is opaque map — the TUI only renders it through the
// existing orch package via /api/dag, so a thin map is enough.
func (c *Client) GetDeps(ctx context.Context, id string) (map[string]any, error) {
	out := make(map[string]any)
	if err := c.doJSON(ctx, "GET", "/api/tasks/"+id+"/deps", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// HaltDownstreamReport is the per-row summary returned by halt-downstream.
type HaltDownstreamReport struct {
	Halted   int      `json:"halted"`
	Stopped  int      `json:"stopped"`
	Archived int      `json:"archived"`
	IDs      []string `json:"ids,omitempty"`
}

// HaltDownstream cascades stop/archive through a task's descendants.
func (c *Client) HaltDownstream(ctx context.Context, id string) (*HaltDownstreamReport, error) {
	var resp HaltDownstreamReport
	if err := c.doJSON(ctx, "POST", "/api/tasks/"+id+"/halt-downstream", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DAGFilter mirrors orch.DAGFilter query params.
type DAGFilter struct {
	Project         string
	PlanSlug        string
	IncludeArchived bool
}

// GetDAG returns the full DAG node list for rendering.
func (c *Client) GetDAG(ctx context.Context, f DAGFilter) ([]map[string]any, error) {
	arch := ""
	if f.IncludeArchived {
		arch = "1"
	}
	path := "/api/dag" + query("project", f.Project, "plan", f.PlanSlug, "archived", arch)
	var resp struct {
		Nodes []map[string]any `json:"nodes"`
	}
	if err := c.doJSON(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Nodes, nil
}

// StopAll stops every running session and marks all in_progress tasks
// in_review. Master-only.
func (c *Client) StopAll(ctx context.Context) (int, error) {
	var resp struct {
		Stopped int `json:"stopped"`
	}
	if err := c.doJSON(ctx, "POST", "/api/sessions/stop-all", nil, &resp); err != nil {
		return 0, err
	}
	return resp.Stopped, nil
}

// PruneReport is the prune-completed response envelope.
type PruneReport struct {
	Pruned    int `json:"pruned"`
	Worktrees int `json:"worktrees"`
	Orphans   int `json:"orphans"`
}

// PruneCompleted removes every task with status=complete, sweeps orphan
// worktrees. Master-only.
func (c *Client) PruneCompleted(ctx context.Context) (*PruneReport, error) {
	var resp PruneReport
	if err := c.doJSON(ctx, "POST", "/api/maintenance/prune-completed", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ListTasksRaw returns every task as a full model.Task. Use this in the TUI
// store adapter where lossy TaskJSON would drop fields like SessionID,
// DependsOn, Result, AgentPID, Pinned, etc. Master-only on the server.
// Added in phase 3 (gap fill for tui store interface).
func (c *Client) ListTasksRaw(ctx context.Context) ([]*model.Task, error) {
	var resp struct {
		Tasks []*model.Task `json:"tasks"`
	}
	if err := c.doJSON(ctx, "GET", "/api/tasks-raw", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Tasks, nil
}

// GetTaskRaw returns one task as a full model.Task. Added in phase 3.
func (c *Client) GetTaskRaw(ctx context.Context, id string) (*model.Task, error) {
	var t model.Task
	if err := c.doJSON(ctx, "GET", "/api/tasks/"+id+"/raw", nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// UpdateTaskRaw applies a full model.Task overwrite. Master-only. Added in
// phase 3 — the TUI's store adapter uses this to mirror db.DB.Update().
func (c *Client) UpdateTaskRaw(ctx context.Context, t *model.Task) error {
	return c.doJSON(ctx, "PUT", "/api/tasks/"+t.ID+"/raw", t, nil)
}

// AddTaskRaw inserts a model.Task row directly. Master-only. Used by the TUI
// store adapter to mirror db.DB.Add() for paths that don't go through the
// agent.CreateAndStart lifecycle (e.g., orchestrator stack creation).
//
// Mutates t in place with any server-assigned fields (ID, CreatedAt) so
// callers can keep using the local struct after the call — *db.DB.Add()
// has the same contract.
func (c *Client) AddTaskRaw(ctx context.Context, t *model.Task) error {
	var resp model.Task
	if err := c.doJSON(ctx, "POST", "/api/tasks-raw", t, &resp); err != nil {
		return err
	}
	if resp.ID != "" {
		*t = resp
	}
	return nil
}

// GetScheduleRaw returns a schedule as a full model.ScheduledTask. Master-only.
// Added in phase 3.
func (c *Client) GetScheduleRaw(ctx context.Context, id string) (*model.ScheduledTask, error) {
	var s model.ScheduledTask
	if err := c.doJSON(ctx, "GET", "/api/schedules/"+id+"/raw", nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Package orch holds the task-linking / DAG / halt-downstream operations
// that the daemon RPC service and the HTTP API both invoke. The logic lives
// here rather than in daemon so the api package (which cannot import daemon
// without inducing a cycle) can reuse it directly.
//
// Each operation is a pure function over a *db.DB plus minimal collaborators;
// the daemon RPC wrappers are now thin and exist solely to bridge net/rpc's
// request/response shape into these helpers.
package orch

import (
	"errors"
	"fmt"
	"slices"

	"github.com/drn/argus/internal/model"
)

// Store is the narrow DB surface this package uses. *db.DB satisfies it, as
// does the MCP server's TaskStore — both call sites share orch.* without
// importing db directly.
type Store interface {
	Tasks() ([]*model.Task, error)
	Get(id string) (*model.Task, error)
	Update(t *model.Task) error
}

// Stopper aborts a running session. The HTTP and RPC paths both pass a
// *agent.Runner here; tests pass a stub that records calls.
type Stopper interface {
	Stop(taskID string) error
}

// DAGNode is the minimal projection of a task used for DAG rendering.
// Status/Archived/Result are everything the renderer needs.
type DAGNode struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Archived  bool     `json:"archived"`
	PlanSlug  string   `json:"plan_slug,omitempty"`
	Result    string   `json:"result,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// DAGFilter scopes the DAG snapshot. Empty Project / PlanSlug match all.
// IncludeArchived defaults to false; the DAG view passes true so retried
// stacks render in greyed-out form rather than disappearing.
type DAGFilter struct {
	Project         string
	PlanSlug        string
	IncludeArchived bool
}

// DepsView captures the one-hop neighbours of a task in both directions.
type DepsView struct {
	Upstream   []string `json:"upstream"`
	Downstream []string `json:"downstream"`
}

// HaltReport summarises the outcome of HaltDownstream.
type HaltReport struct {
	Stopped  []string `json:"stopped"`
	Archived []string `json:"archived"`
	NotFound []string `json:"not_found,omitempty"`
}

// CycleError is returned when a link would close a cycle. The path is in
// dependency order (first == last == offending node), so callers can render
// "A → B → C → A".
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("cycle detected: %v", e.Path)
}

// ErrEmptyID is returned for empty ID arguments. Keeping it as a sentinel
// lets HTTP handlers map it to 400, not 500.
var ErrEmptyID = errors.New("task id is required")

// FindCycle performs a DFS on the depends_on graph and returns the cycle path
// (node IDs in dependency order, starting and ending with the same ID) if
// one exists, or nil otherwise. The graph is built from the supplied task
// list so callers can stage hypothetical edges without persisting them.
// Tasks not present in the snapshot are treated as terminal.
//
// Returning the path (not just a bool) is the contract the linking UI relies
// on: a "cycle detected" error without the offending sequence is unactionable.
func FindCycle(tasks []*model.Task, startID string) []string {
	index := make(map[string]*model.Task, len(tasks))
	for _, t := range tasks {
		index[t.ID] = t
	}

	const (
		unvisited = 0
		onStack   = 1
		visited   = 2
	)
	color := make(map[string]int, len(tasks))
	parent := make(map[string]string, len(tasks))

	var stack []string
	push := func(id, from string) {
		stack = append(stack, id)
		color[id] = onStack
		parent[id] = from
	}
	pop := func() {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		color[id] = visited
	}

	push(startID, "")
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		t, ok := index[top]
		if !ok {
			pop()
			continue
		}
		var nextChild string
		for _, dep := range t.DependsOn {
			if color[dep] == unvisited {
				nextChild = dep
				break
			}
			if color[dep] == onStack {
				cycle := []string{dep}
				cur := top
				for cur != dep && cur != "" {
					cycle = append(cycle, cur)
					cur = parent[cur]
				}
				cycle = append(cycle, dep)
				for i, j := 0, len(cycle)-1; i < j; i, j = i+1, j-1 {
					cycle[i], cycle[j] = cycle[j], cycle[i]
				}
				return cycle
			}
		}
		if nextChild == "" {
			pop()
			continue
		}
		push(nextChild, top)
	}
	return nil
}

// Link adds parentID to childID's depends_on. Returns a *CycleError if the
// edge would close a cycle (state is left unchanged in that case). A
// self-loop (child==parent) is rejected with a fabricated two-element path
// so the UI does not have to special-case it.
func Link(database Store, childID, parentID string) error {
	if childID == "" || parentID == "" {
		return ErrEmptyID
	}
	if childID == parentID {
		return &CycleError{Path: []string{childID, childID}}
	}

	child, err := database.Get(childID)
	if err != nil {
		return err
	}
	if _, err := database.Get(parentID); err != nil {
		return err
	}
	if slices.Contains(child.DependsOn, parentID) {
		return nil
	}

	tasks, err := database.Tasks()
	if err != nil {
		return err
	}
	// Stage the hypothetical edge on a child *copy* so the snapshot mutation
	// doesn't alias the row returned to the caller. Without the copy, a Store
	// implementation that shares pointers between Get() and Tasks() (e.g. the
	// MCP test mock) would see the parent appended twice — once via the
	// snapshot mutation, once via the post-cycle-check Update path.
	stagedTasks := make([]*model.Task, len(tasks))
	for i, t := range tasks {
		if t.ID == childID {
			c := *t
			c.DependsOn = append(append([]string(nil), t.DependsOn...), parentID)
			stagedTasks[i] = &c
		} else {
			stagedTasks[i] = t
		}
	}
	if cycle := FindCycle(stagedTasks, childID); cycle != nil {
		return &CycleError{Path: cycle}
	}

	child.DependsOn = append(child.DependsOn, parentID)
	return database.Update(child)
}

// Unlink removes parentID from childID's depends_on. No-op if the edge does
// not exist. Cannot induce a cycle.
func Unlink(database Store, childID, parentID string) error {
	if childID == "" || parentID == "" {
		return ErrEmptyID
	}
	child, err := database.Get(childID)
	if err != nil {
		return err
	}
	filtered := make([]string, 0, len(child.DependsOn))
	for _, dep := range child.DependsOn {
		if dep != parentID {
			filtered = append(filtered, dep)
		}
	}
	if len(filtered) == len(child.DependsOn) {
		return nil
	}
	child.DependsOn = filtered
	return database.Update(child)
}

// Deps returns the one-hop neighbours of taskID. Linear scan; the dataset
// is small enough that maintaining a reverse-edge index would cost more in
// invalidation than it saves in lookups.
func Deps(database Store, taskID string) (DepsView, error) {
	if taskID == "" {
		return DepsView{}, ErrEmptyID
	}
	t, err := database.Get(taskID)
	if err != nil {
		return DepsView{}, err
	}
	view := DepsView{
		Upstream: append([]string(nil), t.DependsOn...),
	}
	tasks, err := database.Tasks()
	if err != nil {
		return DepsView{}, err
	}
	for _, other := range tasks {
		if slices.Contains(other.DependsOn, taskID) {
			view.Downstream = append(view.Downstream, other.ID)
		}
	}
	return view, nil
}

// ListDAG returns the nodes matching the filter. Edges are implicit in each
// node's DependsOn — the client materialises them.
func ListDAG(database Store, filter DAGFilter) ([]DAGNode, error) {
	tasks, err := database.Tasks()
	if err != nil {
		return nil, err
	}
	var out []DAGNode
	for _, t := range tasks {
		if filter.Project != "" && t.Project != filter.Project {
			continue
		}
		if filter.PlanSlug != "" && t.PlanSlug != filter.PlanSlug {
			continue
		}
		if t.Archived && !filter.IncludeArchived {
			continue
		}
		out = append(out, DAGNode{
			ID:        t.ID,
			Name:      t.Name,
			Status:    t.Status.String(),
			Archived:  t.Archived,
			PlanSlug:  t.PlanSlug,
			Result:    t.Result,
			DependsOn: append([]string(nil), t.DependsOn...),
		})
	}
	return out, nil
}

// HaltDownstream stops in_progress descendants and archives pending ones.
// The seed task is NOT halted — the caller's own failure is the reason this
// cascade was triggered, and double-halting the seed loses status context
// (e.g. its result.failed payload).
//
// Re-queries each row's status inside the loop because the depswatcher can
// flip a pending row to in_progress between the snapshot and the per-row
// decision; in that case the row is stopped rather than archived.
func HaltDownstream(database Store, stopper Stopper, taskID string) (HaltReport, error) {
	var report HaltReport
	if taskID == "" {
		return report, ErrEmptyID
	}
	tasks, err := database.Tasks()
	if err != nil {
		return report, err
	}

	children := make(map[string][]string, len(tasks))
	for _, t := range tasks {
		for _, dep := range t.DependsOn {
			children[dep] = append(children[dep], t.ID)
		}
	}

	visited := map[string]bool{}
	var order []string
	queue := append([]string(nil), children[taskID]...)
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if visited[next] {
			continue
		}
		visited[next] = true
		order = append(order, next)
		queue = append(queue, children[next]...)
	}

	for _, id := range order {
		current, err := database.Get(id)
		if err != nil {
			report.NotFound = append(report.NotFound, id)
			continue
		}
		switch current.Status {
		case model.StatusComplete:
			continue
		case model.StatusPending:
			current.SetArchived(true)
			if err := database.Update(current); err != nil {
				continue
			}
			report.Archived = append(report.Archived, id)
		default:
			_ = stopper.Stop(id)
			report.Stopped = append(report.Stopped, id)
		}
	}
	return report, nil
}

// SetPlanSlug writes the orchestrator grouping label. Daemon does not
// interpret the value — same opacity contract as result.
func SetPlanSlug(database Store, taskID, slug string) error {
	if taskID == "" {
		return ErrEmptyID
	}
	t, err := database.Get(taskID)
	if err != nil {
		return err
	}
	t.PlanSlug = slug
	return database.Update(t)
}

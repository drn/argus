package hera

import (
	"fmt"
	"strings"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/tui/planview"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
)

// heraPlanNodes projects an orchestrator's PLAN DAG — its planned (never-bound)
// and live worker roles as nodes, its hera_blocks blocking edges as dependency
// edges — into the planview widget's input. It is the plan-view parallel to the
// retired heraTreeNodes (which projected the role hierarchy); this projects the
// order-of-ops graph the coordinator authored.
//
// It is a pure function over the already-built OrchView (the rail's snapshot,
// with OrchView.Blocks populated by BuildModel) — no DB read, no I/O — so it is
// safe on the tview thread and trivially testable. A live node carries its
// bound task's status/result (the colour source); a planned node carries
// State=StatePlanned. The degenerate "no plan authored" case (no planned nodes,
// no edges) yields the live worker roles as a flat edgeless single stage (D1).
//
// Sub-coordinator drill-in (Node.Drillable, D6) needs the whole Model to see
// sibling orchestrators, which a lone OrchView cannot; this single-arg form (the
// test surface) leaves every node un-drillable. The Hera page calls
// heraPlanNodesWithBridge with the rail's bridge index to stamp Drillable.
func heraPlanNodes(orch *OrchView) ([]planview.Node, []planview.Edge) {
	return heraPlanNodesWithBridge(orch, nil)
}

// heraPlanNodesWithBridge is the Model-aware projection: `bridge` maps each
// orchestrator's coordinator bridge task to the orchestrator it coordinates
// (Model.bridgeIndex). A worker node whose bound task is a key in that map IS a
// sub-coordinator (its task is some child orchestrator's coordinator) and is
// marked Drillable. Pass nil bridge to disable drill-in (the lone-OrchView path).
func heraPlanNodesWithBridge(orch *OrchView, bridge map[string]*OrchView) ([]planview.Node, []planview.Edge) {
	if orch == nil {
		return nil, nil
	}

	// One roleID -> nodeID map drives BOTH nodes and edges so an edge's endpoints
	// resolve to the same ids the nodes carry (a live node's id is its bound task,
	// a planned node's id is a synthetic role key). Coordinator roles are not plan
	// nodes (they author the plan; the DAG is the worker order-of-ops), so they
	// are skipped here.
	nodeID := make(map[int64]string, len(orch.Roles))
	nodes := make([]planview.Node, 0, len(orch.Roles))
	for i := range orch.Roles {
		r := &orch.Roles[i]
		if r.Kind != db.HeraKindWorker || r.Archived {
			continue
		}
		id := planNodeID(r)
		nodeID[r.RoleID] = id
		state := planNodeState(r)
		n := planview.Node{
			ID:          id,
			Name:        r.Name,
			Planned:     r.Planned,
			State:       state,
			Description: firstLine(r.Prompt),
			Icon:        planNodeIcon(r, state),
		}
		// A worker whose bound task coordinates a child orchestrator is a
		// sub-coordinator: Enter drills into that child's plan DAG (D6). The bridge
		// index already encodes the worker→child-coordinator relationship the rail
		// uses; reuse it rather than re-deriving. A node with no bound task
		// (planned) can never be a sub-coordinator.
		if bridge != nil && r.BridgeTaskID != "" {
			if child := bridge[r.BridgeTaskID]; child != nil && child.ID != orch.ID {
				n.Drillable = true
			}
		}
		nodes = append(nodes, n)
	}

	// Edges: every blocking edge whose BOTH endpoints projected to a node. An edge
	// to a coordinator or archived role (not in nodeID) is dropped — ListHeraBlocks
	// already excludes archived/nuked endpoints, but guard anyway so a stray edge
	// never references a missing node. Direction matches dagview: From=blocker
	// (upstream), To=blocked (downstream).
	edges := make([]planview.Edge, 0, len(orch.Blocks))
	for _, b := range orch.Blocks {
		from, okFrom := nodeID[b.BlockerRoleID]
		to, okTo := nodeID[b.BlockedRoleID]
		if !okFrom || !okTo {
			continue
		}
		edges = append(edges, planview.Edge{From: from, To: to})
	}

	uxlog.Log("[planview] projected orch %d: %d nodes, %d edges", orch.ID, len(nodes), len(edges))
	return nodes, edges
}

// planNodeID is a plan node's stable identity: a live node's bound argus task id
// (so the planview OnEnter can jump to its agent view) or, for a never-bound
// planned role, a synthetic key derived from the role id. The two id spaces
// cannot collide — a synthetic key is prefixed, a task id never is.
func planNodeID(r *RoleView) string {
	if r.TaskID != "" {
		return r.TaskID
	}
	if r.BridgeTaskID != "" {
		// A finished worker (binding ended, not planned) still keys off its task.
		return r.BridgeTaskID
	}
	return fmt.Sprintf("plan:%d", r.RoleID)
}

// planNodeState maps a role to its render state (D7). A planned (never-bound)
// role is StatePlanned (violet ○). A live/finished role colours from its bound
// argus task status + result, with the {"failed":true} result winning over the
// workflow status (red ✕) — reusing coordTaskFailed (details.go), not a third copy.
func planNodeState(r *RoleView) planview.State {
	if r.Planned {
		return planview.StatePlanned
	}
	if coordTaskFailed(r.TaskResult) {
		return planview.StateFailed
	}
	switch r.TaskStatus {
	case model.StatusComplete.String():
		return planview.StateDone
	case model.StatusInReview.String():
		return planview.StateInReview
	case model.StatusInProgress.String():
		return planview.StateWorking
	default:
		// pending / unknown / unbound-but-once-materialized → pending dot.
		return planview.StatePending
	}
}

// planNodeIcon resolves a LIVE node's status icon 1:1 with the rail (BUG-007):
// the SAME shared classifier (widget.RoleStatusIcon over roleStatusInputs) the
// rail's statusIcon uses, so the plan node and the rail row never disagree. It
// returns nil for the two plan-view-specific overlays the rail has no concept of
// — a PLANNED (never-bound) node (rendered ○ via State) and a FAILED node
// (rendered ✕ via State) — letting the widget fall back to the State glyph there.
//
// Animated marks the genuinely-active "working" case so the widget re-resolves
// the live spinner frame at Draw. It is derived from the classifier's OWN frame-0
// output (glyph == the spinner frame), NOT from in.Active directly: in.Active is
// NOT mutually exclusive with the higher-precedence signals (a live in_progress
// worker blocked on a prompt is BOTH active AND needs-input), and the classifier
// correctly resolves that to the STATIC needs-input "?" (needs-input outranks
// active). Flagging such a node Animated would make the widget swap the "?" for
// the spinner frame at Draw, breaking 1:1 parity with the rail row (which shows
// "?"). Comparing the resolved glyph to the spinner keeps Animated true ONLY when
// the spinner actually won, with zero duplication of the classifier's precedence
// (and it tracks the active spinner style, which both sides read). See BUG-012.
func planNodeIcon(r *RoleView, state planview.State) *planview.NodeIcon {
	if state == planview.StatePlanned || state == planview.StateFailed {
		return nil // ○ / ✕ overlays come from State (the rail has neither)
	}
	in := roleStatusInputs(r)
	glyph, style := widget.RoleStatusIcon(in, false, 0)
	animated := glyph == widget.SpinnerFrame(0)
	return &planview.NodeIcon{Glyph: glyph, Style: style, Animated: animated}
}

// firstLine returns the first line of s, trimmed of surrounding whitespace, for
// the header Description. Empty when s is empty (the widget shows "(no description)").
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

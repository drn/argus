package hera

import (
	"github.com/drn/argus/internal/tui/planview"
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
// Stage 2 implements this.
func heraPlanNodes(orch *OrchView) ([]planview.Node, []planview.Edge) {
	return nil, nil
}

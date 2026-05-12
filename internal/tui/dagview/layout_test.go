package dagview

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
)

// nodeIDs returns the IDs of placed nodes in layer-major, column-minor order.
// Useful for asserting the deterministic ordering invariant without depending
// on map iteration.
func nodeIDs(l Layout) []string {
	out := make([]string, 0, len(l.Nodes))
	for _, n := range l.Nodes {
		out = append(out, n.ID)
	}
	return out
}

// nodeLayers returns each placed node's layer keyed by ID.
func nodeLayers(l Layout) map[string]int {
	m := map[string]int{}
	for _, n := range l.Nodes {
		m[n.ID] = n.Layer
	}
	return m
}

func TestLayout_LinearChain(t *testing.T) {
	l := Compute([]Node{
		{ID: "A"},
		{ID: "B", DependsOn: []string{"A"}},
		{ID: "C", DependsOn: []string{"B"}},
	})
	testutil.Equal(t, l.Layers, 3)
	testutil.Equal(t, l.Width, 1)
	got := nodeLayers(l)
	testutil.Equal(t, got["A"], 0)
	testutil.Equal(t, got["B"], 1)
	testutil.Equal(t, got["C"], 2)
}

func TestLayout_FanOut(t *testing.T) {
	l := Compute([]Node{
		{ID: "A"},
		{ID: "B", DependsOn: []string{"A"}},
		{ID: "C", DependsOn: []string{"A"}},
		{ID: "D", DependsOn: []string{"A"}},
	})
	testutil.Equal(t, l.Layers, 2)
	testutil.Equal(t, l.Width, 3) // B, C, D share layer 1
	got := nodeLayers(l)
	testutil.Equal(t, got["A"], 0)
	testutil.Equal(t, got["B"], 1)
	testutil.Equal(t, got["C"], 1)
	testutil.Equal(t, got["D"], 1)
}

func TestLayout_Diamond(t *testing.T) {
	l := Compute([]Node{
		{ID: "A"},
		{ID: "B", DependsOn: []string{"A"}},
		{ID: "C", DependsOn: []string{"A"}},
		{ID: "D", DependsOn: []string{"B", "C"}},
	})
	got := nodeLayers(l)
	testutil.Equal(t, got["A"], 0)
	testutil.Equal(t, got["B"], 1)
	testutil.Equal(t, got["C"], 1)
	testutil.Equal(t, got["D"], 2)
}

// TestLayout_DroppedStaleParent — when a DependsOn references an unknown ID
// the layout must keep the node, prune the edge, and not crash. Mirrors the
// daemon contract: stale parents are surfaced to the orchestrator as a
// dangling reference, not fatal.
func TestLayout_DroppedStaleParent(t *testing.T) {
	l := Compute([]Node{
		{ID: "A", DependsOn: []string{"ghost"}},
	})
	testutil.Equal(t, len(l.Nodes), 1)
	testutil.Equal(t, len(l.Edges), 0)
	testutil.Equal(t, l.Nodes[0].Layer, 0)
}

// TestLayout_DeterministicOrdering — two computes of the same graph yield
// identical ID orderings. Without this, golden render tests would flake
// across runs due to map iteration randomness.
func TestLayout_DeterministicOrdering(t *testing.T) {
	nodes := []Node{
		{ID: "A"},
		{ID: "B", DependsOn: []string{"A"}},
		{ID: "C", DependsOn: []string{"A"}},
		{ID: "D", DependsOn: []string{"A"}},
	}
	for i := 0; i < 5; i++ {
		l1 := Compute(nodes)
		l2 := Compute(nodes)
		testutil.DeepEqual(t, nodeIDs(l1), nodeIDs(l2))
	}
}

// TestLayout_MCPv1Shape — the actual stack from
// memory/handoff/2026-05-11-175011-orchestrate-mcp-v1-execution.md. Verifies
// the layout doesn't explode on the real target workload.
func TestLayout_MCPv1Shape(t *testing.T) {
	l := Compute([]Node{
		{ID: "M0"},
		{ID: "M6F"},
		{ID: "M1", DependsOn: []string{"M0"}},
		{ID: "M2", DependsOn: []string{"M1"}},
		{ID: "M3", DependsOn: []string{"M1"}},
		{ID: "M8", DependsOn: []string{"M1"}},
		{ID: "M4", DependsOn: []string{"M3"}},
		{ID: "M5", DependsOn: []string{"M3"}},
		{ID: "M6A", DependsOn: []string{"M5"}},
		{ID: "M6B", DependsOn: []string{"M6A"}},
		{ID: "M6C", DependsOn: []string{"M6B"}},
		{ID: "M7", DependsOn: []string{"M6C"}},
		{ID: "M9", DependsOn: []string{"M7"}},
		{ID: "M12", DependsOn: []string{"M9", "M4", "M2", "M8"}},
	})
	got := nodeLayers(l)
	// M0 and M6F are sources (no parents).
	testutil.Equal(t, got["M0"], 0)
	testutil.Equal(t, got["M6F"], 0)
	// Final acceptance is the deepest node.
	testutil.Equal(t, got["M12"] > got["M9"], true)
	// Width is bounded; ≤ 10 keeps the layout viewable in a normal terminal.
	testutil.Equal(t, l.Width <= 10, true)
}

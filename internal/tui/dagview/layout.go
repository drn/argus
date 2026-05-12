// Package dagview is the TUI widget that renders a task DAG. The algorithm is
// Sugiyama-lite: assign each node a layer via Kahn topological sort, then
// reorder within each layer via barycentric placement to reduce edge crossings.
// Straight vertical edges with right-angle horizontal jogs are good enough at
// the scale of Argus stacks (≤ ~30 nodes); the renderer makes no attempt at
// spline routing or auto-bundling.
package dagview

// Node is the input projection for the layout pass. Status / Archived / Result
// are passed through unchanged so the renderer can colour each node without
// re-fetching from the daemon.
type Node struct {
	ID        string
	Name      string
	Status    string
	Archived  bool
	Result    string
	DependsOn []string
}

// Placed is a Node with its computed grid position. Col is the column within
// the layer (0-indexed left-to-right); Layer is the depth from the closest
// source.
type Placed struct {
	Node
	Layer int
	Col   int
}

// Edge represents a parent → child relationship in the laid-out graph. Layout
// emits one Edge per actual depends_on link; coordinates are grid cells, not
// pixel/cell positions — the renderer translates them.
type Edge struct {
	From string // parent task ID
	To   string // child task ID
}

// Layout is the materialised graph: every node in a deterministic order so
// rendering is reproducible across runs, plus the edge list and the grid
// dimensions for the renderer to size its viewport against.
type Layout struct {
	Nodes  []Placed
	Edges  []Edge
	Layers int // total number of layers
	Width  int // max columns in any single layer
}

// Compute lays out the supplied node set. Nodes referenced via DependsOn but
// not present in the set are silently dropped from the edge list — the daemon
// occasionally surfaces stale IDs (e.g. a deleted parent), and the DAG view
// should render the partial graph instead of refusing.
//
// Deterministic ordering: nodes within a layer are sorted by ID after the
// barycentric pass so two layouts of the same input produce identical output
// (test golden files would otherwise flake on map iteration order).
func Compute(nodes []Node) Layout {
	// Index by ID and prune unknown dep references upfront.
	index := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		index[n.ID] = n
	}
	cleaned := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		var deps []string
		for _, d := range n.DependsOn {
			if _, ok := index[d]; ok {
				deps = append(deps, d)
			}
		}
		cleaned[n.ID] = deps
	}

	// Children adjacency, used by both layer assignment (longest-path) and
	// barycentric ordering.
	children := make(map[string][]string, len(nodes))
	for child, parents := range cleaned {
		for _, p := range parents {
			children[p] = append(children[p], child)
		}
	}

	// Assign layer = max(parent.layer)+1 (sources at 0).
	layer := make(map[string]int, len(nodes))
	visiting := make(map[string]bool, len(nodes))
	var visit func(string) int
	visit = func(id string) int {
		if l, ok := layer[id]; ok {
			return l
		}
		if visiting[id] {
			// Cycle protection — should never fire because Compute is called
			// after the daemon's cycle check, but if a stale snapshot sneaks
			// one in we degrade to a flat layout rather than infinite-loop.
			return 0
		}
		visiting[id] = true
		max := -1
		for _, p := range cleaned[id] {
			if pl := visit(p); pl > max {
				max = pl
			}
		}
		visiting[id] = false
		l := max + 1
		layer[id] = l
		return l
	}
	for _, n := range nodes {
		visit(n.ID)
	}

	// Group by layer.
	totalLayers := 0
	for _, l := range layer {
		if l+1 > totalLayers {
			totalLayers = l + 1
		}
	}
	byLayer := make([][]string, totalLayers)
	for id, l := range layer {
		byLayer[l] = append(byLayer[l], id)
	}

	// Initial order within each layer: sort by ID for determinism, then
	// run two barycentric sweeps using parent columns from the previous
	// layer. Two sweeps is enough at Argus scale; the literature shows
	// returns diminish quickly.
	for l := range byLayer {
		stableSort(byLayer[l])
	}
	col := make(map[string]int, len(nodes))
	for l := 0; l < totalLayers; l++ {
		for i, id := range byLayer[l] {
			col[id] = i
		}
	}

	for sweep := 0; sweep < 2; sweep++ {
		for l := 1; l < totalLayers; l++ {
			byLayer[l] = sortByBarycenter(byLayer[l], cleaned, col)
			for i, id := range byLayer[l] {
				col[id] = i
			}
		}
	}

	// Final placement.
	out := Layout{Layers: totalLayers}
	for l := 0; l < totalLayers; l++ {
		if len(byLayer[l]) > out.Width {
			out.Width = len(byLayer[l])
		}
		for i, id := range byLayer[l] {
			out.Nodes = append(out.Nodes, Placed{
				Node:  index[id],
				Layer: l,
				Col:   i,
			})
		}
	}
	// Edges materialised after placement so they enumerate in node order.
	for _, p := range out.Nodes {
		for _, parent := range cleaned[p.ID] {
			out.Edges = append(out.Edges, Edge{From: parent, To: p.ID})
		}
	}
	return out
}

// sortByBarycenter reorders ids by the mean column of their parents. Nodes
// with no parents in the previous layer keep their relative position by ID.
func sortByBarycenter(ids []string, parents map[string][]string, col map[string]int) []string {
	type scored struct {
		id    string
		score float64
		hasP  bool
	}
	rows := make([]scored, len(ids))
	for i, id := range ids {
		var sum, n float64
		for _, p := range parents[id] {
			if c, ok := col[p]; ok {
				sum += float64(c)
				n++
			}
		}
		s := scored{id: id}
		if n > 0 {
			s.score = sum / n
			s.hasP = true
		}
		rows[i] = s
	}
	// Stable sort: parented nodes by score asc, parentless nodes by ID asc
	// (and they sort after all parented nodes to keep the layered look).
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			if !less(rows[j-1], rows[j]) {
				rows[j-1], rows[j] = rows[j], rows[j-1]
				continue
			}
			break
		}
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.id
	}
	return out
}

func less(a, b struct {
	id    string
	score float64
	hasP  bool
},
) bool {
	if a.hasP != b.hasP {
		return a.hasP
	}
	if a.hasP && a.score != b.score {
		return a.score < b.score
	}
	return a.id < b.id
}

// stableSort sorts a slice of IDs in place — deterministic ordering for the
// initial layer arrangement and ties in the barycentric pass.
func stableSort(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

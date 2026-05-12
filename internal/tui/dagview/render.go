package dagview

import (
	"strings"

	"github.com/gdamore/tcell/v2"
)

// Cell sizing — boxes are 3 rows tall and exposeBoxWidth columns wide.
// Layers are separated by 1 row of edge space. Within a layer, nodes are
// separated by colGap blank columns.
const (
	boxHeight   = 3
	boxWidth    = 18
	colGap      = 2
	rowGap      = 1
	cellRow     = boxHeight + rowGap // 4
	cellCol     = boxWidth + colGap  // 20
	labelMargin = 2                  // " name " inside the box
)

// CellAt returns the screen offset (col, row) within the rendered DAG for a
// placed node's top-left corner. Used by the widget for cursor hit-testing.
func CellAt(p Placed) (col, row int) {
	return p.Col * cellCol, p.Layer * cellRow
}

// buildNodeIndex builds a lookup table from task ID to Placed so the edge
// renderer can resolve endpoints in O(1) instead of O(N) per edge — saves
// O(N²) per draw at Argus scale (≤ 30 nodes ≈ ~900 ops, not catastrophic,
// but the index keeps the cost flat as stacks grow).
func buildNodeIndex(nodes []Placed) map[string]Placed {
	idx := make(map[string]Placed, len(nodes))
	for _, n := range nodes {
		idx[n.ID] = n
	}
	return idx
}

// boxMid returns the column halfway across a node's box — the anchor for
// vertical edges entering or leaving the node.
func boxMid(p Placed) int {
	col, _ := CellAt(p)
	return col + boxWidth/2
}

// Style yields the tcell.Style for a status / archived / failed combination.
// Archived overrides everything to a dim grey palette so retried stacks are
// still visible but visually demoted. Failed result (parsed by the widget,
// not the renderer) is rendered with a red border instead of an alternate
// background — we don't have a generic "highlight one face of the box".
func Style(status string, archived, failed bool, focused bool) tcell.Style {
	base := tcell.StyleDefault
	if archived {
		return base.Foreground(tcell.ColorGray).Dim(true)
	}
	if failed {
		return base.Foreground(tcell.ColorRed).Bold(true)
	}
	switch status {
	case "pending":
		return base.Foreground(tcell.ColorGray)
	case "in_progress":
		if focused {
			return base.Foreground(tcell.ColorAqua).Bold(true)
		}
		return base.Foreground(tcell.ColorAqua)
	case "in_review":
		return base.Foreground(tcell.ColorYellow)
	case "complete":
		return base.Foreground(tcell.ColorGreen)
	default:
		return base
	}
}

// StatusGlyph returns a one-rune indicator drawn inside the box. The renderer
// pastes it next to the truncated name. Mirrors the spinner indicators on
// the task list so users learn one vocabulary.
func StatusGlyph(status string, archived, failed bool) rune {
	switch {
	case archived:
		return '·'
	case failed:
		return '✕'
	case status == "complete":
		return '✓'
	case status == "in_review":
		return '⊙'
	case status == "in_progress":
		return '▶'
	default:
		return '○'
	}
}

// Draw paints the layout onto screen starting at offset (x0, y0). Returns
// the bounding box width/height actually rendered so callers can position
// scrollbars or status footers below.
//
// cursor is the ID of the highlighted node (empty = no highlight). focused
// reports whether the parent widget has keyboard focus — only affects the
// cursor's render, not other nodes.
//
// parseFailed reports per-node whether result indicates a failure; the
// widget computes this once when SetNodes is called rather than re-parsing
// JSON on every Draw.
func Draw(screen tcell.Screen, x0, y0 int, l Layout, cursor string, focused bool, parseFailed func(id string) bool) (w, h int) {
	if len(l.Nodes) == 0 {
		return 0, 0
	}
	// Draw nodes first so edges can be overlaid without bleeding into boxes.
	for _, p := range l.Nodes {
		failed := parseFailed != nil && parseFailed(p.ID)
		drawNode(screen, x0, y0, p, p.ID == cursor && focused, failed)
	}
	index := buildNodeIndex(l.Nodes)
	for _, e := range l.Edges {
		drawEdge(screen, x0, y0, index, e)
	}
	w = l.Width*cellCol - colGap
	h = l.Layers*cellRow - rowGap
	return w, h
}

// drawNode renders a single placed node's 3-row box at its grid position.
func drawNode(screen tcell.Screen, x0, y0 int, p Placed, highlight, failed bool) {
	col, row := CellAt(p)
	x := x0 + col
	y := y0 + row
	style := Style(p.Status, p.Archived, failed, highlight)
	if highlight {
		// Bold + reverse highlight to make the cursor obvious in dark terms.
		style = style.Reverse(true)
	}

	// Top border.
	screen.SetContent(x, y, '╭', nil, style)
	for i := 1; i < boxWidth-1; i++ {
		screen.SetContent(x+i, y, '─', nil, style)
	}
	screen.SetContent(x+boxWidth-1, y, '╮', nil, style)

	// Middle row: glyph + truncated name. Iterate over runes — `for _, r :=
	// range s` yields byte indices, which skip cells for multi-byte glyphs
	// like ✓ (3 bytes) and ✕ (3 bytes).
	screen.SetContent(x, y+1, '│', nil, style)
	runes := []rune(makeLabel(p, failed))
	maxRunes := boxWidth - labelMargin*2
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	for i, r := range runes {
		screen.SetContent(x+labelMargin+i, y+1, r, nil, style)
	}
	for i := labelMargin + len(runes); i < boxWidth-1; i++ {
		screen.SetContent(x+i, y+1, ' ', nil, style)
	}
	screen.SetContent(x+boxWidth-1, y+1, '│', nil, style)

	// Bottom border.
	screen.SetContent(x, y+2, '╰', nil, style)
	for i := 1; i < boxWidth-1; i++ {
		screen.SetContent(x+i, y+2, '─', nil, style)
	}
	screen.SetContent(x+boxWidth-1, y+2, '╯', nil, style)
}

// makeLabel composes the glyph + name string that goes inside a node's box.
// Name is truncated with an ellipsis so the box width is preserved.
// Width is measured in runes, not bytes — otherwise multi-byte glyphs would
// undercount and over-truncate.
func makeLabel(p Placed, failed bool) string {
	glyph := string(StatusGlyph(p.Status, p.Archived, failed))
	name := p.Name
	avail := boxWidth - labelMargin*2 - 2 // glyph + space + name budget
	if r := []rune(name); len(r) > avail {
		if avail > 1 {
			name = string(r[:avail-1]) + "…"
		} else {
			name = ""
		}
	}
	return glyph + " " + name
}

// drawEdge paints a single parent→child relationship using single-line box
// chars on the edge row between layers.
//
// Same column: a single `│` at midcol.
//
// Different columns: corner + horizontal + corner on the edge row. The
// corners visually bridge into the child's top border. The top-border cells
// of the child at the entering column remain `─`, which is a documented
// cosmetic limitation (the corner glyphs do not visually attach to the
// horizontal). See gotchas/dag-rendering.md.
func drawEdge(screen tcell.Screen, x0, y0 int, index map[string]Placed, e Edge) {
	parent, okP := index[e.From]
	child, okC := index[e.To]
	if !okP || !okC {
		return
	}
	parentMid := boxMid(parent)
	childMid := boxMid(child)
	edgeRow := parent.Layer*cellRow + boxHeight // first row below parent box

	style := tcell.StyleDefault.Foreground(tcell.ColorGray)

	if parentMid == childMid {
		screen.SetContent(x0+parentMid, y0+edgeRow, '│', nil, style)
		return
	}

	// Bent edge — corner + horizontal segment + corner on the same row.
	leftMid, rightMid := parentMid, childMid
	leftCorner, rightCorner := '╰', '╮'
	if parentMid > childMid {
		leftMid, rightMid = childMid, parentMid
		leftCorner, rightCorner = '╭', '╯'
	}
	// Left corner.
	screen.SetContent(x0+leftMid, y0+edgeRow, leftCorner, nil, style)
	// Horizontal in between.
	for c := leftMid + 1; c < rightMid; c++ {
		screen.SetContent(x0+c, y0+edgeRow, '─', nil, style)
	}
	// Right corner.
	screen.SetContent(x0+rightMid, y0+edgeRow, rightCorner, nil, style)
}

// RenderToString paints the layout into a string grid for golden-file tests.
// Status colours are dropped (golden files would be unreadable). cursor is
// stamped as a `*` on the node it identifies; the renderer otherwise uses
// the same glyph + name logic as the live Draw path.
func RenderToString(l Layout, cursor string, parseFailed func(id string) bool) string {
	if len(l.Nodes) == 0 {
		return ""
	}
	width := l.Width*cellCol - colGap
	height := l.Layers*cellRow - rowGap
	grid := make([][]rune, height)
	for i := range grid {
		row := make([]rune, width)
		for j := range row {
			row[j] = ' '
		}
		grid[i] = row
	}
	setCell := func(c, r int, ch rune) {
		if r < 0 || r >= height || c < 0 || c >= width {
			return
		}
		grid[r][c] = ch
	}

	for _, p := range l.Nodes {
		col, row := CellAt(p)
		failed := parseFailed != nil && parseFailed(p.ID)
		// Top border.
		setCell(col, row, '╭')
		for i := 1; i < boxWidth-1; i++ {
			setCell(col+i, row, '─')
		}
		setCell(col+boxWidth-1, row, '╮')
		// Middle row. Cursor prefix is added BEFORE the rune-count clamp so
		// the `*` does not push the truncation boundary past the right
		// border. Without the unified clamp, the cursor marker on a
		// max-width label could write 1 cell past the box edge.
		setCell(col, row+1, '│')
		label := makeLabel(p, failed)
		if p.ID == cursor {
			label = "*" + label
		}
		runes := []rune(label)
		maxRunes := boxWidth - labelMargin*2
		if len(runes) > maxRunes {
			runes = runes[:maxRunes]
		}
		for i, r := range runes {
			setCell(col+labelMargin+i, row+1, r)
		}
		setCell(col+boxWidth-1, row+1, '│')
		// Bottom border.
		setCell(col, row+2, '╰')
		for i := 1; i < boxWidth-1; i++ {
			setCell(col+i, row+2, '─')
		}
		setCell(col+boxWidth-1, row+2, '╯')
	}
	// Overlay edges. Build the lookup once per render so this scales
	// linearly in edge count, not quadratically.
	index := buildNodeIndex(l.Nodes)
	for _, e := range l.Edges {
		parent, okP := index[e.From]
		child, okC := index[e.To]
		if !okP || !okC {
			continue
		}
		pm, cm := boxMid(parent), boxMid(child)
		er := parent.Layer*cellRow + boxHeight
		if pm == cm {
			setCell(pm, er, '│')
			continue
		}
		lm, rm := pm, cm
		lc, rc := '╰', '╮'
		if pm > cm {
			lm, rm = cm, pm
			lc, rc = '╭', '╯'
		}
		setCell(lm, er, lc)
		for c := lm + 1; c < rm; c++ {
			setCell(c, er, '─')
		}
		setCell(rm, er, rc)
	}

	var b strings.Builder
	for i, row := range grid {
		b.WriteString(strings.TrimRight(string(row), " "))
		if i < len(grid)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

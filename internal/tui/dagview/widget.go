package dagview

import (
	"encoding/json"
	"math"

	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Widget renders the DAG in a bordered panel. Owns the placed layout, the
// cursor, and the per-node failed-result cache; recomputes layout only when
// SetNodes is called (cheap at Argus scale).
type Widget struct {
	*tview.Box
	layout   Layout
	cursor   string
	focused  bool
	failed   map[string]bool
	OnClick  func() // optional; fires when the widget gains focus via click
	OnEnter  func(id string)
	OnLink   func(child string)
	OnUnlink func(child string)
	OnHalt   func(id string)
	OnFocus  func() // setFocus hook the page uses on mouse click

	// OnBranchChange fires when Draw will paint a structurally different
	// frame than the previous one (node count, layer count, edge count, or
	// node-status set). App wires this to forceRedraw, which is now a
	// log-only debug signal (does NOT trigger Sync) — tcell.Show()'s
	// per-cell diff plus tview.Clear() handle the rendering. See
	// gotchas/ui-threading.md and gotchas/dag-rendering.md.
	OnBranchChange func()
	lastShape      uint64
}

// New constructs an empty DAG widget. SetNodes must be called before the
// widget is meaningful.
func New() *Widget {
	return &Widget{
		Box:    tview.NewBox(),
		failed: map[string]bool{},
	}
}

// SetNodes installs a new snapshot. Recomputes layout, repopulates the
// failed-result cache, and clamps the cursor to the new node set.
func (w *Widget) SetNodes(nodes []Node) {
	w.layout = Compute(nodes)
	w.failed = make(map[string]bool, len(nodes))
	for _, n := range nodes {
		w.failed[n.ID] = parseFailed(n.Result)
	}
	// Cursor clamp — if the highlighted ID is no longer present, jump to
	// the first node (or empty if the graph is empty).
	if _, ok := w.findNode(w.cursor); !ok {
		if len(w.layout.Nodes) > 0 {
			w.cursor = w.layout.Nodes[0].ID
		} else {
			w.cursor = ""
		}
	}
	w.maybeNotifyBranchChange()
}

// SetFocused toggles the focus state. Bubble up to Draw, which renders the
// cursor more prominently when the widget owns focus.
func (w *Widget) SetFocused(f bool) {
	if w.focused == f {
		return
	}
	w.focused = f
	w.maybeNotifyBranchChange()
}

// CurrentTask returns the highlighted task ID, or "" when the DAG is empty.
func (w *Widget) CurrentTask() string {
	return w.cursor
}

// MoveCursor relocates the cursor by grid deltas: dx moves within a layer,
// dy moves between layers. Movement is clamped — going off the right edge
// of a layer or below the last layer is a no-op rather than wrapping.
//
// Movement treats every node in `layout.Nodes` uniformly — filtering
// (archived, orphan, etc.) is the caller's responsibility. The TUI's
// `dagNodesFromTasks` drops archived rows before `SetNodes`, so in
// practice archived nodes never reach this code path from the TUI.
//
// Fires maybeNotifyBranchChange when the cursor actually changes. The
// branch-change contract is mandatory for this widget: the cursor box
// renders with reverse+bold highlight, so a move shifts which cell set
// gets the highlighted style. Without the callback, tcell's per-cell diff
// would leave the previous highlight on screen as a ghost — visible under
// tmux/iTerm2 on cursor navigation.
func (w *Widget) MoveCursor(dx, dy int) {
	cur, ok := w.findNode(w.cursor)
	if !ok {
		if len(w.layout.Nodes) > 0 {
			w.cursor = w.layout.Nodes[0].ID
			w.maybeNotifyBranchChange()
		}
		return
	}
	targetLayer := cur.Layer + dy
	targetCol := cur.Col + dx
	if targetLayer < 0 || targetLayer >= w.layout.Layers {
		return
	}
	// Find the closest node in the target layer at or near targetCol.
	// Seed with MaxInt32 so the first candidate's distance is always smaller.
	var best Placed
	bestDist := math.MaxInt32
	found := false
	for _, p := range w.layout.Nodes {
		if p.Layer != targetLayer {
			continue
		}
		d := p.Col - targetCol
		if d < 0 {
			d = -d
		}
		if d < bestDist {
			bestDist = d
			best = p
			found = true
		}
	}
	if !found {
		return
	}
	if best.ID == w.cursor {
		return
	}
	w.cursor = best.ID
	w.maybeNotifyBranchChange()
}

// Draw paints the layout, plus a header banner and a key-hints footer.
func (w *Widget) Draw(screen tcell.Screen) {
	w.DrawForSubclass(screen, w)
	x, y, wpx, hpx := w.GetInnerRect()
	if wpx <= 0 || hpx <= 0 {
		return
	}
	borderStyle := theme.StyleBorder
	if w.focused {
		borderStyle = theme.StyleFocusedBorder
	}
	inner := widget.DrawBorderedPanel(screen, x, y, wpx, hpx, " DAG ", borderStyle)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}

	if len(w.layout.Nodes) == 0 {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "No tasks in DAG. Link tasks with `l` from the task list.", theme.StyleDimmed)
		return
	}

	failedFn := func(id string) bool { return w.failed[id] }
	Draw(screen, inner.X, inner.Y, w.layout, w.cursor, w.focused, failedFn)
}

// InputHandler routes hjkl / arrow keys to MoveCursor and dispatches Enter
// / l / L / h to the corresponding callbacks. Unknown keys are passed
// through to the default tview.Box handler, which is a no-op.
func (w *Widget) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return w.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyUp:
			w.MoveCursor(0, -1)
		case tcell.KeyDown:
			w.MoveCursor(0, 1)
		case tcell.KeyLeft:
			w.MoveCursor(-1, 0)
		case tcell.KeyRight:
			w.MoveCursor(1, 0)
		case tcell.KeyEnter:
			if w.OnEnter != nil && w.cursor != "" {
				w.OnEnter(w.cursor)
			}
		case tcell.KeyRune:
			switch event.Rune() {
			case 'h':
				if w.OnHalt != nil && w.cursor != "" {
					w.OnHalt(w.cursor)
				}
			case 'j':
				w.MoveCursor(0, 1)
			case 'k':
				w.MoveCursor(0, -1)
			case 'l':
				if w.OnLink != nil && w.cursor != "" {
					w.OnLink(w.cursor)
				}
			case 'L':
				if w.OnUnlink != nil && w.cursor != "" {
					w.OnUnlink(w.cursor)
				}
			}
		}
	})
}

// MouseHandler positions the cursor on the clicked node and yields focus to
// the widget. Scroll wheel moves the cursor by one row.
func (w *Widget) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
	return w.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (consumed bool, capture tview.Primitive) {
		if !w.InRect(event.Position()) {
			return false, nil
		}
		if action == tview.MouseLeftDown || action == tview.MouseLeftClick {
			setFocus(w)
			consumed = true
			if w.OnClick != nil {
				w.OnClick()
			}
			// Map screen click to grid cell.
			ix, iy, _, _ := w.GetInnerRect()
			ex, ey := event.Position()
			// Adjust for the bordered panel inset (1 cell).
			relX := ex - ix - 1
			relY := ey - iy - 1
			if relX < 0 || relY < 0 {
				return
			}
			col := relX / cellCol
			layer := relY / cellRow
			prevCursor := w.cursor
			for _, p := range w.layout.Nodes {
				if p.Col == col && p.Layer == layer {
					w.cursor = p.ID
					break
				}
			}
			// Mouse path mirrors MoveCursor's branch-change contract:
			// shifting the highlighted cell set without notifying would
			// leave the previous reverse+bold box on screen as a ghost
			// (same class of bug as the keyboard path before iter-2).
			if w.cursor != prevCursor {
				w.maybeNotifyBranchChange()
			}
		}
		if action == tview.MouseScrollUp {
			w.MoveCursor(0, -1)
			consumed = true
		}
		if action == tview.MouseScrollDown {
			w.MoveCursor(0, 1)
			consumed = true
		}
		return
	})
}

// PasteHandler is a no-op for the DAG widget — pasted text has nothing
// sensible to do here. Implementing the interface keeps tview's bracket
// paste from leaking to a parent widget that might consume it badly.
func (w *Widget) PasteHandler() func(text string, setFocus func(p tview.Primitive)) {
	return w.WrapPasteHandler(func(_ string, _ func(p tview.Primitive)) {})
}

// findNode resolves an ID to its placed entry. Returns the empty Placed and
// false if the ID is unknown (e.g. after archive removed it).
func (w *Widget) findNode(id string) (Placed, bool) {
	for _, p := range w.layout.Nodes {
		if p.ID == id {
			return p, true
		}
	}
	return Placed{}, false
}

// parseFailed reads the agent-supplied result blob and reports whether the
// agent set `failed: true`. The widget calls this once per snapshot rather
// than every Draw — the JSON is opaque to the daemon but cheap to parse on
// the UI side.
func parseFailed(raw string) bool {
	if raw == "" {
		return false
	}
	var r struct {
		Failed bool `json:"failed"`
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return false
	}
	return r.Failed
}

// branchShape captures the parts of state that, when changed, mean Draw
// would paint a structurally different cell set. The widget compares this
// signature across SetNodes / SetFocused / MoveCursor to decide whether to
// fire OnBranchChange — purely as a debug-trail signal; forceRedraw is
// log-only post-May-2026. See gotchas/ui-threading.md.
//
// The cursor field is folded into the signature via FNV-1a over the cursor
// task ID so a move between two non-empty cursors (same node count, same
// status, same focus) still produces a different shape and fires the
// callback. The failed-count bit field counts `true` entries in the failed
// map, NOT the map size — that catches a node flipping not-failed → failed
// (the red border + ✕ glyph swap) without any other change.
func (w *Widget) branchShape() uint64 {
	// node count : 24, layer count : 8, edge count : 12, focus : 1,
	// failed-true count : 12, in-progress count : 6,
	// cursor hash : low 32 bits XOR-folded into the result
	var nProg, nFailed int
	for _, p := range w.layout.Nodes {
		if p.Status == "in_progress" {
			nProg++
		}
		if w.failed[p.ID] {
			nFailed++
		}
	}
	// Mask THEN cast — int & literal yields a small positive int that fits
	// in uint64 without the gosec G115 overflow warning. These fields are
	// slice lengths or longest-path layer counts, always ≥ 0 in practice;
	// the mask both clamps to the bit field width and makes the unsigned
	// semantics explicit.
	var shape uint64
	shape |= uint64(len(w.layout.Nodes) & 0xFFFFFF)
	shape |= uint64(w.layout.Layers&0xFF) << 24
	shape |= uint64(len(w.layout.Edges)&0xFFF) << 32
	if w.focused {
		shape |= 1 << 44
	}
	shape |= uint64(nFailed&0xFFF) << 45
	shape |= uint64(nProg&0x3F) << 58
	// Fold a cheap FNV-1a hash of the cursor string into the shape — this
	// is what distinguishes "cursor on A" from "cursor on B". Cursor empty
	// vs non-empty also differs (empty → hash of "" = FNV offset basis).
	shape ^= fnv1aHash(w.cursor)
	return shape
}

// fnv1aHash is a tiny FNV-1a (32-bit) implementation. Used to fold the
// cursor task ID into branchShape without pulling in `hash/fnv`. The hash
// quality is well above the noise floor for this purpose (distinguishing
// different cursor IDs); we are not hashing untrusted input.
func fnv1aHash(s string) uint64 {
	const (
		offset uint32 = 2166136261
		prime  uint32 = 16777619
	)
	h := offset
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= prime
	}
	return uint64(h)
}

func (w *Widget) maybeNotifyBranchChange() {
	shape := w.branchShape()
	if shape == w.lastShape {
		return
	}
	w.lastShape = shape
	if w.OnBranchChange != nil {
		w.OnBranchChange()
	}
}

// Package planview is the TUI widget that renders a Hera *plan DAG* — planned
// and live worker roles as nodes, hera_blocks blocking edges between them — as
// the "tight tree" UX (short-id labels, auto-collapsing parallel groups,
// 4-way cursor navigation, a master-detail header, and sub-coordinator
// drill-in). It replaces the orchestration-tree graph (heraTreeNodes) in the
// Hera Details pane and reuses dagview's Kahn longest-path layer math for stage
// placement. See openspec/changes/add-hera-plan-view/design.md.
//
// Stage 3 (layout + render) is implemented here: short-id parse + fallback,
// edge-driven stage placement (via dagview.Compute), parallel-group detection
// and collapse, chip glyph/colour, the partial-dependency marker, and the
// degenerate no-plan flat stage. The (stage, slot, member) cursor navigation
// (Stage 4), the master-detail header (Stage 5), and sub-coordinator drill-in
// (Stage 6) remain signature-only stubs.
package planview

import (
	"fmt"
	"sort"
	"strings"

	"github.com/drn/argus/internal/tui/dagview"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/drn/argus/internal/uxlog"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// State is a plan node's render state, sourced from the bound argus task
// (for a live node) or the planned discriminator (for a never-bound node).
// It drives the glyph + colour (D7): done ✓ green, working ⟳ amber, in_review
// ◔ cyan, planned ○ violet, failed ✕ red.
type State int

const (
	// StatePlanned is a never-bound worker role (violet ○).
	StatePlanned State = iota
	// StateWorking is a live, in_progress node (amber ⟳).
	StateWorking
	// StateInReview is a live, in_review node (cyan ◔).
	StateInReview
	// StateDone is a complete node (green ✓).
	StateDone
	// StateFailed is a node whose result reported failure (red ✕).
	StateFailed
	// StatePending is a live-but-not-yet-progressing node.
	StatePending
)

// Glyph returns the one-rune state indicator drawn inside a chip.
func (s State) Glyph() rune {
	switch s {
	case StateDone:
		return '✓'
	case StateWorking:
		return '⟳'
	case StateInReview:
		return '◔'
	case StateFailed:
		return '✕'
	case StatePending:
		return '·'
	default: // StatePlanned
		return '○'
	}
}

// style yields the tcell.Style for a state (D7 palette). Reuses the theme
// colours where they line up with the artifact; planned is violet.
func (s State) style() tcell.Style {
	base := tcell.StyleDefault
	switch s {
	case StateDone:
		return base.Foreground(theme.ColorComplete)
	case StateWorking:
		return base.Foreground(theme.ColorInProgress)
	case StateInReview:
		return base.Foreground(theme.ColorInReview)
	case StateFailed:
		return base.Foreground(theme.ColorError).Bold(true)
	case StatePending:
		return base.Foreground(theme.ColorPending)
	default: // StatePlanned — violet
		return base.Foreground(colorPlanned)
	}
}

// colorPlanned is the violet planned-node colour (matches the PR-awaiting
// purple already in the palette so the TUI keeps one violet vocabulary).
var colorPlanned = theme.ColorPRAwaiting

// Node is the input projection for one plan node. Name is the full role name
// (the short-id is parsed from its prefix at layout time, D3); ID is the stable
// identity used for edges, cursor tracking, and the OnEnter jump (the role's
// bound task ID for a live node, or a synthetic planned-role key otherwise).
type Node struct {
	// ID is the node's stable identity for edges and cursor tracking. For a live
	// node it is the bound argus task ID (so OnEnter can jump to its agent view);
	// for a planned node it is a synthetic key derived from the role id.
	ID string
	// Name is the full role name; the short-id label is parsed from its prefix.
	Name string
	// State drives the glyph + colour.
	State State
	// Planned marks a never-bound worker role (worker-kind, !Live, BridgeTaskID=="").
	Planned bool
	// Drillable marks a node whose bound task is the coordinator of a child
	// orchestrator (a sub-coordinator); Enter drills in rather than jumping.
	Drillable bool
	// Description is the role's delivery-prompt first line (the header "Description").
	Description string
}

// Edge is one directed blocking dependency in the plan: To waits on From (To is
// blocked by From). Endpoints reference Node.ID. This is the same direction
// dagview consumes (From=parent/blocker, To=child/blocked) so the layer math
// transfers directly.
type Edge struct {
	From string // blocker node ID (the dependency)
	To   string // blocked node ID (depends on From)
}

// ShortID is the parsed short-id of a role name: the prefix up to the first
// '-', split into the leading-digit Stage and trailing-letter Member. When the
// name has no parseable short-id prefix, OK is false and Label falls back to the
// truncated name (D3).
type ShortID struct {
	Stage  int    // leading digits (e.g. 2 in "2c")
	Member string // trailing letters (e.g. "c" in "2c")
	Label  string // the display label ("2c", or the truncated name on fallback)
	OK     bool   // true when the prefix parsed as <digits><letters>
}

// fallbackLabelRunes caps the truncated-name fallback label width (rune count).
const fallbackLabelRunes = 12

// ParseShortID parses a role name's short-id prefix (`2c-fact-checker` → {Stage:2,
// Member:"c", Label:"2c", OK:true}). A name with no parseable prefix yields a
// truncated-name Label with OK false. The short-id is presentation only; it
// never drives layout (D3).
func ParseShortID(name string) ShortID {
	// The short-id is the prefix up to the first '-'.
	prefix := name
	if i := strings.IndexByte(name, '-'); i >= 0 {
		prefix = name[:i]
	}
	// A valid short-id is <digits><letters>: leading ASCII digits then trailing
	// ASCII letters, nothing else.
	digits, letters := 0, 0
	for digits < len(prefix) && prefix[digits] >= '0' && prefix[digits] <= '9' {
		digits++
	}
	for i := digits; i < len(prefix); i++ {
		c := prefix[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			letters++
			continue
		}
		// A non-letter after the digits disqualifies the prefix.
		letters = -1
		break
	}
	if digits == 0 || letters <= 0 || digits+letters != len(prefix) {
		return ShortID{Label: truncateLabel(name), OK: false}
	}
	stage := 0
	for i := 0; i < digits; i++ {
		stage = stage*10 + int(prefix[i]-'0')
	}
	member := prefix[digits:]
	return ShortID{Stage: stage, Member: member, Label: prefix, OK: true}
}

// truncateLabel clamps a fallback label to fallbackLabelRunes runes (rune-aware
// so multibyte names don't over-truncate), appending an ellipsis when clipped.
func truncateLabel(name string) string {
	r := []rune(name)
	if len(r) <= fallbackLabelRunes {
		return name
	}
	if fallbackLabelRunes <= 1 {
		return string(r[:fallbackLabelRunes])
	}
	return string(r[:fallbackLabelRunes-1]) + "…"
}

// Group is a collapsed parallel group: a maximal set of same-stage nodes that
// share a blocker set and have no internal edges (D4). It renders as a range
// box [first–last] (or [first–last +N] when non-contiguous) with aggregate
// state counts.
type Group struct {
	// Members are the node IDs in the group, sorted by short-id.
	Members []string
	// Stage is the computed longest-path stage shared by all members.
	Stage int
	// Label is the collapsed range box label ("[2a–2c]" / "[2a–2f +1]").
	Label string
	// Counts is the per-state aggregate (e.g. StateDone→3).
	Counts map[State]int
	// PartialFeed is true when only some members feed a downstream node (D5);
	// the box carries a ↘ marker and the feeding member is marked on fan-out.
	PartialFeed bool
	// FeedingMember is the node ID of the single downstream-feeding member when
	// PartialFeed (the chip that carries ↘ on fan-out).
	FeedingMember string
}

// slot is one rendered column in a stage: either a single lone node (group nil,
// nodeID set) or a collapsed parallel group (group set, nodeID "").
type slot struct {
	nodeID string // set when the slot is a lone node
	group  *Group // set when the slot is a collapsed group
}

// Widget renders the plan DAG in a bordered panel with a master-detail header
// strip above the diagram. It owns the projected node/edge set, the computed
// layout (stages + parallel groups), the (stage, slot, member) cursor, the
// per-group fan-out state, and the orchestrator drill-in nav stack.
type Widget struct {
	*tview.Box

	// OnEnter fires on Enter over a plain leaf node with that node's ID — the
	// jump-to-agent-view behaviour carried over from dagview. Group fan-out and
	// sub-coordinator drill-in are handled internally and do NOT fire OnEnter.
	OnEnter func(id string)
	// OnDrillIn fires when Enter drills into a sub-coordinator node, with that
	// node's ID; the page re-projects the child orchestrator's plan and pushes it.
	OnDrillIn func(id string)
	// OnDrillOut fires when Esc pops back to the parent orchestrator's plan.
	OnDrillOut func()
	// OnClick is the setFocus hook the page uses on a mouse click.
	OnClick func()
	// OnBranchChange fires when Draw will paint a structurally different frame
	// (node/edge/stage count, cursor, fanned group, or current orchestrator).
	// Log-only (no Sync), mirroring dagview's contract.
	OnBranchChange func()

	// nodes is the current snapshot keyed by ID.
	nodes   map[string]Node
	order   []string // node IDs in projection order (deterministic)
	edges   []Edge
	stageOf map[string]int // computed longest-path stage per node ID
	labelOf map[string]string
	stages  [][]slot // stages[stage] = ordered slots
	noPlan  bool
	title   string
	focused bool

	lastShape uint64
}

// New constructs an empty plan-view widget. SetData must be called before the
// widget is meaningful.
func New() *Widget {
	return &Widget{
		Box:     tview.NewBox(),
		nodes:   map[string]Node{},
		stageOf: map[string]int{},
		labelOf: map[string]string{},
		title:   " Plan ",
	}
}

// SetTitle overrides the bordered-panel title (the Hera Details pane sets it to
// " Plan "). Pass "" to suppress the title text.
func (w *Widget) SetTitle(title string) { w.title = title }

// SetData installs a new plan snapshot: the node set plus the blocking edges.
// Recomputes the stage layout (Kahn longest-path over the edges, D3), detects
// parallel groups (D4), and clamps the cursor to the new node set.
func (w *Widget) SetData(nodes []Node, edges []Edge) {
	w.nodes = make(map[string]Node, len(nodes))
	w.order = w.order[:0]
	for _, n := range nodes {
		w.nodes[n.ID] = n
		w.order = append(w.order, n.ID)
	}
	w.edges = edges

	// No plan authored: no planned nodes and no edges (D1). Render every node as
	// one flat edgeless stage.
	w.noPlan = !hasPlan(nodes, edges)

	w.computeStages(nodes, edges)
	w.computeLabels(nodes)
	w.buildSlots(nodes, edges)

	uxlog.Log("[planview] SetData: nodes=%d edges=%d stages=%d noPlan=%v",
		len(nodes), len(edges), len(w.stages), w.noPlan)
	w.maybeNotifyBranchChange()
}

// hasPlan reports whether a snapshot has an authored plan: any planned node or
// any blocking edge. The degenerate case (no plan) is its negation (D1).
func hasPlan(nodes []Node, edges []Edge) bool {
	if len(edges) > 0 {
		return true
	}
	for _, n := range nodes {
		if n.Planned {
			return true
		}
	}
	return false
}

// computeStages assigns each node its computed longest-path stage. When no plan
// is authored every node collapses to stage 0 (D1); otherwise stages come from
// dagview's Kahn longest-path layering over the blocking edges (D3).
func (w *Widget) computeStages(nodes []Node, edges []Edge) {
	w.stageOf = make(map[string]int, len(nodes))
	if w.noPlan {
		for _, n := range nodes {
			w.stageOf[n.ID] = 0
		}
		return
	}
	// Build dagview Nodes with DependsOn = blockers (To depends on From).
	blockers := make(map[string][]string, len(nodes))
	for _, e := range edges {
		blockers[e.To] = append(blockers[e.To], e.From)
	}
	dn := make([]dagview.Node, 0, len(nodes))
	for _, n := range nodes {
		dn = append(dn, dagview.Node{ID: n.ID, Name: n.Name, DependsOn: blockers[n.ID]})
	}
	layout := dagview.Compute(dn)
	for _, p := range layout.Nodes {
		w.stageOf[p.ID] = p.Layer
	}
}

// computeLabels caches each node's chip label: its short-id, or the truncated
// name fallback (D3); drillable nodes carry a ▸ marker (D6) so the gesture is
// discoverable even at Stage 3 render time.
func (w *Widget) computeLabels(nodes []Node) {
	w.labelOf = make(map[string]string, len(nodes))
	for _, n := range nodes {
		label := ParseShortID(n.Name).Label
		if n.Drillable {
			label += "▸"
		}
		w.labelOf[n.ID] = label
	}
}

// Stages returns the number of computed stages (longest-path layers) in the
// current layout. 0 when empty.
func (w *Widget) Stages() int { return len(w.stages) }

// NoPlan reports whether the current snapshot has no plan authored (no planned
// nodes and no edges) — the degenerate flat-single-stage render (D1).
func (w *Widget) NoPlan() bool { return w.noPlan }

// StageOf returns the computed longest-path stage of a node by ID, and whether
// the node is present. Layout truth (edge-driven), independent of the short-id
// number (D3).
func (w *Widget) StageOf(id string) (int, bool) {
	s, ok := w.stageOf[id]
	return s, ok
}

// LabelOf returns the rendered chip label for a node by ID (its short-id, or
// the truncated-name fallback). Empty when the node is absent.
func (w *Widget) LabelOf(id string) string { return w.labelOf[id] }

// GroupAt returns the collapsed parallel group occupying (stage, slot), and
// whether that slot is a group (vs a lone node).
func (w *Widget) GroupAt(stage, slot int) (Group, bool) {
	if stage < 0 || stage >= len(w.stages) {
		return Group{}, false
	}
	st := w.stages[stage]
	if slot < 0 || slot >= len(st) {
		return Group{}, false
	}
	if st[slot].group == nil {
		return Group{}, false
	}
	return *st[slot].group, true
}

// SlotCount returns the number of slots (lone nodes + collapsed groups) in a
// stage.
func (w *Widget) SlotCount(stage int) int {
	if stage < 0 || stage >= len(w.stages) {
		return 0
	}
	return len(w.stages[stage])
}

// SetFocused toggles keyboard focus (the cursor renders more prominently when
// the widget owns focus).
func (w *Widget) SetFocused(f bool) {
	if w.focused == f {
		return
	}
	w.focused = f
	w.maybeNotifyBranchChange()
}

// Title returns the bordered-panel title for the currently-displayed
// orchestrator (Stage 6 reflects drill-in here).
func (w *Widget) Title() string { return w.title }

// buildSlots groups each stage's nodes into slots: maximal parallel groups
// collapse to one slot, everything else is a lone-node slot (D4).
func (w *Widget) buildSlots(nodes []Node, edges []Edge) {
	if len(nodes) == 0 {
		w.stages = nil
		return
	}
	total := 0
	for _, s := range w.stageOf {
		if s+1 > total {
			total = s + 1
		}
	}
	// Blocker set per node (sorted for stable comparison) and the internal-edge
	// adjacency used to forbid edges inside a group.
	blockerSet := make(map[string][]string, len(nodes))
	for _, e := range edges {
		blockerSet[e.To] = append(blockerSet[e.To], e.From)
	}
	for id := range blockerSet {
		sort.Strings(blockerSet[id])
	}
	// internalEdge[a][b] true when an edge connects a and b (either direction);
	// members of a group must have no edges among themselves.
	connected := make(map[string]map[string]bool, len(nodes))
	for _, e := range edges {
		if connected[e.From] == nil {
			connected[e.From] = map[string]bool{}
		}
		if connected[e.To] == nil {
			connected[e.To] = map[string]bool{}
		}
		connected[e.From][e.To] = true
		connected[e.To][e.From] = true
	}

	byStage := make([][]string, total)
	for _, n := range nodes {
		s := w.stageOf[n.ID]
		byStage[s] = append(byStage[s], n.ID)
	}

	w.stages = make([][]slot, total)
	for s := 0; s < total; s++ {
		ids := byStage[s]
		w.sortByShortID(ids)
		if w.noPlan {
			// Degenerate case (D1): live roles render as one flat edgeless stage
			// of individual chips — never collapsed into a group.
			lone := make([]slot, 0, len(ids))
			for _, id := range ids {
				lone = append(lone, slot{nodeID: id})
			}
			w.stages[s] = lone
			continue
		}
		w.stages[s] = w.slotsForStage(ids, blockerSet, connected, edges)
	}
}

// slotsForStage partitions a stage's node IDs into slots. A maximal set of nodes
// sharing the same blocker set with no internal edges collapses into one group
// slot; everything else is a lone-node slot. A single-node group is not a group.
func (w *Widget) slotsForStage(ids []string, blockerSet map[string][]string, connected map[string]map[string]bool, edges []Edge) []slot {
	// Partition by identical blocker-set key.
	keyOf := func(id string) string { return strings.Join(blockerSet[id], "\x00") }
	byKey := map[string][]string{}
	var keyOrder []string
	for _, id := range ids {
		k := keyOf(id)
		if _, seen := byKey[k]; !seen {
			keyOrder = append(keyOrder, k)
		}
		byKey[k] = append(byKey[k], id)
	}
	var out []slot
	for _, k := range keyOrder {
		members := byKey[k]
		// A clean group needs ≥2 members with no edges among themselves.
		if len(members) >= 2 && noInternalEdges(members, connected) {
			out = append(out, slot{group: w.buildGroup(members, edges)})
			continue
		}
		// Otherwise each member is its own lone-node slot.
		for _, id := range members {
			out = append(out, slot{nodeID: id})
		}
	}
	return out
}

// noInternalEdges reports whether no edge connects any two members.
func noInternalEdges(members []string, connected map[string]map[string]bool) bool {
	set := make(map[string]bool, len(members))
	for _, m := range members {
		set[m] = true
	}
	for _, m := range members {
		for nb := range connected[m] {
			if set[nb] {
				return false
			}
		}
	}
	return true
}

// buildGroup collapses members into a Group: range-box label, aggregate state
// counts, and the partial-feed marker (D4/D5). Members are already short-id
// sorted by the caller.
func (w *Widget) buildGroup(members []string, edges []Edge) *Group {
	g := &Group{Members: append([]string(nil), members...), Counts: map[State]int{}}
	g.Stage = w.stageOf[members[0]]
	for _, m := range members {
		g.Counts[w.nodes[m].State]++
	}
	g.Label = w.groupLabel(members)

	// Partial-feed (D5): a downstream-feeding member is one with an outgoing
	// edge to a node outside this group's stage (a later stage). If only some
	// members feed downstream, mark the group and record the single feeder.
	memberSet := make(map[string]bool, len(members))
	for _, m := range members {
		memberSet[m] = true
	}
	var feeders []string
	for _, m := range members {
		for _, e := range edges {
			if e.From == m && !memberSet[e.To] {
				feeders = append(feeders, m)
				break
			}
		}
	}
	if len(feeders) > 0 && len(feeders) < len(members) {
		g.PartialFeed = true
		g.FeedingMember = feeders[0]
		// The ↘ goes inside the box per the spec ("[2a–2c ↘]").
		g.Label = bracketWithMarker(g.Label)
	}
	return g
}

// groupLabel renders the [first–last] / [first–last +N] range box label (D4).
// N counts members beyond the two span endpoints when the labels are
// non-contiguous (a gap exists between first and last in the member sequence).
func (w *Widget) groupLabel(members []string) string {
	if len(members) == 0 {
		return "[]"
	}
	first := w.LabelOf(members[0])
	last := w.LabelOf(members[len(members)-1])
	extra := len(members) - 2 // members beyond the two endpoints
	if extra > 0 && !contiguous(members, w) {
		return fmt.Sprintf("[%s–%s +%d]", first, last, extra)
	}
	return fmt.Sprintf("[%s–%s]", first, last)
}

// bracketWithMarker inserts the ↘ inside the closing bracket of a range-box
// label, e.g. "[2a–2c]" → "[2a–2c ↘]" / "[2a–2f +1]" → "[2a–2f +1 ↘]".
func bracketWithMarker(label string) string {
	if strings.HasSuffix(label, "]") {
		return label[:len(label)-1] + " ↘]"
	}
	return label + " ↘"
}

// contiguous reports whether the members' parsed short-id members form a
// contiguous run within their shared stage (e.g. a,b,c is contiguous; a,b,f is
// not). Members with unparseable short-ids are treated as non-contiguous so the
// +N count surfaces them honestly.
func contiguous(members []string, w *Widget) bool {
	if len(members) <= 1 {
		return true
	}
	// Build the single-letter member sequence; multi-letter or unparseable
	// members force non-contiguous (we can't prove a clean run).
	letters := make([]rune, 0, len(members))
	for _, id := range members {
		sid := ParseShortID(w.nodes[id].Name)
		if !sid.OK || len([]rune(sid.Member)) != 1 {
			return false
		}
		letters = append(letters, []rune(sid.Member)[0])
	}
	for i := 1; i < len(letters); i++ {
		if letters[i] != letters[i-1]+1 {
			return false
		}
	}
	return true
}

// sortByShortID orders a stage's node IDs by parsed short-id (stage then
// member), falling back to the raw name for unparseable ids, so groups and
// chips render left-to-right in a stable, human order.
func (w *Widget) sortByShortID(ids []string) {
	sort.SliceStable(ids, func(i, j int) bool {
		a := ParseShortID(w.nodes[ids[i]].Name)
		b := ParseShortID(w.nodes[ids[j]].Name)
		if a.OK && b.OK {
			if a.Stage != b.Stage {
				return a.Stage < b.Stage
			}
			if a.Member != b.Member {
				return a.Member < b.Member
			}
		}
		return w.nodes[ids[i]].Name < w.nodes[ids[j]].Name
	})
}

// --- Navigation (Stage 4) ---

// Cursor is the current (stage, slot, member) position. Member is -1 when the
// cursor is on a slot (a lone node or a collapsed group), or ≥0 when inside a
// fanned-out group.
type Cursor struct {
	Stage  int
	Slot   int
	Member int // -1 when not inside a fanned-out group
}

// CursorPos returns the current cursor position.
//
// Stage 4 implements this.
func (w *Widget) CursorPos() Cursor { return Cursor{} }

// CurrentNodeID returns the node ID under the cursor: the lone node at the slot,
// the fanned-out member, or "" when the cursor is on a collapsed group (a group
// is not itself a node). Used by tests and OnEnter dispatch.
//
// Stage 4 implements this.
func (w *Widget) CurrentNodeID() string { return "" }

// Fanned reports whether the group at the given (stage, slot) is currently
// fanned out.
//
// Stage 4 implements this.
func (w *Widget) Fanned(stage, slot int) bool { return false }

// MoveStage moves the cursor by dStage (↑ -1 / ↓ +1), collapsing any fanned-out
// group on the way (D4 nav). Clamped at the stage edges.
//
// Stage 4 implements this.
func (w *Widget) MoveStage(dStage int) {}

// MoveSlot moves the cursor by dSlot within a stage (←/→). When the cursor is
// inside a fanned-out group it walks members; stepping off either edge exits and
// collapses the group, moving to the adjacent slot (or clamps).
//
// Stage 4 implements this.
func (w *Widget) MoveSlot(dSlot int) {}

// ActivateCursor performs the Enter/Space action at the cursor: fan out / collapse
// a group, drill into a sub-coordinator (fires OnDrillIn), or jump to a plain
// leaf's agent view (fires OnEnter). Disjoint by the cursor's target type (D6).
//
// Stage 4/6 implements this.
func (w *Widget) ActivateCursor() {}

// --- Drill-in (Stage 6) ---

// DrillDepth returns the orchestrator nav-stack depth: 0 at the root, ≥1 when
// drilled into a sub-coordinator (D6).
//
// Stage 6 implements this.
func (w *Widget) DrillDepth() int { return 0 }

// PushOrch pushes a child orchestrator's plan snapshot onto the nav stack and
// re-projects it (the drill-in target). title becomes the header title.
//
// Stage 6 implements this.
func (w *Widget) PushOrch(title string, nodes []Node, edges []Edge) {}

// PopOrch pops the nav stack back to the parent orchestrator's plan (Esc). No-op
// at the root.
//
// Stage 6 implements this.
func (w *Widget) PopOrch() {}

// --- Master-detail header (Stage 5) ---

// HeaderLines returns the fixed-height header strip's rendered lines for the
// current selection: for a node, [name, description, feeds]; for a collapsed
// group, [range·title, members, downstream] (D9).
//
// Stage 5 implements this.
func (w *Widget) HeaderLines() []string { return nil }

// HeaderHeight returns the fixed header height budgeted above the diagram (D9).
//
// Stage 5 implements this.
func (w *Widget) HeaderHeight() int { return 0 }

// --- tview wiring ---

// Draw paints the master-detail header strip then the plan diagram inside a
// bordered panel. No screen.Sync (CLAUDE.md UX-rendering rules).
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
	inner := widget.DrawBorderedPanel(screen, x, y, wpx, hpx, w.title, borderStyle)
	if inner.W <= 0 || inner.H <= 0 {
		return
	}
	if len(w.stages) == 0 {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "No plan — spawn a worker under this coordinator.", theme.StyleDimmed)
		return
	}
	if w.noPlan {
		widget.DrawText(screen, inner.X, inner.Y, inner.W, "no plan authored — live roles:", theme.StyleDimmed)
	}
	w.drawStages(screen, inner)
}

// drawStages paints each stage as a row of chips/group boxes with single-line
// vertical edges between consecutive stages.
func (w *Widget) drawStages(screen tcell.Screen, inner widget.InnerRect) {
	row := inner.Y
	if w.noPlan {
		row++ // leave the hint line above the flat stage
	}
	for s := 0; s < len(w.stages); s++ {
		col := inner.X
		for _, sl := range w.stages[s] {
			label, st := w.slotChip(sl)
			runes := []rune(label)
			for i, r := range runes {
				if col+i >= inner.X+inner.W {
					break
				}
				screen.SetContent(col+i, row, r, nil, st)
			}
			col += len(runes) + 2 // chip gap
			if col >= inner.X+inner.W {
				break
			}
		}
		// Single-line edge connector between stages (cosmetic).
		if s < len(w.stages)-1 && row+1 < inner.Y+inner.H {
			screen.SetContent(inner.X, row+1, '│', nil, theme.StyleDimmed.Foreground(theme.ColorBorder))
		}
		row += 2
		if row >= inner.Y+inner.H {
			break
		}
	}
}

// slotChip returns a slot's rendered label + style: a glyph+short-id chip for a
// lone node, or the range-box label for a collapsed group (with its aggregate
// counts appended).
func (w *Widget) slotChip(sl slot) (string, tcell.Style) {
	if sl.group != nil {
		return sl.group.Label + " " + groupCounts(sl.group), tcell.StyleDefault.Foreground(theme.ColorNormal)
	}
	n := w.nodes[sl.nodeID]
	return string(n.State.Glyph()) + " " + w.LabelOf(sl.nodeID), n.State.style()
}

// groupCounts renders the aggregate per-state counts for a collapsed group box,
// e.g. "3 ✓ · 2 ⟳ · 1 ○". States with a zero count are omitted; the order
// follows the State enum for stability.
func groupCounts(g *Group) string {
	var parts []string
	for _, s := range []State{StateDone, StateInReview, StateWorking, StatePending, StateFailed, StatePlanned} {
		if c := g.Counts[s]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d %c", c, s.Glyph()))
		}
	}
	return strings.Join(parts, " · ")
}

// InputHandler routes the 4-way navigation (↑↓ stage, ←→ slot/member),
// Enter/Space (fan-out/collapse, drill-in, or jump), and Esc (drill-out).
//
// Stage 4/6 implements this.
func (w *Widget) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return w.WrapInputHandler(func(*tcell.EventKey, func(tview.Primitive)) {})
}

// MouseHandler positions the cursor on the clicked slot and yields focus.
//
// Stage 4 implements this.
func (w *Widget) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return w.WrapMouseHandler(func(_ tview.MouseAction, _ *tcell.EventMouse, _ func(tview.Primitive)) (bool, tview.Primitive) {
		return false, nil
	})
}

// PasteHandler is a no-op (the plan view is read-only navigation).
func (w *Widget) PasteHandler() func(string, func(tview.Primitive)) {
	return w.WrapPasteHandler(func(string, func(tview.Primitive)) {})
}

// branchShape captures the parts of state that, when changed, mean Draw would
// paint a structurally different cell set. Folds in node/edge/stage counts, the
// focus flag, and (Stage 4) the cursor/fanned state. Log-only — no Sync.
func (w *Widget) branchShape() uint64 {
	var shape uint64
	shape |= uint64(len(w.order) & 0xFFFFFF)
	shape |= uint64(len(w.stages)&0xFF) << 24
	shape |= uint64(len(w.edges)&0xFFF) << 32
	if w.focused {
		shape |= 1 << 44
	}
	if w.noPlan {
		shape |= 1 << 45
	}
	return shape
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

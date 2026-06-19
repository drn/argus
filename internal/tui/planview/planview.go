// Package planview is the TUI widget that renders a Hera *plan DAG* — planned
// and live worker roles as nodes, hera_blocks blocking edges between them — as
// the "tight tree" UX (short-id labels, auto-collapsing parallel groups,
// 4-way cursor navigation, a master-detail header, and sub-coordinator
// drill-in). It replaces the orchestration-tree graph (heraTreeNodes) in the
// Hera Details pane and reuses dagview's Kahn longest-path layer math for stage
// placement. See openspec/changes/add-hera-plan-view/design.md.
//
// Stage 3 (layout + render), Stage 4 (the (stage, slot, member) cursor
// navigation with group fan-out), Stage 5 (the master-detail header), and
// Stage 6 (sub-coordinator drill-in via an orchestrator nav stack) are all
// implemented here.
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

	// cursor is the current (stage, slot, member) position. Member is -1 unless
	// the cursor is inside a fanned-out group (Stage 4).
	cursor Cursor
	// fanned tracks which group slots are currently fanned out, keyed by
	// [stage, slot]. A group slot is collapsed unless present here (Stage 4).
	fanned map[[2]int]bool

	// dataSig is the structural signature of the currently-displayed snapshot
	// (projectionSig). UpdateData no-ops when the incoming signature matches, so a
	// refresh tick that re-projects an unchanged plan never disturbs the cursor.
	dataSig uint64

	// navStack holds the parent orchestrators' snapshots saved on drill-in (D6).
	// Each frame is the full render state to restore on PopOrch; the live fields
	// (nodes/edges/title/cursor/...) always describe the *current* orchestrator,
	// so DrillDepth is len(navStack).
	navStack []navFrame

	lastShape uint64
}

// navFrame is one saved orchestrator render state on the drill-in nav stack
// (D6). PushOrch saves the current state into a frame before installing the
// child; PopOrch restores the top frame.
type navFrame struct {
	nodes  []Node
	edges  []Edge
	title  string
	cursor Cursor
	fanned map[[2]int]bool
}

// New constructs an empty plan-view widget. SetData must be called before the
// widget is meaningful.
func New() *Widget {
	return &Widget{
		Box:     tview.NewBox(),
		nodes:   map[string]Node{},
		stageOf: map[string]int{},
		labelOf: map[string]string{},
		fanned:  map[[2]int]bool{},
		cursor:  Cursor{Member: -1},
		title:   " Plan ",
	}
}

// SetTitle overrides the bordered-panel title (the Hera Details pane sets it to
// " Plan "). Pass "" to suppress the title text.
func (w *Widget) SetTitle(title string) { w.title = title }

// SetData installs a new plan snapshot: the node set plus the blocking edges.
// Recomputes the stage layout (Kahn longest-path over the edges, D3), detects
// parallel groups (D4), and RESETS the cursor + fan-out to stage 0, slot 0.
//
// SetData is the full-reset path: it is correct for a genuine selection change
// (a different coordinator's plan) and for the drill-in PushOrch/PopOrch
// gestures, where the prior cursor/fan-out is meaningless against the new graph.
// It is WRONG for the ~1s refresh tick re-projecting the SAME orchestrator's
// plan — that path must call UpdateData, which preserves the user's cursor and
// fanned groups. See gotchas/hera-view.md.
func (w *Widget) SetData(nodes []Node, edges []Edge) {
	w.installLayout(nodes, edges)
	// A fresh snapshot invalidates every prior fan-out and the cursor; reset to
	// stage 0, slot 0, clamped to the new layout.
	w.fanned = map[[2]int]bool{}
	w.cursor = Cursor{Member: -1}
	w.clampCursor()

	uxlog.Log("[planview] SetData: nodes=%d edges=%d stages=%d noPlan=%v",
		len(nodes), len(edges), len(w.stages), w.noPlan)
	w.maybeNotifyBranchChange()
}

// UpdateData re-projects the SAME orchestrator's plan on a refresh tick while
// PRESERVING the user's UI state. When the projected (nodes, edges) signature is
// byte-for-byte the structure already displayed, it is a pure no-op (cursor and
// fanned groups untouched). When the structure changed — a cascade step
// materialized a planned node, a state flipped, an edge appeared — it recomputes
// the layout and RE-ANCHORS the cursor to the same node ID (or, on a collapsed
// group, the same member-id set) when it still exists, clamping when it
// vanished; it then re-applies fan-out to every group whose member-id set still
// resolves to a slot.
//
// This is the refresh-safe counterpart to SetData: applySelection runs every
// ~1s tick, so an unconditional SetData there would reset the cursor to
// stage0/slot0 and collapse a fanned group out from under the operator. See
// gotchas/hera-view.md.
func (w *Widget) UpdateData(nodes []Node, edges []Edge) {
	sig := projectionSig(nodes, edges)
	if sig == w.dataSig && len(w.order) == len(nodes) {
		// Structure is identical to what's displayed: nothing to re-layout, so the
		// cursor and fan-out stay exactly where the user left them.
		return
	}

	// Capture the re-anchor targets before the layout is rebuilt: the node ID the
	// cursor names (lone node or fanned member), the collapsed-group member set the
	// cursor sits on, and the member-id set of every currently-fanned group.
	anchorNodeID := w.CurrentNodeID()
	anchorGroupKey := w.cursorGroupKey()
	fannedKeys := w.fannedGroupKeys()

	w.installLayout(nodes, edges)
	w.reanchorCursor(anchorNodeID, anchorGroupKey)
	w.reapplyFanned(fannedKeys)
	// If the cursor re-anchored onto a fanned group via a member node ID, restore
	// the member index so the cursor names the same node it did before (a collapsed
	// re-anchor leaves Member -1, which would name no node inside a fanned group).
	w.restoreFannedMember(anchorNodeID)
	w.clampCursor()

	uxlog.Log("[planview] UpdateData: nodes=%d edges=%d stages=%d noPlan=%v reanchor=%q",
		len(nodes), len(edges), len(w.stages), w.noPlan, anchorNodeID)
	w.maybeNotifyBranchChange()
}

// installLayout rebuilds the node map, edges, stage layout, labels, and slots
// from a snapshot WITHOUT touching the cursor or fan-out. Shared by SetData
// (which then resets the cursor) and UpdateData (which re-anchors it).
func (w *Widget) installLayout(nodes []Node, edges []Edge) {
	w.nodes = make(map[string]Node, len(nodes))
	w.order = w.order[:0]
	for _, n := range nodes {
		w.nodes[n.ID] = n
		w.order = append(w.order, n.ID)
	}
	w.edges = edges
	w.dataSig = projectionSig(nodes, edges)

	// No plan authored: no planned nodes and no edges (D1). Render every node as
	// one flat edgeless stage.
	w.noPlan = !hasPlan(nodes, edges)

	w.computeStages(nodes, edges)
	w.computeLabels(nodes)
	w.buildSlots(nodes, edges)
}

// projectionSig hashes a snapshot's structure: every node's ID + State +
// Drillable, and every edge's endpoints. Two snapshots with the same signature
// render identical cells, so UpdateData can no-op (preserving cursor/fan-out)
// when the signature is unchanged. Order-sensitive on purpose — the projection
// is deterministic, so a stable order means a stable signature.
func projectionSig(nodes []Node, edges []Edge) uint64 {
	var h uint64 = 1469598103934665603 // FNV-1a offset basis
	mix := func(s string) {
		for i := 0; i < len(s); i++ {
			h ^= uint64(s[i])
			h *= 1099511628211
		}
		h ^= 0 // field separator
		h *= 1099511628211
	}
	mixByte := func(b byte) {
		h ^= uint64(b)
		h *= 1099511628211
	}
	for _, n := range nodes {
		mix(n.ID)
		mixByte(byte(n.State))
		if n.Drillable {
			mixByte(1)
		} else {
			mixByte(0)
		}
	}
	mixByte(0xFF) // node/edge boundary
	for _, e := range edges {
		mix(e.From)
		mix(e.To)
	}
	return h
}

// cursorGroupKey returns the stable key (sorted member-id set) of the collapsed
// group the cursor currently sits on, or "" when the cursor is on a lone node or
// a fanned member. Used by UpdateData to re-anchor a group-slot cursor across a
// re-layout when the member identities are unchanged.
func (w *Widget) cursorGroupKey() string {
	sl, ok := w.slotAt(w.cursor.Stage, w.cursor.Slot)
	if !ok || sl.group == nil || w.cursor.Member >= 0 {
		return ""
	}
	return groupKey(sl.group.Members)
}

// fannedGroupKeys returns the stable member-id-set key of every currently
// fanned-out group, so UpdateData can re-fan the same groups after a re-layout
// even when their (stage, slot) coordinates shifted.
func (w *Widget) fannedGroupKeys() map[string]bool {
	keys := map[string]bool{}
	for k := range w.fanned {
		sl, ok := w.slotAt(k[0], k[1])
		if ok && sl.group != nil {
			keys[groupKey(sl.group.Members)] = true
		}
	}
	return keys
}

// groupKey is the stable identity of a group: its member IDs, sorted and joined.
// A re-layout that preserves a group's membership yields the same key even when
// the group's (stage, slot) coordinates moved.
func groupKey(members []string) string {
	cp := append([]string(nil), members...)
	sort.Strings(cp)
	return strings.Join(cp, "\x00")
}

// reanchorCursor positions the cursor over the same node (or the same collapsed
// group) it named before a re-layout. anchorNodeID is the node the cursor was
// on; anchorGroupKey is the member-set key of a collapsed group the cursor sat
// on (mutually exclusive with anchorNodeID). When neither still resolves, the
// cursor falls back to stage 0, slot 0 and clampCursor finishes the job.
func (w *Widget) reanchorCursor(anchorNodeID, anchorGroupKey string) {
	w.cursor = Cursor{Member: -1}
	if anchorNodeID != "" {
		if found := w.locateNode(anchorNodeID); found {
			return
		}
	}
	if anchorGroupKey != "" {
		if found := w.locateGroup(anchorGroupKey); found {
			return
		}
	}
}

// locateNode places the cursor on the node with the given ID: a lone-node slot
// lands directly on the slot; a node inside a group lands on the group's slot
// (collapsed, Member -1) so it re-fans cleanly when reapplyFanned runs. Reports
// whether the node was found.
func (w *Widget) locateNode(id string) bool {
	for s := range w.stages {
		for slotIdx, sl := range w.stages[s] {
			if sl.group == nil {
				if sl.nodeID == id {
					w.cursor = Cursor{Stage: s, Slot: slotIdx, Member: -1}
					return true
				}
				continue
			}
			for _, m := range sl.group.Members {
				if m == id {
					w.cursor = Cursor{Stage: s, Slot: slotIdx, Member: -1}
					return true
				}
			}
		}
	}
	return false
}

// locateGroup places the cursor on the group slot whose member-id set matches
// key (the collapsed-group re-anchor). Reports whether the group was found.
func (w *Widget) locateGroup(key string) bool {
	for s := range w.stages {
		for slotIdx, sl := range w.stages[s] {
			if sl.group != nil && groupKey(sl.group.Members) == key {
				w.cursor = Cursor{Stage: s, Slot: slotIdx, Member: -1}
				return true
			}
		}
	}
	return false
}

// restoreFannedMember sets the cursor's Member index to anchorNodeID's position
// within the group it now sits on, but only when that slot is a fanned group and
// anchorNodeID is one of its members. This restores a member-level cursor across
// a re-layout (the cursor was walking inside a fanned group before the refresh).
// On a collapsed group or a lone node it leaves Member at -1.
func (w *Widget) restoreFannedMember(anchorNodeID string) {
	if anchorNodeID == "" {
		return
	}
	sl, ok := w.slotAt(w.cursor.Stage, w.cursor.Slot)
	if !ok || sl.group == nil || !w.Fanned(w.cursor.Stage, w.cursor.Slot) {
		return
	}
	for i, m := range sl.group.Members {
		if m == anchorNodeID {
			w.cursor.Member = i
			return
		}
	}
}

// reapplyFanned re-fans every group slot whose member-id set is in keys (the
// set captured before a re-layout). A group that vanished or whose membership
// changed simply stays collapsed. Run after reanchorCursor so the fan map is
// rebuilt against the new slot coordinates.
func (w *Widget) reapplyFanned(keys map[string]bool) {
	w.fanned = map[[2]int]bool{}
	if len(keys) == 0 {
		return
	}
	for s := range w.stages {
		for slotIdx, sl := range w.stages[s] {
			if sl.group != nil && keys[groupKey(sl.group.Members)] {
				w.fanned[[2]int{s, slotIdx}] = true
			}
		}
	}
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
func (w *Widget) CursorPos() Cursor { return w.cursor }

// CurrentNodeID returns the node ID under the cursor: the lone node at the slot,
// the fanned-out member, or "" when the cursor is on a collapsed group (a group
// is not itself a node). Used by tests and OnEnter dispatch.
func (w *Widget) CurrentNodeID() string {
	sl, ok := w.slotAt(w.cursor.Stage, w.cursor.Slot)
	if !ok {
		return ""
	}
	if sl.group == nil {
		return sl.nodeID
	}
	// On a group: the cursor names a node only when fanned out and pointing at a
	// member; a collapsed group is not itself a node.
	if w.cursor.Member >= 0 && w.cursor.Member < len(sl.group.Members) {
		return sl.group.Members[w.cursor.Member]
	}
	return ""
}

// Fanned reports whether the group at the given (stage, slot) is currently
// fanned out.
func (w *Widget) Fanned(stage, slot int) bool { return w.fanned[[2]int{stage, slot}] }

// MoveStage moves the cursor by dStage (↑ -1 / ↓ +1), collapsing any fanned-out
// group on the way (D4 nav). Clamped at the stage edges.
func (w *Widget) MoveStage(dStage int) {
	if len(w.stages) == 0 {
		return
	}
	// Leaving a stage always exits + collapses whatever group the cursor was in.
	w.collapseCursorGroup()
	ns := w.cursor.Stage + dStage
	if ns < 0 {
		ns = 0
	}
	if ns >= len(w.stages) {
		ns = len(w.stages) - 1
	}
	w.cursor.Stage = ns
	w.cursor.Member = -1
	w.clampCursor()
	w.maybeNotifyBranchChange()
}

// MoveSlot moves the cursor by dSlot within a stage (←/→). When the cursor is
// inside a fanned-out group it walks members; stepping off either edge exits and
// collapses the group, moving to the adjacent slot (or clamps).
func (w *Widget) MoveSlot(dSlot int) {
	if len(w.stages) == 0 {
		return
	}
	sl, ok := w.slotAt(w.cursor.Stage, w.cursor.Slot)
	// Inside a fanned-out group: walk members, exiting off either edge.
	if ok && sl.group != nil && w.cursor.Member >= 0 && w.Fanned(w.cursor.Stage, w.cursor.Slot) {
		nm := w.cursor.Member + dSlot
		if nm >= 0 && nm < len(sl.group.Members) {
			w.cursor.Member = nm
			w.maybeNotifyBranchChange()
			return
		}
		// Stepped off the group edge: collapse and move to the adjacent slot
		// (clamping at the stage edge).
		w.collapseCursorGroup()
		w.cursor.Member = -1
		w.shiftSlot(dSlot)
		w.maybeNotifyBranchChange()
		return
	}
	// On a slot (lone node or collapsed group): move between slots.
	w.cursor.Member = -1
	w.shiftSlot(dSlot)
	w.maybeNotifyBranchChange()
}

// shiftSlot moves the cursor's slot by dSlot within the current stage, clamped
// to the stage's slot range.
func (w *Widget) shiftSlot(dSlot int) {
	n := w.SlotCount(w.cursor.Stage)
	if n == 0 {
		w.cursor.Slot = 0
		return
	}
	ns := w.cursor.Slot + dSlot
	if ns < 0 {
		ns = 0
	}
	if ns >= n {
		ns = n - 1
	}
	w.cursor.Slot = ns
}

// ActivateCursor performs the Enter/Space action at the cursor. Disjoint by the
// cursor's target type (D6): on a group it fans out / collapses; Stage 6 adds
// the sub-coordinator drill-in and plain-leaf OnEnter branches.
func (w *Widget) ActivateCursor() {
	sl, ok := w.slotAt(w.cursor.Stage, w.cursor.Slot)
	if !ok {
		return
	}
	// Group target (Stage 4): toggle fan-out / collapse.
	if sl.group != nil {
		key := [2]int{w.cursor.Stage, w.cursor.Slot}
		if w.fanned[key] {
			delete(w.fanned, key)
			w.cursor.Member = -1
		} else {
			w.fanned[key] = true
			w.cursor.Member = 0 // land on the first member
		}
		w.maybeNotifyBranchChange()
		return
	}
	// Node target (Stage 6): disjoint by the node's type. A sub-coordinator node
	// drills into its child orchestrator (the consumer re-projects + pushes via
	// OnDrillIn); a plain leaf jumps to its agent view via OnEnter.
	id := w.CurrentNodeID()
	if id == "" {
		return
	}
	if w.nodes[id].Drillable {
		if w.OnDrillIn != nil {
			w.OnDrillIn(id)
		}
		return
	}
	if w.OnEnter != nil {
		w.OnEnter(id)
	}
}

// slotAt returns the slot at (stage, slotIdx) and whether it exists.
func (w *Widget) slotAt(stage, slotIdx int) (slot, bool) {
	if stage < 0 || stage >= len(w.stages) {
		return slot{}, false
	}
	st := w.stages[stage]
	if slotIdx < 0 || slotIdx >= len(st) {
		return slot{}, false
	}
	return st[slotIdx], true
}

// collapseCursorGroup collapses the group slot the cursor currently occupies
// (if any), so leaving the slot never leaves a stale fan-out behind.
func (w *Widget) collapseCursorGroup() {
	delete(w.fanned, [2]int{w.cursor.Stage, w.cursor.Slot})
}

// clampCursor keeps the cursor within the current layout: a valid stage, a valid
// slot within that stage, and Member -1 unless the slot is a fanned-out group.
func (w *Widget) clampCursor() {
	if len(w.stages) == 0 {
		w.cursor = Cursor{Member: -1}
		return
	}
	if w.cursor.Stage < 0 {
		w.cursor.Stage = 0
	}
	if w.cursor.Stage >= len(w.stages) {
		w.cursor.Stage = len(w.stages) - 1
	}
	n := w.SlotCount(w.cursor.Stage)
	if w.cursor.Slot < 0 {
		w.cursor.Slot = 0
	}
	if n > 0 && w.cursor.Slot >= n {
		w.cursor.Slot = n - 1
	}
	if !w.Fanned(w.cursor.Stage, w.cursor.Slot) {
		w.cursor.Member = -1
	}
}

// --- Drill-in (Stage 6) ---

// DrillDepth returns the orchestrator nav-stack depth: 0 at the root, ≥1 when
// drilled into a sub-coordinator (D6).
func (w *Widget) DrillDepth() int { return len(w.navStack) }

// PushOrch pushes a child orchestrator's plan snapshot onto the nav stack and
// re-projects it (the drill-in target). The current orchestrator's full render
// state (nodes, edges, title, cursor, fan-out) is saved onto the stack first so
// PopOrch can restore it exactly; the child's nodes/edges then install via the
// same layout path SetData uses, and title becomes the header title (D6).
func (w *Widget) PushOrch(title string, nodes []Node, edges []Edge) {
	w.navStack = append(w.navStack, navFrame{
		nodes:  w.snapshotNodes(),
		edges:  w.edges,
		title:  w.title,
		cursor: w.cursor,
		fanned: w.fanned,
	})
	w.title = title
	// Reuse SetData's layout + cursor-reset path; it also fires the branch-change
	// callback (the title hash folded into branchShape re-fires it for free).
	w.SetData(nodes, edges)
}

// PopOrch pops the nav stack back to the parent orchestrator's plan (Esc),
// restoring its saved nodes/edges/title and re-clamping the parent's cursor +
// fan-out. No-op at the root (D6).
func (w *Widget) PopOrch() {
	if len(w.navStack) == 0 {
		return
	}
	top := w.navStack[len(w.navStack)-1]
	w.navStack = w.navStack[:len(w.navStack)-1]
	w.title = top.title
	// Reinstall the parent's snapshot, then restore the saved cursor + fan-out
	// (SetData resets both, so re-apply after and clamp to the rebuilt layout).
	w.SetData(top.nodes, top.edges)
	w.fanned = top.fanned
	if w.fanned == nil {
		w.fanned = map[[2]int]bool{}
	}
	w.cursor = top.cursor
	w.clampCursor()
	w.maybeNotifyBranchChange()
}

// snapshotNodes copies the current node set back into a []Node in projection
// order (w.order), so a pushed frame can restore the parent verbatim on pop.
func (w *Widget) snapshotNodes() []Node {
	out := make([]Node, 0, len(w.order))
	for _, id := range w.order {
		out = append(out, w.nodes[id])
	}
	return out
}

// --- Master-detail header (Stage 5) ---

// headerContentRows is the fixed number of content lines the header strip
// occupies (D9): three description lines (node → name / description / feeds;
// group → range·title / members / downstream) plus one separator rule below.
// Held constant so the diagram budget never drifts with the cursor target.
const headerContentRows = 3

// headerHeight is the total fixed header height: the three content rows plus a
// one-row separator rule. The diagram region is the panel inner height minus
// this, mirroring DetailsView.ContentHeight's exact-budget discipline (D9).
const headerHeight = headerContentRows + 1

// HeaderLines returns the fixed-height header strip's rendered lines for the
// current selection: for a node, [name, description, feeds]; for a collapsed
// group, [range·title, members, downstream] (D9). Always returns exactly
// headerContentRows lines (padded with "") so the strip's height never depends
// on the cursor target — the budget stays exact.
func (w *Widget) HeaderLines() []string {
	lines := w.headerContent()
	// Pad/clamp to exactly headerContentRows so the strip is fixed-height.
	for len(lines) < headerContentRows {
		lines = append(lines, "")
	}
	return lines[:headerContentRows]
}

// headerContent builds the (unpadded) header lines for the current cursor
// target: a collapsed group when the cursor sits on a group slot, otherwise the
// node under the cursor. An empty selection (empty widget) yields no lines.
func (w *Widget) headerContent() []string {
	sl, ok := w.slotAt(w.cursor.Stage, w.cursor.Slot)
	if !ok {
		return nil
	}
	// On a collapsed group (cursor not fanned into a member): group view.
	if sl.group != nil && w.cursor.Member < 0 {
		return w.groupHeaderLines(sl.group)
	}
	if id := w.CurrentNodeID(); id != "" {
		return w.nodeHeaderLines(id)
	}
	return nil
}

// nodeHeaderLines renders the node-view header: the role name, its description
// (the delivery-prompt first line), and what it feeds (the downstream nodes it
// blocks, by label, from the edge set) (D9).
func (w *Widget) nodeHeaderLines(id string) []string {
	n := w.nodes[id]
	desc := n.Description
	if desc == "" {
		desc = "(no description)"
	}
	feeds := w.feedLabels(id)
	feedsLine := "Feeds: " + strings.Join(feeds, ", ")
	if len(feeds) == 0 {
		feedsLine = "Feeds: (nothing)"
	}
	return []string{
		n.Name,
		desc,
		feedsLine,
	}
}

// groupHeaderLines renders the group-view header: the range·title (the group's
// range-box label), its members (by label), and its downstream target (the
// nodes any member feeds outside the group) (D9).
func (w *Widget) groupHeaderLines(g *Group) []string {
	members := make([]string, 0, len(g.Members))
	for _, m := range g.Members {
		members = append(members, w.LabelOf(m))
	}
	down := w.groupDownstreamLabels(g)
	downLine := "Downstream: " + strings.Join(down, ", ")
	if len(down) == 0 {
		downLine = "Downstream: (nothing)"
	}
	return []string{
		"Group " + g.Label,
		"Members: " + strings.Join(members, ", "),
		downLine,
	}
}

// feedLabels returns the labels of the nodes a node feeds (the To-endpoints of
// its outgoing edges), sorted by short-id for stable presentation.
func (w *Widget) feedLabels(id string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, e := range w.edges {
		if e.From == id && !seen[e.To] {
			seen[e.To] = true
			ids = append(ids, e.To)
		}
	}
	w.sortByShortID(ids)
	labels := make([]string, 0, len(ids))
	for _, did := range ids {
		labels = append(labels, w.LabelOf(did))
	}
	return labels
}

// groupDownstreamLabels returns the labels of the nodes the group feeds — the
// To-endpoints of any member's outgoing edge that lands outside the group —
// sorted by short-id and de-duplicated.
func (w *Widget) groupDownstreamLabels(g *Group) []string {
	memberSet := make(map[string]bool, len(g.Members))
	for _, m := range g.Members {
		memberSet[m] = true
	}
	var ids []string
	seen := map[string]bool{}
	for _, e := range w.edges {
		if memberSet[e.From] && !memberSet[e.To] && !seen[e.To] {
			seen[e.To] = true
			ids = append(ids, e.To)
		}
	}
	w.sortByShortID(ids)
	labels := make([]string, 0, len(ids))
	for _, did := range ids {
		labels = append(labels, w.LabelOf(did))
	}
	return labels
}

// HeaderHeight returns the fixed header height budgeted above the diagram (D9).
// The diagram region gets the panel inner height minus this value, with no
// drift across cursor targets.
func (w *Widget) HeaderHeight() int { return headerHeight }

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
	// Master-detail header strip above the diagram (D9). Its height is fixed and
	// budgeted exactly; the diagram gets the remainder. When the panel is too
	// short to fit even the header, skip it and give the diagram the whole rect.
	diagram := inner
	if inner.H > headerHeight {
		w.drawHeader(screen, inner)
		diagram = widget.InnerRect{
			X: inner.X,
			Y: inner.Y + headerHeight,
			W: inner.W,
			H: inner.H - headerHeight,
		}
	}
	if w.noPlan {
		widget.DrawText(screen, diagram.X, diagram.Y, diagram.W, "no plan authored — live roles:", theme.StyleDimmed)
	}
	w.drawStages(screen, diagram)
}

// drawHeader paints the fixed-height master-detail header strip at the top of
// the inner rect: headerContentRows description lines plus a separator rule.
// DrawBorderedPanel has already blanked the interior, so a shorter selection's
// padded blank lines keep the strip free of stale cells (no Sync).
func (w *Widget) drawHeader(screen tcell.Screen, inner widget.InnerRect) {
	lines := w.HeaderLines()
	for i, line := range lines {
		row := inner.Y + i
		style := theme.StyleNormal
		if i == 0 {
			style = theme.StyleTitle // the name / group title line stands out
		}
		widget.DrawText(screen, inner.X, row, inner.W, line, style)
	}
	// Separator rule below the content, before the diagram.
	rule := inner.Y + headerContentRows
	for col := inner.X; col < inner.X+inner.W; col++ {
		screen.SetContent(col, rule, '─', nil, theme.StyleDimmed.Foreground(theme.ColorBorder))
	}
}

// chipGap is the blank-column run between two chips/group boxes in a stage row.
const chipGap = 2

// drawStages paints each stage as a CENTERED row of chips/group boxes with
// single-line vertical edges between consecutive stages, mirroring the web
// artifact's tight-tree layout. Each stage row is horizontally centered within
// the diagram region (rune-aware width math) and the whole block is vertically
// centered when it is shorter than the region. The chip under the cursor is
// drawn with a highlight style so the selected node is visible in the diagram,
// not only in the header (BUG: no selection highlight).
func (w *Widget) drawStages(screen tcell.Screen, inner widget.InnerRect) {
	top := inner.Y
	if w.noPlan {
		top++ // leave the hint line above the flat stage
	}
	// Vertically center the block (two rows per stage minus the trailing gap)
	// within the region below the optional hint line.
	availH := inner.Y + inner.H - top
	blockH := len(w.stages)*2 - 1
	if blockH < 0 {
		blockH = 0
	}
	if availH > blockH {
		top += (availH - blockH) / 2
	}
	row := top
	for s := 0; s < len(w.stages); s++ {
		if row >= inner.Y+inner.H {
			break
		}
		chips := w.stageRowChips(s)
		// Horizontally center the row: total rendered width is the sum of chip
		// widths plus a chipGap between each pair (rune-aware). A fanned group
		// contributes its expanded member chips, so this tracks the wider row.
		col := inner.X
		if rowW := rowChipsWidth(chips); inner.W > rowW {
			col += (inner.W - rowW) / 2
		}
		for _, c := range chips {
			runes := []rune(c.label)
			for i, r := range runes {
				if col+i >= inner.X+inner.W {
					break
				}
				screen.SetContent(col+i, row, r, nil, c.style)
			}
			col += len(runes) + chipGap
			if col >= inner.X+inner.W {
				break
			}
		}
		// Single-line edge connector between stages (cosmetic), centered under the
		// row so the connector tracks the centered chips.
		if s < len(w.stages)-1 && row+1 < inner.Y+inner.H {
			ec := inner.X + inner.W/2
			screen.SetContent(ec, row+1, '│', nil, theme.StyleDimmed.Foreground(theme.ColorBorder))
		}
		row += 2
	}
}

// renderChip is one drawable chip in a stage row: its rendered label and the
// style to paint it with (already cursor-highlighted when appropriate).
type renderChip struct {
	label string
	style tcell.Style
}

// stageRowChips builds the ordered drawable chips for a stage, expanding any
// FANNED group slot into one chip per member (glyph + short-id, with the partial-
// feed ↘ on the feeding member) instead of the collapsed range box — this is the
// "Enter fans out a group to SHOW its members" behaviour the web artifact uses.
// A collapsed group renders as a single range-box chip; a lone node as its
// glyph+short-id chip. The cursor's slot (lone node / collapsed group) or its
// member (inside a fanned group) carries the highlight style.
func (w *Widget) stageRowChips(s int) []renderChip {
	if s < 0 || s >= len(w.stages) {
		return nil
	}
	var chips []renderChip
	for slotIdx, sl := range w.stages[s] {
		onSlot := w.cursor.Stage == s && w.cursor.Slot == slotIdx
		if sl.group != nil && w.Fanned(s, slotIdx) {
			// Expanded: one chip per member. Highlight the cursor's member.
			for memberIdx, id := range sl.group.Members {
				label, st := w.memberChip(sl.group, id)
				if onSlot && w.cursor.Member == memberIdx {
					st = w.highlightStyle(st)
				}
				chips = append(chips, renderChip{label: label, style: st})
			}
			continue
		}
		// Collapsed group or lone node: one chip.
		label, st := w.slotChip(sl)
		if onSlot {
			st = w.highlightStyle(st)
		}
		chips = append(chips, renderChip{label: label, style: st})
	}
	return chips
}

// memberChip renders a single fanned-out group member's chip: glyph + short-id in
// the member's own state style, with the partial-feed ↘ appended on the group's
// feeding member (D5 — on fan-out the specific feeding member carries the marker
// the collapsed box otherwise shows).
func (w *Widget) memberChip(g *Group, id string) (string, tcell.Style) {
	n := w.nodes[id]
	label := string(n.State.Glyph()) + " " + w.LabelOf(id)
	if g.PartialFeed && g.FeedingMember == id {
		label += " ↘"
	}
	return label, n.State.style()
}

// rowChipsWidth returns the total rendered width (cells) of a row's chips: the
// sum of each chip's rune width plus a chipGap between consecutive chips.
func rowChipsWidth(chips []renderChip) int {
	total := 0
	for i, c := range chips {
		total += len([]rune(c.label))
		if i > 0 {
			total += chipGap
		}
	}
	return total
}

// highlightStyle returns the cursor-highlight variant of a chip's state style:
// reverse video (matching dagview's cursor treatment) so the selected node
// stands out in the diagram regardless of its state colour. When the widget owns
// focus the highlight is bolded for extra prominence; an unfocused widget still
// reverses so the last position stays visible.
func (w *Widget) highlightStyle(st tcell.Style) tcell.Style {
	st = st.Reverse(true)
	if w.focused {
		st = st.Bold(true)
	}
	return st
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

// InputHandler routes the 4-way navigation (↑↓ stage, ←→ slot/member) and
// Enter/Space (fan-out/collapse on a group; Stage 6 adds drill-in/jump and Esc
// drill-out). Unknown keys fall through to the default tview.Box no-op.
func (w *Widget) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return w.WrapInputHandler(func(event *tcell.EventKey, _ func(tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyUp:
			w.MoveStage(-1)
		case tcell.KeyDown:
			w.MoveStage(1)
		case tcell.KeyLeft:
			w.MoveSlot(-1)
		case tcell.KeyRight:
			w.MoveSlot(1)
		case tcell.KeyEnter:
			w.ActivateCursor()
		case tcell.KeyEscape:
			// Esc pops the drill-in nav stack back to the parent orchestrator (D6).
			// At the root it is a no-op here so the key falls through to the page.
			if w.DrillDepth() > 0 {
				w.PopOrch()
				if w.OnDrillOut != nil {
					w.OnDrillOut()
				}
			}
		case tcell.KeyRune:
			switch event.Rune() {
			case ' ':
				w.ActivateCursor()
			case 'k':
				w.MoveStage(-1)
			case 'j':
				w.MoveStage(1)
			case 'h':
				w.MoveSlot(-1)
			case 'l':
				w.MoveSlot(1)
			}
		}
	})
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
// focus flag, the (stage, slot, member) cursor, the fanned-group state, and the
// current orchestrator (via the title hash, ghost-prevention across drill-in).
// Log-only — no Sync (CLAUDE.md UX-rendering rules).
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
	// Cursor: stage/slot/member each clamped to a byte (the diagram never has
	// hundreds of stages/slots, and member tops out at the largest group).
	shape |= uint64(w.cursor.Stage&0xFF) << 46
	shape |= uint64(w.cursor.Slot&0x1F) << 54
	// Member is -1..N; bias by 1 so -1 is distinguishable and fits 5 bits.
	shape |= uint64((w.cursor.Member+1)&0x1F) << 59
	// Fold the fanned-slot count and the current-orchestrator title into the
	// low bits via a small mix, so toggling a fan or drilling in re-fires the
	// branch-change callback without widening the bitfield.
	mix := uint64(len(w.fanned))
	for i := 0; i < len(w.title); i++ {
		mix = mix*31 + uint64(w.title[i])
	}
	shape ^= mix << 12
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

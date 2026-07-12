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
	"time"

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
	// StateCancelled is a planned node that was explicitly cancelled by the
	// coordinator (grey ✕). It renders distinctly from StateFailed (red ✕) —
	// same glyph, different colour. Cancelled nodes remain visible in the plan
	// DAG but are excluded from materialization (make-hera-plan-living B3).
	StateCancelled
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
	case StateCancelled:
		return '✕'
	case StatePending:
		return '·'
	default: // StatePlanned
		return '○'
	}
}

// Label returns the human word for a state, shown on the node header's Status
// line (BUG-006): planned / working / in review / done / failed / pending.
func (s State) Label() string {
	switch s {
	case StateDone:
		return "done"
	case StateWorking:
		return "working"
	case StateInReview:
		return "in review"
	case StateFailed:
		return "failed"
	case StateCancelled:
		return "cancelled"
	case StatePending:
		return "pending"
	default: // StatePlanned
		return "planned"
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
	case StateCancelled:
		return base.Foreground(theme.ColorDimmed)
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
	// Description is the role's stored delivery prompt, VERBATIM. The header
	// (nodeHeaderLines) renders its first descMaxLines non-empty lines, wrapped/
	// truncated to the pane width; it is policy-agnostic (no stripping, no
	// classifying any line as boilerplate). An empty prompt shows "(no description)".
	Description string
	// Icon, when non-nil, is the resolved status glyph + style for a LIVE node,
	// computed by the projection via the SHARED classifier (widget.RoleStatusIcon)
	// so the plan node renders 1:1 with the rail (BUG-007). nil for a planned/failed
	// node (those use the State overlay: planned ○ / failed ✕) and for the
	// single-arg test projection — the widget then falls back to State.Glyph()/style.
	Icon *NodeIcon

	// Archetype / Model / Effort are the diligence-tiering readout
	// (add-diligence-profiles, D-VIEW): the node's selected archetype and the
	// model/effort that resolved for it. They are PURE render strings stamped by
	// the hera projection (the resolution itself — agent.ResolveModel + profile
	// load — runs OFF this widget, since it reads disk and the projection must stay
	// pure / tview-thread-safe). Empty Archetype means "(none)" (no profile
	// consulted); empty Model means the CLI/backend default applied.
	Archetype string
	Model     string
	Effort    string
	// ProfileWarning is non-empty when the node's project points at a missing or
	// invalid diligence profile (the runtime fail-open case). When set, the header
	// surfaces a ⚠ decoration so the operator is told why the agent ran on the CLI
	// default instead of a tiered model.
	ProfileWarning string
}

// NodeIcon is a live node's resolved status indicator, mirroring exactly what the
// rail's statusIcon renders for the same role (BUG-007). Animated marks the
// genuinely-active "working" case: the widget re-resolves the spinner frame at
// Draw (so it animates) rather than freezing the projection-time frame.
type NodeIcon struct {
	Glyph    rune
	Style    tcell.Style
	Animated bool
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
// share a blocker set and have no internal edges (D4). It renders as a two-line
// box: a top line `[first–last]` + a feed indicator, and a sub-line
// `<role token> · <per-state counts>` (BUG-005, matching the design images).
type Group struct {
	// Members are the node IDs in the group, sorted by short-id.
	Members []string
	// Stage is the computed longest-path stage shared by all members.
	Stage int
	// Label is the bare collapsed range box label ("[2a–2c]" / "[2a–2f +1]") — the
	// feed indicator (→ target / ↘) is rendered separately, NOT embedded here.
	Label string
	// Counts is the per-state aggregate (e.g. StateDone→3).
	Counts map[State]int
	// FeedTarget is the short-id of the single downstream node every out-of-group
	// edge points to (the FULL-feed case → "→ <FeedTarget>"); empty otherwise.
	FeedTarget string
	// PartialFeed is true when only SOME members feed downstream (the partial case
	// → "↘" on the top line); mutually exclusive with a non-empty FeedTarget.
	PartialFeed bool
	// FeedingMembers is the set of member node IDs that have an out-of-group edge;
	// each such member's box carries a ↘ on fan-out (the design's "2d ↘ 2e ↘ …").
	FeedingMembers map[string]bool
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

	// animFrame is the spinner animation frame recomputed at the top of each Draw
	// (wall-clock, mirroring the rail's spinnerFrame) so an Animated node icon
	// animates 1:1 with the rail (BUG-007). Read by nodeGlyph during the frame.
	animFrame int
	// frameFn is the source for animFrame, recomputed at the top of each Draw.
	// Defaults to planSpinnerFrame (wall-clock); tests inject a fixed frame so
	// spinner-glyph assertions are deterministic rather than racing the clock.
	frameFn func() int

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

	// xOffset is the horizontal viewport scroll (cells, ≥0), maintained across
	// Draws (BUG-010). When the widest stage block overflows the diagram width,
	// Draw ensure-visibles the cursor's selected box by adjusting this offset so a
	// node past the right edge is scrolled into view; 0 when everything fits.
	xOffset int

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
	// stage 0, slot 0, clamped to the new layout. The horizontal viewport resets
	// too (a different plan's widths are meaningless against the old offset).
	w.fanned = map[[2]int]bool{}
	w.cursor = Cursor{Member: -1}
	w.xOffset = 0
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
// Drillable + status icon, and every edge's endpoints. Two snapshots with the
// same signature render identical cells, so UpdateData can no-op (preserving
// cursor/fan-out) when the signature is unchanged. Order-sensitive on purpose —
// the projection is deterministic, so a stable order means a stable signature.
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
	mixU := func(v uint64) {
		h ^= v
		h *= 1099511628211
	}
	for _, n := range nodes {
		mix(n.ID)
		// Fold the state via its glyph string (a stable per-state value) so the sig
		// changes when a node's state flips — reuses the byte mixer, no numeric
		// conversion gosec would flag (State is a non-negative enum, unprovably so).
		mix(string(n.State.Glyph()))
		if n.Drillable {
			mixU(1)
		} else {
			mixU(0)
		}
		// Fold the resolved status icon (the rail-parity glyph carrying the
		// ready_to_close ✓ + hera role-status mark) so a status step / ready_to_close
		// clear that changes the node's GLYPH without changing its task-derived State
		// still flips the sig — otherwise UpdateData no-ops on the unchanged State and
		// the DAG node renders a stale ✓ while the rail already updated (BUG-012). The
		// projected Icon.Glyph is a stable frame-0 placeholder (spinner frames are
		// re-resolved at Draw), so this never spams a reproject. A nil icon (planned /
		// failed nodes fall back to State.Glyph) folds a distinct sentinel.
		if n.Icon != nil {
			mix(string(n.Icon.Glyph))
			if n.Icon.Animated {
				mixU(2)
			} else {
				mixU(3)
			}
		} else {
			mixU(0xEE) // nil-icon sentinel
		}
	}
	mixU(0xFF) // node/edge boundary
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
	if anchorNodeID != "" && w.locateNode(anchorNodeID) {
		return
	}
	if anchorGroupKey != "" && w.locateGroup(anchorGroupKey) {
		return
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

// buildGroup collapses members into a Group: bare range-box label, aggregate
// state counts, and the feed semantics (BUG-005). Members are already short-id
// sorted by the caller. Feed derivation over the members' out-of-group edges:
//   - every out-of-group edge points to ONE node → FeedTarget = that node's
//     short-id (full feed, renders "→ <id>" on the top line);
//   - only SOME members have an out-of-group edge → PartialFeed (renders "↘");
//   - FeedingMembers = the set of members with any out-of-group edge (each gets
//     a ↘ on its box when fanned out).
func (w *Widget) buildGroup(members []string, edges []Edge) *Group {
	g := &Group{Members: append([]string(nil), members...), Counts: map[State]int{}, FeedingMembers: map[string]bool{}}
	g.Stage = w.stageOf[members[0]]
	for _, m := range members {
		g.Counts[w.nodes[m].State]++
	}
	g.Label = w.groupLabel(members)

	memberSet := make(map[string]bool, len(members))
	for _, m := range members {
		memberSet[m] = true
	}
	// Collect the distinct out-of-group targets and which members feed them.
	targets := map[string]bool{}
	for _, m := range members {
		for _, e := range edges {
			if e.From == m && !memberSet[e.To] {
				g.FeedingMembers[m] = true
				targets[e.To] = true
			}
		}
	}
	feederCount := len(g.FeedingMembers)
	switch {
	case feederCount == 0:
		// No downstream feed at all — bare range box, no indicator.
	case feederCount < len(members):
		// Only SOME members feed downstream → partial "↘" (regardless of how many
		// distinct targets). This is checked before the single-target case so a lone
		// feeder reads as partial, not as a full "→ X" feed.
		g.PartialFeed = true
	case len(targets) == 1:
		// ALL members feed, and to ONE node → full feed "→ <short-id>".
		for t := range targets {
			g.FeedTarget = w.LabelOf(t)
		}
	default:
		// All members feed but to multiple distinct targets → partial "↘" (no
		// single arrow target to name).
		g.PartialFeed = true
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

// ToggleCursorFan is the Space action (BUG-013 follow-up): a PURE fan-out /
// collapse toggle on the group slot under the cursor — it never navigates. On a
// collapsed group it fans out (cursor lands on the first member); on a fanned
// group it collapses (regardless of which member the cursor is on), Member → -1.
// On a lone-node slot it is a no-op — opening a leaf is Enter's job, not Space's.
func (w *Widget) ToggleCursorFan() {
	sl, ok := w.slotAt(w.cursor.Stage, w.cursor.Slot)
	if !ok || sl.group == nil {
		return
	}
	key := [2]int{w.cursor.Stage, w.cursor.Slot}
	if w.fanned[key] {
		delete(w.fanned, key)
		w.cursor.Member = -1
	} else {
		w.fanned[key] = true
		w.cursor.Member = 0 // land on the first member
	}
	w.maybeNotifyBranchChange()
}

// ActivateCursor performs the Enter action at the cursor. Disjoint by the
// cursor's target type (D6): on a COLLAPSED group it fans out; on an interior
// MEMBER of a fanned group it navigates to that member (BUG-013), exactly like a
// plain leaf — collapse is Esc's job (EscBack) and Space's (ToggleCursorFan),
// never Enter; Stage 6 adds the sub-coordinator drill-in and plain-leaf OnEnter
// branches.
func (w *Widget) ActivateCursor() {
	sl, ok := w.slotAt(w.cursor.Stage, w.cursor.Slot)
	if !ok {
		return
	}
	// Group slot (Stage 4). A collapsed group fans out; a fanned enclosure with no
	// member selected toggles back collapsed (both delegated to the shared fan
	// toggle). But a fanned group with the cursor on a MEMBER falls through to the
	// node-target dispatch below so Enter navigates to that member instead of
	// collapsing the group (BUG-013) — collapse is Esc / Space.
	if sl.group != nil {
		key := [2]int{w.cursor.Stage, w.cursor.Slot}
		if !w.fanned[key] || w.cursor.Member < 0 {
			w.ToggleCursorFan()
			return
		}
		// Fanned group, cursor on a member: fall through to navigate to it.
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

// EscBack is the "back out one level" gesture, bound to Esc. It backs out of the
// plan view's OWN state in a fixed priority order and is always CONSUMED by the
// widget (the caller never lets Esc fall through to the page/rail):
//
//  1. cursor on a FANNED group → collapse it (un-fan; same effect as Enter/Space
//     toggling a fanned group: drop the fan, Member back to -1, notify).
//  2. else drilled into a sub-coordinator (DrillDepth > 0) → pop back to the
//     parent orchestrator's plan + fire OnDrillOut.
//  3. else (root, nothing fanned) → no-op. The operator leaves the pane via the
//     focus ladder (^Q / Tab), never via Esc — so Esc never jumps to the rail.
func (w *Widget) EscBack() {
	if sl, ok := w.slotAt(w.cursor.Stage, w.cursor.Slot); ok && sl.group != nil && w.Fanned(w.cursor.Stage, w.cursor.Slot) {
		w.collapseCursorGroup()
		w.cursor.Member = -1
		w.maybeNotifyBranchChange()
		return
	}
	if w.DrillDepth() > 0 {
		w.PopOrch()
		if w.OnDrillOut != nil {
			w.OnDrillOut()
		}
		return
	}
	// Root, nothing fanned: consumed no-op (the widget swallows Esc).
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

// warnGlyph decorates the header Tier line when a node's project points at a
// missing/invalid profile (D-VIEW fail-open surfacing).
const warnGlyph = '⚠'

// descMaxLines caps how many leading NON-EMPTY prompt lines the node-view header
// shows as the description (improve-hera-node-descriptions). Policy-agnostic: the
// first N non-empty lines are shown verbatim (each truncated to the pane width by
// DrawText) — no line is stripped, skipped, or classified as boilerplate. Grown
// from the original single first-line so a coordinator reads the mission, not one
// truncated line. Header height is sized from this so the diagram budget stays
// fixed regardless of the cursor target. See gotchas/hera-view.md.
const descMaxLines = 3

// headerContentRows is the fixed number of content lines the header strip
// occupies (D9 + BUG-006): the node view's worst case — name / status / tier /
// up to descMaxLines description lines / feeds — which also covers the 3-line
// group view (range·title / members / downstream, padded). Held constant
// (derived from descMaxLines) so the diagram budget never drifts with the
// cursor target.
const headerContentRows = 4 + descMaxLines // name + status + tier + feeds (4 fixed) + description lines

// headerHeight is the total fixed header height: the content rows plus a one-row
// separator rule. The diagram region is the panel inner height minus this,
// mirroring DetailsView.ContentHeight's exact-budget discipline (D9).
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

// nodeHeaderLines renders the node-view header: the role name, its Status (the
// node's state word + glyph; BUG-006), its description (the first descMaxLines
// non-empty lines of the stored prompt), and what it feeds (the downstream nodes
// it blocks, by label) (D9). The Status glyph uses the SAME resolved icon the
// node box shows (1:1 with the rail for a live node, BUG-007), so the header and
// the diagram never disagree; the word is the State.Label() vocabulary (planned /
// working / done / …).
//
// The description is the mission's opening lines, not a single truncated line
// (improve-hera-node-descriptions): descriptionLines yields up to descMaxLines
// rows (each truncated to the pane width by DrawText). The returned slice is
// [name, status, <desc rows…>, feeds]; HeaderLines pads/clamps it to
// headerContentRows (= 3 + descMaxLines), so the feeds line is always kept and
// the header stays within its fixed budget.
func (w *Widget) nodeHeaderLines(id string) []string {
	n := w.nodes[id]
	feeds := w.feedLabels(id)
	feedsLine := "Feeds: " + strings.Join(feeds, ", ")
	if len(feeds) == 0 {
		feedsLine = "Feeds: (nothing)"
	}
	statusLine := fmt.Sprintf("Status: %c %s", w.headerStatusGlyph(n), n.State.Label())
	descLines := descriptionLines(n.Description)
	lines := make([]string, 0, 3+len(descLines)+1)
	lines = append(lines, n.Name, statusLine, nodeTierLine(n))
	lines = append(lines, descLines...)
	lines = append(lines, feedsLine)
	return lines
}

// descriptionLines returns the header description rows for a node's stored prompt:
// its first descMaxLines NON-EMPTY lines, each trimmed of surrounding whitespace
// (DrawText truncates each to the pane width at render time). It is
// POLICY-AGNOSTIC: no line is stripped, skipped by content, or classified as
// boilerplate — a clean prompt shows its opening mission lines, a still-polluted
// prompt shows its opening lines as-is (the fix for pollution is upstream prompt
// hygiene, not view-side stripping). An empty/all-blank prompt yields the single
// "(no description)" placeholder row.
func descriptionLines(prompt string) []string {
	out := make([]string, 0, descMaxLines)
	for _, ln := range strings.Split(prompt, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		out = append(out, ln)
		if len(out) == descMaxLines {
			break
		}
	}
	if len(out) == 0 {
		return []string{"(no description)"}
	}
	return out
}

// nodeTierLine renders the diligence-tiering readout (D-VIEW): the node's
// archetype and the model/effort applied to it, or a ⚠ warning when the project's
// bound profile is missing/invalid (the runtime fail-open case the operator must
// see). "(none)" means the node carries no archetype, so no profile was consulted
// and the agent ran on the project/backend default.
func nodeTierLine(n Node) string {
	if n.ProfileWarning != "" {
		if n.Archetype != "" {
			return fmt.Sprintf("Tier: %s  %c %s", n.Archetype, warnGlyph, n.ProfileWarning)
		}
		return fmt.Sprintf("Tier: %c %s", warnGlyph, n.ProfileWarning)
	}
	if n.Archetype == "" {
		return "Tier: (none)"
	}
	model := n.Model
	if model == "" {
		model = "(default)"
	}
	line := fmt.Sprintf("Tier: %s → %s", n.Archetype, model)
	if n.Effort != "" {
		line += " /" + n.Effort
	}
	return line
}

// headerStatusGlyph is the glyph shown on the header Status line: the node's
// resolved status icon (1:1 with the rail + the box, BUG-007) when the projection
// stamped one, falling back to the State glyph for planned/failed overlays and
// the test projection. For the animated "working" case it shows a representative
// spinner frame (the header is static text — it does not animate per-frame).
func (w *Widget) headerStatusGlyph(n Node) rune {
	if n.Icon != nil {
		if n.Icon.Animated {
			return widget.SpinnerFrame(w.animFrame)
		}
		return n.Icon.Glyph
	}
	return n.State.Glyph()
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
	// Recompute the spinner frame once per Draw so Animated node icons animate in
	// lockstep with the rail (BUG-007); cheap, wall-clock-derived (frameFn is the
	// injectable seam tests pin for determinism).
	w.animFrame = w.spinnerFrame()
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
		// Nothing to lay out — render the empty-plan placeholder. The hera Plan
		// pane feeds an EMPTY node set here whenever no plan is authored (no planned
		// nodes, no blocking edges), so the live worker roles are NOT drawn as a
		// flat pseudo-DAG stage (BUG-013); the live agents are the rail's concern.
		w.drawEmptyPlan(screen, inner)
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
	// Footer hint bar pinned to the bottom inner row; the diagram region loses
	// that row. Drawn last (below) so a tall diagram never paints over it.
	if diagram.H > 1 {
		footerRow := diagram.Y + diagram.H - 1
		w.drawFooter(screen, diagram.X, footerRow, diagram.W)
		diagram.H--
	}
	if w.noPlan {
		widget.DrawText(screen, diagram.X, diagram.Y, diagram.W, "no plan authored — live roles:", theme.StyleDimmed)
	}
	w.drawStages(screen, diagram)
}

// drawEmptyPlan renders the empty-plan placeholder shown when the widget has no
// stages to lay out (an empty node set). The hera Plan pane feeds an empty set
// whenever no plan is authored (no planned nodes, no blocking edges), so the
// live worker roles are NOT drawn as a flat pseudo-DAG (BUG-013) — the live
// agents are the rail's concern; the plan graph depicts only the AUTHORED plan.
// Two dim, centered lines: the state and an authoring hint.
func (w *Widget) drawEmptyPlan(screen tcell.Screen, inner widget.InnerRect) {
	lines := []string{
		"No plan authored.",
		"Author one with hera_plan_node / hera_plan.",
	}
	top := inner.Y + (inner.H-len(lines))/2
	if top < inner.Y {
		top = inner.Y
	}
	for i, line := range lines {
		row := top + i
		if row >= inner.Y+inner.H {
			break
		}
		col := inner.X + (inner.W-len(line))/2
		if col < inner.X {
			col = inner.X
		}
		widget.DrawText(screen, col, row, inner.W, line, theme.StyleDimmed)
	}
}

// footerHint is the dim bottom-row nav legend (D-render). Kept ASCII-light so
// the single-width truncation math in DrawText holds.
const footerHint = "↑↓ stage · ←→ within · Enter fan · Esc back"

// drawFooter paints the dim nav legend on a single row, clipped to width.
func (w *Widget) drawFooter(screen tcell.Screen, x, y, maxW int) {
	if maxW <= 0 {
		return
	}
	widget.DrawText(screen, x, y, maxW, footerHint, theme.StyleDimmed)
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

// Box-rendering geometry (the artifact's "full boxed treatment").
const (
	// boxGap is the blank-column run between two sibling boxes on a stage row.
	boxGap = 1
	// boxHPad is the horizontal padding inside a node box (one space each side).
	boxHPad = 1
	// nodeBoxH is the fixed height of a node box: top border + content + bottom.
	nodeBoxH = 3
	// groupPad is the dashed enclosure's inner padding on each horizontal side.
	groupPad = 1
)

// stageBlock is one stage's laid-out drawable: its total width/height (cells)
// and a draw closure that paints it with its top-left at (x, y). drawStages
// centers each block horizontally and stacks them with a connector between.
// selW > 0 only for the stage that holds the cursor: selRelX/selW are the
// selected box's x-offset (relative to the block's left edge) and width, used by
// the horizontal viewport to ensure-visible the selection (BUG-010).
type stageBlock struct {
	width   int
	height  int
	selRelX int
	selW    int
	draw    func(screen tcell.Screen, x, y int, clip clipRect)
}

// clipRect bounds painting to the diagram region so a scrolled/overflowing box
// never writes outside it (and never over the footer). Cells outside are dropped.
type clipRect struct{ x0, y0, x1, y1 int } // [x0,x1) × [y0,y1)

func (c clipRect) contains(x, y int) bool {
	return x >= c.x0 && x < c.x1 && y >= c.y0 && y < c.y1
}

// edgeMoreLeft / edgeMoreRight are the off-screen content indicators drawn at a
// stage row's left / right pane edge when sibling boxes are hidden by the
// horizontal viewport (BUG-010). Single-width, consistent with the plan's
// single-line glyph vocabulary.
const (
	edgeMoreLeft  = '‹'
	edgeMoreRight = '›'
)

// drawStages lays each stage out as a boxed block (node = rounded box, group =
// dashed enclosure), centers each block horizontally, stacks them vertically
// with a centered `│` connector between, and vertical-scrolls so the cursor's
// stage block stays in view when the plan overflows. When the widest stage
// overflows the diagram width it also scrolls HORIZONTALLY so the cursor's
// selected box stays fully visible, drawing `‹`/`›` edge indicators where
// content is hidden (BUG-010). No screen.Sync; every cell is clipped to the
// diagram region (full-rect coverage by DrawBorderedPanel).
func (w *Widget) drawStages(screen tcell.Screen, inner widget.InnerRect) {
	regionTop := inner.Y
	if w.noPlan {
		regionTop++ // leave the hint line above the flat stage
	}
	regionH := inner.Y + inner.H - regionTop
	if regionH <= 0 {
		return
	}
	clip := clipRect{x0: inner.X, y0: regionTop, x1: inner.X + inner.W, y1: inner.Y + inner.H}

	blocks := make([]stageBlock, len(w.stages))
	for s := range w.stages {
		blocks[s] = w.buildStageBlock(s, inner.W)
	}

	// Total block height: each stage's height plus a 1-row connector between.
	totalH := 0
	maxBlockW := 0
	for i, b := range blocks {
		totalH += b.height
		if i > 0 {
			totalH++ // connector row
		}
		if b.width > maxBlockW {
			maxBlockW = b.width
		}
	}

	// Vertical placement: center the block when it fits; otherwise scroll so the
	// cursor's stage block is fully visible within the region.
	startY := regionTop
	if regionH > totalH {
		startY += (regionH - totalH) / 2
	} else {
		startY -= w.scrollOffsetFor(blocks, regionH)
	}

	// Horizontal viewport: scroll only when the widest stage overflows the pane.
	// In scroll mode every block is left-aligned at inner.X and shifted left by
	// xOffset; otherwise each block stays centered and the offset is 0.
	scrollX := maxBlockW > inner.W
	if scrollX {
		w.xOffset = w.ensureCursorVisibleX(blocks, inner.W)
	} else {
		w.xOffset = 0
	}

	y := startY
	for s, b := range blocks {
		bx := inner.X
		if scrollX {
			bx = inner.X - w.xOffset
		} else if inner.W > b.width {
			bx += (inner.W - b.width) / 2
		}
		b.draw(screen, bx, y, clip)
		if scrollX {
			w.drawEdgeIndicators(screen, inner, clip, b, y)
		}
		y += b.height
		// Connector to the next stage, hung under this block's center (clamped to
		// the visible region so a scrolled block keeps a sane connector).
		if s < len(blocks)-1 {
			ec := bx + b.width/2
			if ec < inner.X {
				ec = inner.X
			}
			if ec > inner.X+inner.W-1 {
				ec = inner.X + inner.W - 1
			}
			if clip.contains(ec, y) {
				screen.SetContent(ec, y, '│', nil, theme.StyleDimmed.Foreground(theme.ColorBorder))
			}
			y++ // connector row
		}
	}
}

// drawEdgeIndicators paints the `‹`/`›` off-screen markers on a block's middle
// row when its content is hidden past the left / right pane edge by the current
// xOffset (BUG-010). Drawn over the (clipped) box edge cell — the marker is the
// signal that more siblings exist beyond the pane.
func (w *Widget) drawEdgeIndicators(screen tcell.Screen, inner widget.InnerRect, clip clipRect, b stageBlock, y int) {
	if b.width <= 0 {
		return
	}
	midY := y + b.height/2
	style := theme.StyleDimmed.Foreground(theme.ColorBorder)
	if w.xOffset > 0 { // content hidden to the left
		setIf(screen, clip, inner.X, midY, edgeMoreLeft, style)
	}
	if b.width-w.xOffset > inner.W { // content extends past the right edge
		setIf(screen, clip, inner.X+inner.W-1, midY, edgeMoreRight, style)
	}
}

// edgeGutter is the one-column margin ensure-visible keeps at each pane edge in
// scroll mode, so the `‹`/`›` indicators never overlap the fully-shown selected
// box (BUG-010).
const edgeGutter = 1

// ensureCursorVisibleX returns the horizontal scroll offset (≥0) that keeps the
// cursor's selected box fully within a viewport of width viewW, scrolling the
// minimum from the current offset (BUG-010). selRelX/selW come from the cursor
// stage's block (the dagview-derived layout). A one-column gutter is reserved at
// each edge for the off-screen indicators. With no selectable box it falls back
// to the start (0).
func (w *Widget) ensureCursorVisibleX(blocks []stageBlock, viewW int) int {
	cur := w.cursor.Stage
	if cur < 0 || cur >= len(blocks) || blocks[cur].selW <= 0 {
		return 0
	}
	boxLeft := blocks[cur].selRelX
	boxRight := boxLeft + blocks[cur].selW // exclusive
	off := w.xOffset
	// Scroll right enough to reveal the box's right edge (inside the right gutter).
	if boxRight-off > viewW-edgeGutter {
		off = boxRight - (viewW - edgeGutter)
	}
	// Prefer showing the left edge inside the left gutter (also handles a box
	// wider than the viewport).
	if boxLeft-off < edgeGutter {
		off = boxLeft - edgeGutter
	}
	if off < 0 {
		off = 0
	}
	return off
}

// scrollOffsetFor returns how many rows to shift the block up so the cursor's
// stage is fully visible inside a region of height regionH that the full block
// overflows. Anchors the cursor stage's top into view, then nudges so its bottom
// fits, clamped to [0, totalH-regionH]. The offset is region-relative.
func (w *Widget) scrollOffsetFor(blocks []stageBlock, regionH int) int {
	// Y of each stage block's top, relative to the block origin (offset 0).
	tops := make([]int, len(blocks))
	yy := 0
	for i, b := range blocks {
		tops[i] = yy
		yy += b.height
		if i < len(blocks)-1 {
			yy++ // connector
		}
	}
	totalH := yy
	cur := w.cursor.Stage
	if cur < 0 || cur >= len(blocks) {
		return 0
	}
	cTop := tops[cur]
	cBot := cTop + blocks[cur].height // exclusive
	off := 0
	// Scroll down enough that the cursor block's bottom is visible.
	if cBot > regionH {
		off = cBot - regionH
	}
	// But never hide the cursor block's top.
	if cTop < off {
		off = cTop
	}
	if max := totalH - regionH; off > max {
		off = max
	}
	if off < 0 {
		off = 0
	}
	return off
}

// buildStageBlock lays out one stage into a stageBlock: a row of sibling boxes
// (lone nodes as rounded boxes, collapsed groups as dashed boxes, fanned groups
// as a dashed enclosure wrapping the member node-boxes). The block's width is the
// sum of sibling widths + boxGap between them; its height is the tallest sibling.
// availW is the diagram's inner width, used so a fanned group wraps its member
// boxes onto multiple rows to fit the pane instead of overflowing (BUG-011).
func (w *Widget) buildStageBlock(s int, availW int) stageBlock {
	type sib struct {
		width, height int
		draw          func(screen tcell.Screen, x, y int, clip clipRect)
		// selRelX/selW describe the selected box WITHIN this sib (selW > 0 only for
		// the sib the cursor sits on): a lone node / collapsed group is the whole
		// box; a fanned group with a member selected is that member's box.
		selRelX, selW int
	}
	isCursorStage := w.cursor.Stage == s
	var sibs []sib
	for slotIdx, sl := range w.stages[s] {
		onSlot := isCursorStage && w.cursor.Slot == slotIdx
		switch {
		case sl.group != nil && w.Fanned(s, slotIdx):
			bw, bh, draw, memRelX, memW := w.layoutFannedGroup(s, slotIdx, sl.group, availW)
			sb := sib{width: bw, height: bh, draw: draw}
			if onSlot {
				if memW > 0 { // a member is selected inside the fanned group
					sb.selRelX, sb.selW = memRelX, memW
				} else { // the enclosure slot itself is selected
					sb.selW = bw
				}
			}
			sibs = append(sibs, sb)
		case sl.group != nil:
			// Two-line collapsed box (BUG-005): top = "[range]" + feed indicator,
			// sub = "<role token> · <per-state counts>".
			bw, bh, draw := w.layoutDashedBox(w.groupTopLine(sl.group), w.groupSubSegs(sl.group), "", onSlot)
			sb := sib{width: bw, height: bh, draw: draw}
			if onSlot {
				sb.selW = bw
			}
			sibs = append(sibs, sb)
		default:
			glyph, style := w.nodeGlyph(sl.nodeID)
			content := string(glyph) + " " + w.LabelOf(sl.nodeID)
			bw, bh, draw := w.layoutNodeBox(content, style, onSlot)
			sb := sib{width: bw, height: bh, draw: draw}
			if onSlot {
				sb.selW = bw
			}
			sibs = append(sibs, sb)
		}
	}
	width, height := 0, 0
	selRelX, selW := 0, 0
	cx := 0
	for i, sb := range sibs {
		if i > 0 {
			cx += boxGap
		}
		if sb.selW > 0 {
			selRelX, selW = cx+sb.selRelX, sb.selW
		}
		cx += sb.width
		if sb.height > height {
			height = sb.height
		}
	}
	width = cx
	sibsCopy := sibs
	return stageBlock{
		width:   width,
		height:  height,
		selRelX: selRelX,
		selW:    selW,
		draw: func(screen tcell.Screen, x, y int, clip clipRect) {
			cx := x
			for i, sb := range sibsCopy {
				if i > 0 {
					cx += boxGap
				}
				sb.draw(screen, cx, y, clip)
				cx += sb.width
			}
		},
	}
}

// layoutNodeBox returns the width/height and a draw closure for a single node
// box, one space of horizontal padding inside. SELECTED renders a DOUBLE-LINE
// border (`╔═╗ / ║ <content> ║ / ╚═╝`); UNSELECTED renders the single rounded
// border (`╭─╮ / │ <content> │ / ╰─╯`). The border takes the node's STATE colour
// in BOTH cases (bold when selected and the widget owns focus) — selection is
// conveyed purely by the heavier glyph set, never a colour or a fill (BUG-008): a
// dedicated colour would collide with the green DONE state, and a background fill
// leaks gray around the mid-cell border glyph. The content (glyph + label) always
// keeps its own state colour — no selection override.
func (w *Widget) layoutNodeBox(content string, contentStyle tcell.Style, selected bool) (int, int, func(tcell.Screen, int, int, clipRect)) {
	cw := len([]rune(content))
	innerW := cw + 2*boxHPad
	boxW := innerW + 2 // borders
	border := w.boxBorderStyle(contentStyle, selected)
	draw := func(screen tcell.Screen, x, y int, clip clipRect) {
		if selected {
			w.drawDoubleBox(screen, x, y, boxW, nodeBoxH, border, clip)
		} else {
			w.drawRoundedBox(screen, x, y, boxW, nodeBoxH, border, clip)
		}
		// Content centered-left after the padding, on the middle row.
		put(screen, clip, x+1+boxHPad, y+1, content, contentStyle)
	}
	return boxW, nodeBoxH, draw
}

// layoutDashedBox returns a dashed-bordered box holding a label line and an
// optional sub line (the counts), used for a collapsed group. The dashed edge is
// the collapsed/expandable identity, so it survives selection: UNSELECTED uses
// the light dashed set (`┌╌╌┐ / ╎ … ╎ / └╌╌┘`), SELECTED swaps to a HEAVY dashed
// set (`┏╍╍┓ / ╏ … ╏ / ┗╍╍┛`) so the cue reads without losing the dashed signal
// (BUG-008 — no colour, no fill). topLabel, when non-empty, is embedded into the
// top edge (`┌╌ label ╌┐`) — the fanned-group enclosure uses that variant.
func (w *Widget) layoutDashedBox(label string, sub []countSeg, topLabel string, selected bool) (int, int, func(tcell.Screen, int, int, clipRect)) {
	subW := 0
	for _, seg := range sub {
		subW += len([]rune(seg.text))
	}
	contentW := len([]rune(label))
	if subW > contentW {
		contentW = subW
	}
	if t := len([]rune(topLabel)) + 4; t > contentW+2*groupPad+2 {
		contentW = t - 2*groupPad - 2
	}
	innerW := contentW + 2*groupPad
	boxW := innerW + 2
	nLines := 1
	if subW > 0 {
		nLines = 2
	}
	boxH := nLines + 2
	border := w.dashedBorderStyle(selected)
	draw := func(screen tcell.Screen, x, y int, clip clipRect) {
		w.drawDashedBox(screen, x, y, boxW, boxH, topLabel, border, clip, selected)
		put(screen, clip, x+1+groupPad, y+1, label, theme.StyleNormal)
		// The sub line is painted segment-by-segment so each per-state count keeps
		// its own colour (the working spinner already carries its live frame).
		col := x + 1 + groupPad
		for _, seg := range sub {
			put(screen, clip, col, y+2, seg.text, seg.style)
			col += len([]rune(seg.text))
		}
	}
	return boxW, boxH, draw
}

// layoutFannedGroup lays a fanned group out as a SOLID rounded enclosure wrapping
// the member node-boxes inside (BUG-005, matching the design). The member boxes
// are packed left-to-right and WRAPPED onto multiple rows so the enclosure fits
// the available diagram width (availW) instead of overflowing in one row
// (BUG-011): a new row starts whenever the next box would exceed the inner-width
// budget, and a box wider than the budget on its own still occupies its own row
// (the BUG-010 horizontal viewport then scrolls to it). The enclosure carries the
// group's role label VERTICALLY down its left inner edge (one rune per row, dim)
// and a ▲ collapse affordance at the top-right; each member that feeds downstream
// (g.FeedingMembers) gets a ↘ on its box. The cursor's member box gets the
// selection (double border); the enclosure itself is selected only when the
// cursor rests on the group slot with no member selected.
// The two trailing return values are the selected member's box x-offset
// (relative to the enclosure's left edge) and width, for the horizontal viewport
// (BUG-010); selMemW is 0 unless the cursor sits on a member of THIS group.
func (w *Widget) layoutFannedGroup(s, slotIdx int, g *Group, availW int) (int, int, func(tcell.Screen, int, int, clipRect), int, int) {
	onSlot := w.cursor.Stage == s && w.cursor.Slot == slotIdx
	type mbox struct {
		width, height int
		draw          func(tcell.Screen, int, int, clipRect)
	}
	members := make([]mbox, 0, len(g.Members))
	for memberIdx, id := range g.Members {
		glyph, style := w.nodeGlyph(id)
		content := string(glyph) + " " + w.LabelOf(id)
		if g.FeedingMembers[id] {
			content += " ↘"
		}
		bw, bh, d := w.layoutNodeBox(content, style, onSlot && w.cursor.Member == memberIdx)
		members = append(members, mbox{bw, bh, d})
	}
	// Reserve one extra inner column on the left for the vertical role label when
	// the group has a common token (else the label column is omitted).
	vlabel := []rune(w.commonRoleToken(g.Members))
	labelCol := 0
	if len(vlabel) > 0 {
		labelCol = 1
	}
	// Inner-width budget for the member rows: the available diagram width minus the
	// label column, the enclosure's horizontal padding, and its two borders. At
	// least 1 so a degenerate narrow pane still packs one (overflowing) box per row.
	budget := availW - labelCol - 2*groupPad - 2
	if budget < 1 {
		budget = 1
	}
	// Pack member indices into rows: keep adding to the current row until the next
	// box would exceed the budget, then start a new row. A row always holds at
	// least one box (even if it alone exceeds the budget).
	var rows [][]int
	var cur []int
	curW := 0
	for i, m := range members {
		add := m.width
		if len(cur) > 0 {
			add += boxGap
		}
		if len(cur) > 0 && curW+add > budget {
			rows = append(rows, cur)
			cur = nil
			curW = 0
			add = m.width
		}
		cur = append(cur, i)
		curW += add
	}
	if len(cur) > 0 {
		rows = append(rows, cur)
	}
	// Row geometry: each row's width is the sum of its members + gaps; its height
	// the tallest member. innerW = widest row, innerH = sum of row heights.
	rowHeights := make([]int, len(rows))
	innerW, innerH := 0, 0
	for r, row := range rows {
		rw, rh := 0, 0
		for j, mi := range row {
			if j > 0 {
				rw += boxGap
			}
			rw += members[mi].width
			if members[mi].height > rh {
				rh = members[mi].height
			}
		}
		rowHeights[r] = rh
		if rw > innerW {
			innerW = rw
		}
		innerH += rh
	}
	boxW := labelCol + innerW + 2*groupPad + 2
	boxH := innerH + 2 // rounded top + bottom edges
	enclosureSel := onSlot && w.cursor.Member < 0
	memBase := 1 + labelCol + groupPad
	// Selected member geometry (relative to the enclosure box left edge), mirroring
	// the member-draw loop's x math below. Only the X axis is threaded to the
	// horizontal viewport (BUG-010); wrapping keeps the group within the width in
	// the common case, so the viewport only fires for a lone over-wide member box.
	selMemRelX, selMemW := 0, 0
	for _, row := range rows {
		mcx := memBase
		for j, mi := range row {
			if j > 0 {
				mcx += boxGap
			}
			if onSlot && w.cursor.Member == mi {
				selMemRelX, selMemW = mcx, members[mi].width
			}
			mcx += members[mi].width
		}
	}
	border := w.enclosureBorderStyle(enclosureSel)
	labelStyle := theme.StyleDimmed
	draw := func(screen tcell.Screen, x, y int, clip clipRect) {
		// Selected enclosure (cursor on the group slot, no member) → double-line;
		// otherwise the single rounded enclosure (BUG-008: weight, not colour/fill).
		if enclosureSel {
			w.drawDoubleBox(screen, x, y, boxW, boxH, border, clip)
		} else {
			w.drawRoundedBox(screen, x, y, boxW, boxH, border, clip)
		}
		// ▲ collapse affordance just inside the top-right corner.
		setIf(screen, clip, x+boxW-2, y, '▲', border)
		// Vertical role label down the left inner edge (one rune per row).
		for i, r := range vlabel {
			ry := y + 1 + i
			if ry >= y+boxH-1 {
				break // don't overwrite the bottom border
			}
			setIf(screen, clip, x+1, ry, r, labelStyle)
		}
		// Member boxes, row by row. Each row's top is the running sum of prior row
		// heights below the enclosure's top border.
		ry := y + 1
		for r, row := range rows {
			cx := x + memBase
			for j, mi := range row {
				if j > 0 {
					cx += boxGap
				}
				members[mi].draw(screen, cx, ry, clip)
				cx += members[mi].width
			}
			ry += rowHeights[r]
		}
	}
	return boxW, boxH, draw, selMemRelX, selMemW
}

// commonRoleToken returns the role-name token shared by every member after the
// short-id prefix (the segment after the first '-' in the node name), or "" when
// the members differ or any lacks that segment. Cheap presentation nicety; the
// range label is the fallback.
func (w *Widget) commonRoleToken(members []string) string {
	tokenOf := func(id string) string {
		name := w.nodes[id].Name
		if i := strings.IndexByte(name, '-'); i >= 0 && i+1 < len(name) {
			return name[i+1:]
		}
		return ""
	}
	if len(members) == 0 {
		return ""
	}
	first := tokenOf(members[0])
	if first == "" {
		return ""
	}
	for _, m := range members[1:] {
		if tokenOf(m) != first {
			return ""
		}
	}
	return first
}

// boxBorderStyle is the node box's border style: the node's STATE colour (the fg
// from contentStyle) for BOTH selected and unselected, made BOLD only when the
// box is selected AND the widget owns focus. Selection is carried by the glyph
// set (double vs rounded — see layoutNodeBox), never by hue or a fill (BUG-008):
// a dedicated colour would collide with the green DONE state.
func (w *Widget) boxBorderStyle(contentStyle tcell.Style, selected bool) tcell.Style {
	fg, _, _ := contentStyle.Decompose()
	style := tcell.StyleDefault.Foreground(fg)
	if selected && w.focused {
		style = style.Bold(true)
	}
	return style
}

// dashedBorderStyle is the collapsed-group dashed border style: the dim border
// colour, made bold only when selected and the widget owns focus. The dashed vs
// heavy-dashed glyph set (layoutDashedBox) is what conveys selection.
func (w *Widget) dashedBorderStyle(selected bool) tcell.Style {
	style := theme.StyleDimmed.Foreground(theme.ColorBorder)
	if selected && w.focused {
		style = style.Bold(true)
	}
	return style
}

// enclosureBorderStyle is the fanned-group enclosure border style: the dim border
// colour, bold only when the enclosure slot is selected and the widget owns
// focus. The rounded vs double glyph set (layoutFannedGroup) conveys selection.
func (w *Widget) enclosureBorderStyle(selected bool) tcell.Style {
	style := theme.StyleDimmed.Foreground(theme.ColorBorder)
	if selected && w.focused {
		style = style.Bold(true)
	}
	return style
}

// drawRoundedBox paints a rounded-corner box (`╭─╮ │ │ ╰─╯`) at (x,y) of the
// given width/height, clipped to the region. The UNSELECTED node box.
func (w *Widget) drawRoundedBox(screen tcell.Screen, x, y, bw, bh int, style tcell.Style, clip clipRect) {
	drawBoxFrame(screen, x, y, bw, bh, style, clip, '╭', '╮', '╰', '╯', '─', '│')
}

// drawDoubleBox paints a double-line box (`╔═╗ ║ ║ ╚═╝`) at (x,y). The SELECTED
// node box (BUG-008): selection is the heavier glyph set, in the node's own state
// colour, so it survives any state colour and never collides with a state hue.
func (w *Widget) drawDoubleBox(screen tcell.Screen, x, y, bw, bh int, style tcell.Style, clip clipRect) {
	drawBoxFrame(screen, x, y, bw, bh, style, clip, '╔', '╗', '╚', '╝', '═', '║')
}

// drawDashedBox paints a dashed-edge box; light dashed (`┌╌╌┐ ╎ ╎ └╌╌┘`) when not
// heavy, heavy dashed (`┏╍╍┓ ╏ ╏ ┗╍╍┛`) when heavy (the SELECTED collapsed group —
// keeps the dashed identity while reading as selected). When topLabel is non-empty
// it is embedded into the top edge as `┌╌ label ╌…┐` (heavy: `┏╍ label ╍…┓`).
func (w *Widget) drawDashedBox(screen tcell.Screen, x, y, bw, bh int, topLabel string, style tcell.Style, clip clipRect, heavy bool) {
	tl, tr, bl, br, horiz, vert := '┌', '┐', '└', '┘', '╌', '╎'
	dash := "╌"
	if heavy {
		tl, tr, bl, br, horiz, vert = '┏', '┓', '┗', '┛', '╍', '╏'
		dash = "╍"
	}
	drawBoxFrame(screen, x, y, bw, bh, style, clip, tl, tr, bl, br, horiz, vert)
	if topLabel != "" && bw >= len([]rune(topLabel))+4 {
		// `┌╌ label ╌╌┐`: corner, one dash, space, label, space, dashes…
		put(screen, clip, x+1, y, dash+" "+topLabel+" ", style)
	}
}

// drawBoxFrame paints a generic box frame with the given corner/edge runes,
// clipped. Interior is left untouched (DrawBorderedPanel already blanked it).
func drawBoxFrame(screen tcell.Screen, x, y, bw, bh int, style tcell.Style, clip clipRect, tl, tr, bl, br, horiz, vert rune) {
	if bw < 2 || bh < 2 {
		return
	}
	setIf(screen, clip, x, y, tl, style)
	setIf(screen, clip, x+bw-1, y, tr, style)
	setIf(screen, clip, x, y+bh-1, bl, style)
	setIf(screen, clip, x+bw-1, y+bh-1, br, style)
	for i := 1; i < bw-1; i++ {
		setIf(screen, clip, x+i, y, horiz, style)
		setIf(screen, clip, x+i, y+bh-1, horiz, style)
	}
	for j := 1; j < bh-1; j++ {
		setIf(screen, clip, x, y+j, vert, style)
		setIf(screen, clip, x+bw-1, y+j, vert, style)
	}
}

// setIf paints one cell when it lies inside the clip region.
func setIf(screen tcell.Screen, clip clipRect, x, y int, r rune, style tcell.Style) {
	if clip.contains(x, y) {
		screen.SetContent(x, y, r, nil, style)
	}
}

// put paints a rune-aware string starting at (x,y), one cell per rune, clipped.
func put(screen tcell.Screen, clip clipRect, x, y int, s string, style tcell.Style) {
	col := x
	for _, r := range s {
		if clip.contains(col, y) {
			screen.SetContent(col, y, r, nil, style)
		}
		col++
	}
}

// groupCounts renders the aggregate per-state counts for a collapsed group box,
// e.g. "3 ✓ · 2 ⟳ · 1 ○". States with a zero count are omitted; the order
// follows the State enum for stability. Retained as a flat-string helper (and
// for TestGroupCounts_IncludesCancelled); the live render path uses
// groupCountSegs for per-segment colour + spinner animation (BUG-011).
func groupCounts(g *Group) string {
	var parts []string
	for _, s := range []State{StateDone, StateInReview, StateWorking, StatePending, StateFailed, StateCancelled, StatePlanned} {
		if c := g.Counts[s]; c > 0 {
			parts = append(parts, fmt.Sprintf("%d %c", c, s.Glyph()))
		}
	}
	return strings.Join(parts, " · ")
}

// countSeg is one styled run of the collapsed-group count line: either a
// "<count> <glyph>" segment in its per-state colour or a dim " · " separator
// (BUG-011). Painting per-segment (rather than a single flat string) is what
// lets a mixed group convey each state's colour and animate the working spinner.
type countSeg struct {
	text  string
	style tcell.Style
}

// countSegGlyph resolves the rail-vocabulary glyph for a collapsed-count segment
// of state s. It is 1:1 with the rail + the plan NODES (BUG-007/011): rather than
// the compact State.Glyph() set (◔/⟳), it synthesises a widget.RoleStatusInputs
// from the State and calls the SHARED classifier widget.RoleStatusIcon — the same
// fn the rail's statusIcon and the node's planNodeIcon use — so the count can
// never drift from the rail. The three plan-only overlays the rail has no concept
// of (planned ○ / failed ✕ / cancelled ✕) fall back to the State glyph. The
// working segment animates via the live spinner frame.
//
// Only the GLYPH comes from the classifier; the COLOUR is the caller's
// State.style() (the per-state node-border colour). The classifier's
// ready_to_close style is green (reserved for done) where the count line wants
// in_review cyan, so glyph and colour are sourced separately on purpose.
func countSegGlyph(s State, frame int) rune {
	switch s {
	case StatePlanned, StateFailed, StateCancelled:
		return s.Glyph() // ○ / ✕ / ✕ overlays — the rail has neither
	}
	var in widget.RoleStatusInputs
	switch s {
	case StateDone:
		in.Done = true // → ✓
	case StateWorking:
		in.Active = true // → animated spinner
	case StateInReview:
		in.ReadyToClose = true // → clipboard 󰂼 (theme.IconReview), NOT ◔
	case StatePending:
		in.Idle = true // → moon outline
	}
	glyph, _ := widget.RoleStatusIcon(in, false, frame)
	return glyph
}

// groupCountSegs renders the aggregate per-state counts for a collapsed group box
// as styled segments, e.g. "3 ✓"(green) · "2 <spinner>"(amber) · "1 ○"(violet).
// States with a zero count are omitted; the order follows the State enum for
// stability. Each count segment carries its state's colour (State.style() — the
// same per-state colour the node box uses); the " · " separators stay dim. The
// glyphs are 1:1 with the rail (countSegGlyph), and the working spinner re-resolves
// the frame from w.animFrame each Draw (layout runs per Draw) so it animates.
func (w *Widget) groupCountSegs(g *Group) []countSeg {
	var segs []countSeg
	for _, s := range []State{StateDone, StateInReview, StateWorking, StatePending, StateFailed, StateCancelled, StatePlanned} {
		c := g.Counts[s]
		if c == 0 {
			continue
		}
		if len(segs) > 0 {
			segs = append(segs, countSeg{text: " · ", style: theme.StyleDimmed})
		}
		segs = append(segs, countSeg{
			text:  fmt.Sprintf("%d %c", c, countSegGlyph(s, w.animFrame)),
			style: s.style(),
		})
	}
	return segs
}

// nodeGlyph returns a node's status glyph + style for rendering. When the
// projection stamped a resolved Icon (a LIVE node, classified 1:1 with the rail
// via the shared widget.RoleStatusIcon — BUG-007) it uses that, re-resolving the
// spinner frame at Draw for the Animated "working" case so it animates in
// lockstep with the rail. Otherwise (planned ○ / failed ✕ overlays, or the
// single-arg test projection that stamps no Icon) it falls back to the State
// glyph + style, preserving the prior behaviour.
func (w *Widget) nodeGlyph(id string) (rune, tcell.Style) {
	n := w.nodes[id]
	if n.Icon != nil {
		if n.Icon.Animated {
			return widget.SpinnerFrame(w.animFrame), n.Icon.Style
		}
		return n.Icon.Glyph, n.Icon.Style
	}
	return n.State.Glyph(), n.State.style()
}

// planSpinnerFrame computes the current spinner animation frame from wall-clock
// time, mirroring the rail's spinnerFrame so an Animated plan node advances in
// lockstep. Recomputed at the top of each Draw.
func planSpinnerFrame() int {
	interval := widget.SpinnerTickInterval()
	if interval <= 0 {
		return 0
	}
	return int(time.Now().UnixMilli()/interval.Milliseconds()) % widget.SpinnerFrameCount()
}

// spinnerFrame yields the current spinner frame, honouring an injected frameFn
// (tests pin it for determinism) and falling back to the wall-clock
// planSpinnerFrame for a normally-constructed or zero-value widget.
func (w *Widget) spinnerFrame() int {
	if w.frameFn != nil {
		return w.frameFn()
	}
	return planSpinnerFrame()
}

// groupTopLine is the collapsed box's first line (BUG-005): the bare range label
// plus a feed indicator — "→ <target>" when the group fully feeds a single
// downstream node, "↘" when only some members feed (partial), nothing otherwise.
// The bare planned-count that used to trail the range (which read as "blocks 3a")
// is gone — the count moved to the sub-line.
func (w *Widget) groupTopLine(g *Group) string {
	switch {
	case g.FeedTarget != "":
		return g.Label + " → " + g.FeedTarget
	case g.PartialFeed:
		return g.Label + " ↘"
	default:
		return g.Label
	}
}

// groupSubSegs is the collapsed box's second line (BUG-005) as styled segments:
// the group's common role token (e.g. "research", "drafting") in dim, then the
// per-state counts (each in its state colour), joined by a dim " · " —
// "research · 1 ✓ · 2 <spinner>". Falls back to just the counts when the members
// share no common role token, and to just the token when there are no counts.
func (w *Widget) groupSubSegs(g *Group) []countSeg {
	counts := w.groupCountSegs(g)
	tok := w.commonRoleToken(g.Members)
	if tok == "" {
		return counts
	}
	segs := []countSeg{{text: tok, style: theme.StyleDimmed}}
	if len(counts) > 0 {
		segs = append(segs, countSeg{text: " · ", style: theme.StyleDimmed})
		segs = append(segs, counts...)
	}
	return segs
}

// InputHandler routes the 4-way navigation (↑↓ stage, ←→ slot/member), Enter
// (fan a collapsed group / navigate a member or leaf / drill-in), Space (pure
// fan-out/collapse toggle, never navigate — BUG-013 follow-up), and Esc
// (collapse / drill-out). Unknown keys fall through to the default tview.Box no-op.
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
			w.EscBack()
		case tcell.KeyRune:
			switch event.Rune() {
			case ' ':
				w.ToggleCursorFan()
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

// Package planview is the TUI widget that renders a Hera *plan DAG* — planned
// and live worker roles as nodes, hera_blocks blocking edges between them — as
// the "tight tree" UX (short-id labels, auto-collapsing parallel groups,
// 4-way cursor navigation, a master-detail header, and sub-coordinator
// drill-in). It replaces the orchestration-tree graph (heraTreeNodes) in the
// Hera Details pane and reuses dagview's Kahn longest-path layer math for stage
// placement. See openspec/changes/add-hera-plan-view/design.md.
//
// Stage 1 (this commit) defines the API surface. Production functions are
// signature-only stubs returning zero values (so the package compiles and the
// Stage-1 tests fail on assertions — true Red); Stages 3–6 implement the
// layout, render, navigation, header, and drill-in logic.
package planview

import (
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
//
// Stage 3 implements this.
func (s State) Glyph() rune { return ' ' }

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

// ParseShortID parses a role name's short-id prefix (`2c-fact-checker` → {Stage:2,
// Member:"c", Label:"2c", OK:true}). A name with no parseable prefix yields a
// truncated-name Label with OK false. The short-id is presentation only; it
// never drives layout (D3).
//
// Stage 3 implements this.
func ParseShortID(name string) ShortID { return ShortID{} }

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
}

// New constructs an empty plan-view widget. SetData must be called before the
// widget is meaningful.
//
// Stage 3 implements this; the Stage-1 stub returns a Box-backed shell so the
// nav/header/drill-in tests can construct and drive it (and fail on assertions).
func New() *Widget {
	return &Widget{Box: tview.NewBox()}
}

// SetTitle overrides the bordered-panel title (the Hera Details pane sets it to
// " Plan "). Pass "" to suppress the title text.
//
// Stage 3 implements this.
func (w *Widget) SetTitle(string) {}

// SetData installs a new plan snapshot: the node set plus the blocking edges.
// Recomputes the stage layout (Kahn longest-path over the edges, D3), detects
// parallel groups (D4), and clamps the cursor to the new node set.
//
// Stage 3 implements this.
func (w *Widget) SetData([]Node, []Edge) {}

// Title returns the bordered-panel title for the currently-displayed
// orchestrator (e.g. "Details ▸ <orch> · Plan" when drilled in, D6).
//
// Stage 3/6 implements this.
func (w *Widget) Title() string { return "" }

// SetFocused toggles keyboard focus (the cursor renders more prominently when
// the widget owns focus).
//
// Stage 3 implements this.
func (w *Widget) SetFocused(bool) {}

// Stages returns the number of computed stages (longest-path layers) in the
// current layout. 0 when empty.
//
// Stage 3 implements this.
func (w *Widget) Stages() int { return 0 }

// NoPlan reports whether the current snapshot has no plan authored (no planned
// nodes and no edges) — the degenerate flat-single-stage render (D1).
//
// Stage 3 implements this.
func (w *Widget) NoPlan() bool { return false }

// StageOf returns the computed longest-path stage of a node by ID, and whether
// the node is present. Layout truth (edge-driven), independent of the short-id
// number (D3).
//
// Stage 3 implements this.
func (w *Widget) StageOf(id string) (int, bool) { return 0, false }

// LabelOf returns the rendered chip label for a node by ID (its short-id, or
// the truncated-name fallback). Empty when the node is absent.
//
// Stage 3 implements this.
func (w *Widget) LabelOf(id string) string { return "" }

// GroupAt returns the collapsed parallel group occupying (stage, slot), and
// whether that slot is a group (vs a lone node).
//
// Stage 3 implements this.
func (w *Widget) GroupAt(stage, slot int) (Group, bool) { return Group{}, false }

// SlotCount returns the number of slots (lone nodes + collapsed groups) in a
// stage.
//
// Stage 3 implements this.
func (w *Widget) SlotCount(stage int) int { return 0 }

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
//
// Stage 3 implements this.
func (w *Widget) Draw(screen tcell.Screen) { w.DrawForSubclass(screen, w) }

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
//
// Stage 4 implements this.
func (w *Widget) PasteHandler() func(string, func(tview.Primitive)) {
	return w.WrapPasteHandler(func(string, func(tview.Primitive)) {})
}
